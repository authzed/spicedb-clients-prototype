# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'
require 'timeout'

# Abandoning a stream must release it. Root DESIGN.md, "RULE: Abandoning a
# stream must release it".
#
# Clause 3 of that rule is about exactly this client: where a language's
# iteration protocol closes a generator on `break`, that only releases the
# stream if the transport underneath honors it -- "and this is a trap
# precisely because the call site looks identical whether it does or not."
# Ruby is on the honoring side of that line, and these specs are the
# evidence, because nothing else in the codebase is:
#
#   * `break`ing out of `Enumerator#each` unwinds into the generator block's
#     Fiber, so grpc-ruby's own `ensure set_input_stream_done` in
#     ActiveCall#each_remote_read_then_finish runs. That reaches
#     `maybe_finish_and_close_call_locked`, which calls `@call.close` and
#     tears the RPC down -- synchronously, not on a GC pass.
#   * `first` and `take` stop early through the same unwind, as does an
#     Enumerator abandoned mid-external-iteration and then collected.
#
# So there is deliberately no explicit `cancel` anywhere in this client, and
# that absence is easy to misread as the very defect the rule describes -- a
# grep for "cancel" under lib/ finds only SpiceDB::CancelledError. Which is
# why the behavior is pinned here rather than argued in a comment. See
# spicedb-ruby/DESIGN.md, "Stream lifecycle", for the trap that awaits
# anyone who adds one anyway.
#
# These specs assert on a *server-side* signal, never on the consuming loop
# having exited: the loop always exits, leak or no leak. The double-based
# specs elsewhere in this suite cannot prove release at all, since a double
# has no transport under it.
module StreamRelease
  # How long a handler keeps streaming while waiting to be released. Longer
  # than the specs' own wait, so a leak reads as "never signalled" rather
  # than as a race.
  HANDLER_LIFETIME_SECONDS = 8

  # How long a spec waits for the server's signal before declaring a leak.
  RELEASE_WAIT_SECONDS = 5

  # What the client asks for in one page. A handler that sends exactly this
  # many results looks to the client like a full page, so the client will
  # open another stream if -- and only if -- the caller keeps consuming.
  PAGE_SIZE = 512

  def self.relationship_proto
    Authzed::Api::V1::Relationship.new(
      resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'doc1'),
      relation: 'viewer',
      subject: Authzed::Api::V1::SubjectReference.new(
        object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'alice')
      )
    )
  end

  # What the server observed, collected across handler threads.
  class Observations
    def initialize
      @signals = Queue.new
      @opens = Queue.new
    end

    def record_open(name)
      @opens << name
    end

    # Streams +response+ until the client goes away. A client cancellation
    # reaches a grpc-ruby handler as a failure of the send itself, raised
    # into this enumerator; there is no polled flag that flips reliably
    # while a handler is mid-stream.
    #
    # Never ending on its own is the point. A handler that closed the stream
    # itself would let these specs pass without the client having released
    # anything -- which is exactly what makes "the loop exited" worthless as
    # an assertion.
    def streaming(response)
      Enumerator.new do |yielder|
        deadline = Time.now + HANDLER_LIFETIME_SECONDS
        begin
          while Time.now < deadline
            yielder << response
            sleep 0.01
          end
          @signals << :handler_gave_up
        rescue StandardError, GRPC::Core::CallError => e
          @signals << :released
          raise e
        end
      end
    end

    def await_release
      Timeout.timeout(RELEASE_WAIT_SECONDS) { @signals.pop }
    rescue Timeout::Error
      :never_signalled
    end

    def open_count
      @opens.size
    end
  end

  OBSERVATIONS = Observations.new

  # Handlers for the streams that yield each result straight off the wire,
  # plus the two that drain a page first.
  class PermissionsService < Authzed::Api::V1::PermissionsService::Service
    def lookup_subjects(_request, _call)
      OBSERVATIONS.record_open(:lookup_subjects)
      OBSERVATIONS.streaming(
        Authzed::Api::V1::LookupSubjectsResponse.new(
          subject: Authzed::Api::V1::ResolvedSubject.new(
            subject_object_id: 'alice',
            permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
          )
        )
      )
    end

    def export_bulk_relationships(_request, _call)
      OBSERVATIONS.record_open(:export_bulk_relationships)
      OBSERVATIONS.streaming(
        Authzed::Api::V1::ExportBulkRelationshipsResponse.new(
          relationships: [StreamRelease.relationship_proto],
          after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor-1')
        )
      )
    end

    # read_relationships and lookup_resources drain a whole page into an
    # Array before yielding anything, so they cannot strand an open stream
    # -- the page is already finished by the time the caller sees result
    # one. What they CAN leak is work: an auto-pager that keeps fetching
    # pages nobody asked for. Sending exactly a full page makes the client
    # believe more pages exist, so a second open here would mean the
    # abandonment was ignored.
    def read_relationships(_request, _call)
      OBSERVATIONS.record_open(:read_relationships)
      Array.new(PAGE_SIZE) do
        Authzed::Api::V1::ReadRelationshipsResponse.new(
          relationship: StreamRelease.relationship_proto,
          after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor-1')
        )
      end
    end

    def lookup_resources(_request, _call)
      OBSERVATIONS.record_open(:lookup_resources)
      Array.new(PAGE_SIZE) do
        Authzed::Api::V1::LookupResourcesResponse.new(
          resource_object_id: 'doc1',
          permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
          after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor-1')
        )
      end
    end
  end

  class WatchService < Authzed::Api::V1::WatchService::Service
    def watch(_request, _call)
      OBSERVATIONS.record_open(:watch)
      OBSERVATIONS.streaming(
        Authzed::Api::V1::WatchResponse.new(
          changes_through: Authzed::Api::V1::ZedToken.new(token: 'token-1')
        )
      )
    end
  end
