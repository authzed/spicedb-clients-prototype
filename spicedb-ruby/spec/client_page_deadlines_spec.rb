# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'

# Root DESIGN.md, "Treat a deadline as a budget for the entire operation":
# finite paged reads (relationship reads, lookups, exports) apply the client
# default deadline to each page request, so a server that stalls mid-read
# fails the read instead of hanging it. A caller's total deadline still bounds
# the whole call and wins when earlier; watch keeps no default.
PAGE_STALL = 3.0
# Each page (connection already warm) must fit this with room for a busy machine.
PAGE_DEFAULT = 0.5

class PageStallingPermissionsService < Authzed::Api::V1::PermissionsService::Service
  def initialize(stall_page:, page_delay: 0)
    super()
    @stall_page = stall_page
    @page_delay = page_delay
  end

  # Two results per page; the page number rides in the cursor. Page
  # +stall_page+ stalls before its first result.
  def lookup_resources(request, _call)
    page = request.optional_cursor ? request.optional_cursor.token.to_i : 1
    Enumerator.new do |out|
      sleep(PAGE_STALL) if page == @stall_page
      sleep(@page_delay)
      2.times do |i|
        out << Authzed::Api::V1::LookupResourcesResponse.new(
          resource_object_id: "doc-#{page}-#{i}",
          permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
          after_result_cursor: Authzed::Api::V1::Cursor.new(token: (page + 1).to_s)
        )
      end
    end
  end

  def lookup_subjects(_request, _call)
    Enumerator.new do |_out|
      sleep(PAGE_STALL)
    end
  end

  # One call streams every page; page +stall_page+ stalls.
  def export_bulk_relationships(_request, _call)
    Enumerator.new do |out|
      (1..4).each do |page|
        sleep(PAGE_STALL) if page == @stall_page
        out << Authzed::Api::V1::ExportBulkRelationshipsResponse.new(
          after_result_cursor: Authzed::Api::V1::Cursor.new(token: page.to_s),
          relationships: [Authzed::Api::V1::Relationship.new(
            resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: "doc-#{page}"),
            relation: 'viewer',
            subject: Authzed::Api::V1::SubjectReference.new(
              object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'alice')
            )
          )]
        )
      end
    end
  end
end

class StallingWatchService < Authzed::Api::V1::WatchService::Service
  def watch(_request, _call)
    Enumerator.new do |out|
      sleep(PAGE_DEFAULT * 3)
      out << Authzed::Api::V1::WatchResponse.new(changes_through: Authzed::Api::V1::ZedToken.new(token: 'r1'))
    end
  end
end

