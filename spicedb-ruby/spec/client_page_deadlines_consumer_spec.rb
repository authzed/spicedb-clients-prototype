# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'

# Root DESIGN.md, "Treat a deadline as a budget for the entire operation": the
# per-page default deadline bounds only waiting on the server, never the
# caller's processing time. Each spec reads 5 items from a healthy server that
# answers at once, with a 0.2 s per-page default, while the caller spends
# 0.1 s on every item (0.5 s in all). Every read must succeed.
CONSUMER_DEFAULT = 0.2
CONSUMER_ITEMS = 5
CONSUMER_WORK = 0.1

module ConsumerFixtures
  module_function

  def relationship(index)
    Authzed::Api::V1::Relationship.new(
      resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: "doc-#{index}"),
      relation: 'viewer',
      subject: Authzed::Api::V1::SubjectReference.new(object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'alice'))
    )
  end

  def cursor(index) = Authzed::Api::V1::Cursor.new(token: "c#{index}")
end

class HealthyPermissionsService < Authzed::Api::V1::PermissionsService::Service
  def read_relationships(_request, _call)
    Array.new(CONSUMER_ITEMS) do |i|
      Authzed::Api::V1::ReadRelationshipsResponse.new(relationship: ConsumerFixtures.relationship(i), after_result_cursor: ConsumerFixtures.cursor(i))
    end
  end

  def lookup_resources(_request, _call)
    Array.new(CONSUMER_ITEMS) do |i|
      Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: "doc-#{i}", permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                                                    after_result_cursor: ConsumerFixtures.cursor(i))
    end
  end

  def lookup_subjects(_request, _call)
    Array.new(CONSUMER_ITEMS) do |i|
      Authzed::Api::V1::LookupSubjectsResponse.new(
        subject: Authzed::Api::V1::ResolvedSubject.new(subject_object_id: "user-#{i}", permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
      )
    end
  end

  # One relationship per streamed page.
  def export_bulk_relationships(_request, _call)
    Array.new(CONSUMER_ITEMS) do |i|
      Authzed::Api::V1::ExportBulkRelationshipsResponse.new(relationships: [ConsumerFixtures.relationship(i)],
                                                            after_result_cursor: ConsumerFixtures.cursor(i))
    end
  end
end

class HealthyPermissionSetsService < Authzed::Api::Materialize::V0::WatchPermissionSetsService::Service
  def lookup_permission_sets(_request, _call)
    Array.new(CONSUMER_ITEMS) { Authzed::Api::Materialize::V0::LookupPermissionSetsResponse.new }
  end
end

RSpec.describe 'SpiceDB::Client per-page deadlines exclude the caller\'s processing time' do
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }
  let(:readme) { SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme') }
  let(:full) { SpiceDB::Consistency.full }

  around do |example|
    server = GRPC::RpcServer.new(pool_size: 4, pool_keep_alive: 0.1)
    port = server.add_http2_port('localhost:0', :this_port_is_insecure)
    server.handle(HealthyPermissionsService.new)
    server.handle(HealthyPermissionSetsService.new)
    Thread.new { server.run }
    server.wait_till_running(5)
    SpiceDB::Client.new_plaintext("localhost:#{port}", 't', default_timeout: CONSUMER_DEFAULT) do |client|
      @client = client
      example.run
    end
  ensure
    server&.stop
  end

  attr_reader :client

  # Consumes +stream+ slowly; returns the items seen and the elapsed time.
  def consume_slowly(stream)
    start = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    seen = []
    stream.each do |item|
      seen << item
      sleep(CONSUMER_WORK)
    end
    [seen, Process.clock_gettime(Process::CLOCK_MONOTONIC) - start]
  end

  def expect_slow_success(stream)
    seen, elapsed = consume_slowly(stream)
    expect(seen.length).to eq(CONSUMER_ITEMS)
    expect(elapsed).to be > CONSUMER_DEFAULT * 2, 'the caller must have outlasted the per-page default for this spec to mean anything'
  end

  it 'read_relationships' do
    expect_slow_success(client.read_relationships(SpiceDB::Filter.new(resource_type: 'document'), consistency: full, page_size: 10))
  end

  it 'read_relationships with a single request' do
    expect_slow_success(client.read_relationships(SpiceDB::Filter.new(resource_type: 'document'), consistency: full,
                                                                                                  paging: SpiceDB::Paging::SINGLE_REQUEST))
  end

  it 'lookup_resources' do
    expect_slow_success(client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice, consistency: full, page_size: 10))
  end

  it 'lookup_subjects' do
    expect_slow_success(client.lookup_subjects(resource: readme, permission: 'view', subject_type: 'user', consistency: full))
  end

  it 'experimental_lookup_permission_sets' do
    expect_slow_success(client.experimental_lookup_permission_sets)
  end

  it 'export_relationships' do
    expect_slow_success(client.export_relationships(consistency: full))
  end
end