end

RSpec.describe 'SpiceDB::Client stream release' do
  before(:all) do
    @server = GRPC::RpcServer.new(pool_size: 8, pool_keep_alive: 0.1)
    @port = @server.add_http2_port('localhost:0', :this_port_is_insecure)
    @server.handle(StreamRelease::PermissionsService.new)
    @server.handle(StreamRelease::WatchService.new)
    @server_thread = Thread.new { @server.run }
    @server.wait_till_running(5)
  end

  after(:all) do
    @server.stop
    @server_thread&.join(5)
  end

  let(:client) { SpiceDB::Client.new_plaintext("localhost:#{@port}", 'stream-release-token') }
  let(:observations) { StreamRelease::OBSERVATIONS }

  # Takes exactly one item and walks away -- the idiom the rule is about.
  # The single iteration is the whole point, hence the disable.
  def take_one(enumerator)
    seen = 0
    enumerator.each do # rubocop:disable Lint/UnreachableLoop
      seen += 1
      break
    end
    expect(seen).to eq(1), 'the stream never produced anything, so nothing was abandoned'
  end

  def expect_released
    expect(observations.await_release).to eq(
      :released
    ), 'the server never saw the stream end: abandoning the Enumerator leaked the gRPC call ' \
       "and SpiceDB's server-side dispatch"
  end

  describe 'a stream that yields straight off the wire' do
    it 'releases lookup_subjects when the caller breaks out' do
      take_one(client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user'))
      expect_released
    end

    it 'releases export_relationships when the caller breaks out' do
      take_one(client.export_relationships(SpiceDB::Consistency.full))
      expect_released
    end

    it 'releases updates when the caller breaks out' do
      take_one(client.updates(['document']))
      expect_released
    end

    # `first` and `take` stop early through the same unwind as `break`, so
    # they must release too -- worth pinning separately, since "just give me
    # the first few" is the scenario the rule opens with.
    it 'releases updates when the caller takes only the first event' do
      expect(client.updates(['document']).first.changes_through).to eq('token-1')
      expect_released
    end
  end

  describe 'a stream that drains a page before yielding' do
    def expect_no_further_page(before_count)
      expect(observations.open_count - before_count).to eq(
        1
      ), 'the client opened another page after the caller walked away -- an abandoned auto-pager must stop fetching'
    end

    it 'does not open another read_relationships page after the caller stops' do
      before_count = observations.open_count

      take_one(client.read_relationships(SpiceDB::Consistency.full, SpiceDB::Filter.new(resource_type: 'document')))

      expect_no_further_page(before_count)
    end

    it 'does not open another lookup_resources page after the caller stops' do
      before_count = observations.open_count

      take_one(client.lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice'))

      expect_no_further_page(before_count)
    end
  end
end