RSpec.describe 'SpiceDB::Client per-page default deadlines' do
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }

  def with_server(stall_page: 3, page_delay: 0)
    server = GRPC::RpcServer.new(pool_size: 4, pool_keep_alive: 0.1)
    port = server.add_http2_port('localhost:0', :this_port_is_insecure)
    server.handle(PageStallingPermissionsService.new(stall_page: stall_page, page_delay: page_delay))
    server.handle(StallingWatchService.new)
    Thread.new { server.run }
    server.wait_till_running(5)
    yield port
  ensure
    server&.stop
  end

  def timed
    start = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    error = nil
    begin
      yield
    rescue StandardError => e
      error = e
    end
    [error, Process.clock_gettime(Process::CLOCK_MONOTONIC) - start]
  end

  def lookup(client, **)
    client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice,
                            consistency: SpiceDB::Consistency.full, page_size: 2, **)
  end

  it 'fails a lookup that stalls on page 3 after the per-page default, with no caller option' do
    with_server do |port|
      SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: PAGE_DEFAULT) do |client|
        client.connect!(timeout: 30) # untimed: the per-page allowance covers pages, not connection setup
        seen = []
        error, elapsed = timed { lookup(client).each { |item| seen << item.resource_id } }
        expect(error).to be_a(SpiceDB::DeadlineExceededError)
        expect(seen).to eq(%w[doc-1-0 doc-1-1 doc-2-0 doc-2-1])
        expect(elapsed).to be < PAGE_STALL
      end
    end
  end

  it 'gives each page its own default, so the whole lookup may outlast it' do
    # Every page takes 60% of the default; three pages take nearly twice it.
    with_server(stall_page: nil, page_delay: PAGE_DEFAULT * 0.6) do |port|
      SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: PAGE_DEFAULT) do |client|
        client.connect!(timeout: 30) # untimed: the per-page allowance covers pages, not connection setup
        error, elapsed = timed { lookup(client, limit: 6).to_a }
        expect(error).to be_nil
        expect(elapsed).to be > PAGE_DEFAULT
      end
    end
  end

  it "lets a caller's earlier total deadline bound the whole lookup" do
    with_server(stall_page: nil, page_delay: 0.2) do |port|
      SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: 10) do |client|
        client.connect!(timeout: 30) # untimed: the per-page allowance covers pages, not connection setup
        error, elapsed = timed { lookup(client, limit: 20, timeout: 0.5).to_a }
        expect(error).to be_a(SpiceDB::DeadlineExceededError)
        expect(elapsed).to be < 1.5
      end
    end
  end

  it 'applies no per-page default when the client default is disabled' do
    with_server(stall_page: 2) do |port|
      SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: PAGE_DEFAULT,
                                                              default_deadline: SpiceDB::DefaultDeadline::DISABLED) do |client|
        client.connect!(timeout: 30) # untimed: the per-page allowance covers pages, not connection setup
        error, elapsed = timed { lookup(client, limit: 4).to_a }
        expect(error).to be_nil
        expect(elapsed).to be >= PAGE_STALL
      end
    end
  end

  it 'fails a stalled lookup_subjects after the per-page default' do
    with_server do |port|
      SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: PAGE_DEFAULT) do |client|
        client.connect!(timeout: 30) # untimed: the per-page allowance covers pages, not connection setup
        error, elapsed = timed do
          client.lookup_subjects(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'x'), permission: 'view',
                                 subject_type: 'user', consistency: SpiceDB::Consistency.full).to_a
        end
        expect(error).to be_a(SpiceDB::DeadlineExceededError)
        expect(elapsed).to be < PAGE_STALL
      end
    end
  end

  it 'fails an export whose server stalls before page 3, after the per-page default' do
    with_server do |port|
      SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: PAGE_DEFAULT) do |client|
        client.connect!(timeout: 30) # untimed: the per-page allowance covers pages, not connection setup
        seen = []
        error, elapsed = timed do
          client.export_relationships(consistency: SpiceDB::Consistency.full).each { |item| seen << item.resource_id }
        end
        expect(error).to be_a(SpiceDB::DeadlineExceededError)
        expect(error.message).to match(/per-page deadline/)
        expect(seen).to eq(%w[doc-1 doc-2])
        expect(elapsed).to be < PAGE_STALL
      end
    end
  end

  it 'does not count time the caller spends consuming an export page' do
    with_server(stall_page: nil) do |port|
      SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: PAGE_DEFAULT) do |client|
        client.connect!(timeout: 30) # untimed: the per-page allowance covers pages, not connection setup
        error, = timed do
          client.export_relationships(consistency: SpiceDB::Consistency.full).each { sleep(PAGE_DEFAULT * 1.5) }
        end
        expect(error).to be_nil
      end
    end
  end

  it 'keeps no default on watch' do
    with_server do |port|
      SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: PAGE_DEFAULT) do |client|
        client.connect!(timeout: 30) # untimed: the per-page allowance covers pages, not connection setup
        event = nil
        error, = timed { client.watch { |e| event = e; break } } # rubocop:disable Style/Semicolon
        expect(error).to be_nil
        expect(event.changes_through.to_s).to eq('r1')
      end
    end
  end
end
