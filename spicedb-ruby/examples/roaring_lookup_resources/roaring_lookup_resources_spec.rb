# frozen_string_literal: true

require_relative '../spec_helper'
require 'spicedb_proto'
require 'grpc'

# Demonstrates #experimental_roaring_lookup_resources, the wrapper around the
# materialize-tier RoaringLookupResourcesService: a roaring64 bitmap
# (RoaringFormatSpec 64-bit portable format) of the resource object IDs a
# subject has a permission on, meant to be consumed directly by a search index
# (e.g. OpenSearch's `bitmap` term query) rather than decoded by this client.
#
# Why this example stands up its own server, like error_mapping and
# custom_tls do: `authzed/spicedb:latest`, the image the integration job
# starts, lists no `authzed.api.materialize.v0.RoaringLookupResourcesService`
# via gRPC reflection (verified, not assumed) -- this RPC is not served by the
# shared SpiceDB the other examples run against. A stand-in implementing the
# generated `RoaringLookupResourcesService::Service` exercises the real
# request/response mapping this client added -- consistency/resource
# type/permission/subject on the way out, bitmap bytes/cardinality/ZedToken on
# the way back -- without depending on upstream server support that does not
# exist yet.
module RoaringLookupResourcesStandIns
  # A minimal RoaringLookupResourcesService that answers only what this
  # example asks of it, and records the request it was called with so the
  # example can assert the client built it correctly.
  class StandInService < Authzed::Api::Materialize::V0::RoaringLookupResourcesService::Service
    attr_reader :last_request

    def experimental_roaring_lookup_resources(request, _call)
      @last_request = request

      # Mirrors the real service's FAILED_PRECONDITION for a resource type
      # whose relationships carry a non-canonical object ID (e.g. "007"
      # instead of "7") -- triggered here by a sentinel permission name,
      # since this stand-in has no relationships to violate the rule for
      # real.
      if request.permission == 'non-canonical-ids'
        raise GRPC::FailedPrecondition,
              'resource object id "007" is not a canonical 44-bit integer'
      end

      Authzed::Api::Materialize::V0::ExperimentalRoaringLookupResourcesResponse.new(
        # Not a real roaring64 payload -- this stand-in only proves the bytes
        # round-trip through `bitmap` unchanged, which is all the client does
        # with them (it does not decode roaring itself).
        bitmap: [1, 2, 3, 4].pack('C*'),
        cardinality: 2,
        at_revision: Authzed::Api::V1::ZedToken.new(token: 'rev-1')
      )
    end
  end
end

RSpec.describe 'RoaringLookupResources', :no_spicedb do
  def start_stand_in_server
    service = RoaringLookupResourcesStandIns::StandInService.new
    server = GRPC::RpcServer.new(pool_size: 4)
    port = server.add_http2_port('127.0.0.1:0', :this_port_is_insecure)
    server.handle(service)
    thread = Thread.new { server.run }
    server.wait_till_running(5)
    [server, thread, service, port]
  end

  def stop_stand_in_server(server, thread)
    server.stop
    thread.join(5)
  end

  it 'returns the bitmap, cardinality, and revision, and sends the request the client built' do
    server, thread, service, port = start_stand_in_server
    begin
      SpiceDB::Client.new_plaintext("127.0.0.1:#{port}", SPICEDB_TOKEN) do |client|
        result = client.experimental_roaring_lookup_resources(resource_type: 'document', permission: 'view',
                                                              subject: SpiceDB::SubjectRef.new(
                                                                object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')
                                                              ),
                                                              consistency: SpiceDB::Consistency.full)

        expect(result).to be_a(SpiceDB::RoaringLookupResourcesResult)
        expect(result.bitmap).to eq([1, 2, 3, 4].pack('C*'))
        expect(result.cardinality).to eq(2)
        expect(result.at_revision).to eq(SpiceDB::ZedToken.new('rev-1'))

        # What actually reached the stand-in -- proves the request was built
        # correctly, not merely that some response came back.
        req = service.last_request
        expect(req.resource_object_type).to eq('document')
        expect(req.permission).to eq('view')
        expect(req.subject.object.object_type).to eq('user')
        # Bracket access, not `.object_id` -- ObjectReference's "object_id"
        # field collides with Kernel#object_id, and the generated protobuf
        # accessor does not override it.
        expect(req.subject.object['object_id']).to eq('alice')
      end
    ensure
      stop_stand_in_server(server, thread)
    end
  end

  it 'raises SpiceDB::FailedPreconditionError for a non-canonical resource object id' do
    server, thread, _service, port = start_stand_in_server
    begin
      SpiceDB::Client.new_plaintext("127.0.0.1:#{port}", SPICEDB_TOKEN) do |client|
        expect do
          client.experimental_roaring_lookup_resources(resource_type: 'document', permission: 'non-canonical-ids',
                                                       subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                                       object_id: 'alice')),
                                                       consistency: SpiceDB::Consistency.full)
        end.to raise_error(SpiceDB::FailedPreconditionError, /canonical/)
      end
    ensure
      stop_stand_in_server(server, thread)
    end
  end
end

require_relative 'materialize_stand_ins'

RSpec.describe 'Materialize permission sets', :no_spicedb do
  it 'authenticates every service and returns native counters, cursor events and download manifests' do
    server = GRPC::RpcServer.new(pool_size: 4)
    port = server.add_http2_port('127.0.0.1:0', :this_port_is_insecure)
    server.handle(MaterializeSetStandIns::Counts.new)
    server.handle(MaterializeSetStandIns::Sets.new)
    thread = Thread.new { server.run }
    server.wait_till_running(5)
    SpiceDB::Client.new_plaintext("127.0.0.1:#{port}", 'materialize-token') do |client|
      count = client.experimental_count_relationships_by_filter(SpiceDB::Filter.new(resource_type: 'document'))
      expect(count.relationship_count).to eq(12)
      expect(count.revision).to eq(SpiceDB::ZedToken.new('count-rev'))
      event = client.experimental_watch_permission_sets.first
      expect(event.kind).to eq(:completed_revision)
      expect(event.revision).to eq(SpiceDB::ZedToken.new('checkpoint'))
      page = client.experimental_lookup_permission_sets(limit: 2).first
      expect(page.change.child_member.object.object_id).to eq('alice')
      expect(page.change.operation).to eq(:added)
      expect(page.cursor.to_h).to eq(limit: 2, token: SpiceDB::ZedToken.new('set-rev'), starting_index: 7,
                                     completed_members: false, starting_key: 'key', cursor: 'opaque')
      download = client.experimental_download_permission_sets
      expect(download.files.first.name).to eq('snapshot')
      expect(download.timestamp).to eq(Time.at(12, 34, :nsec))
      expect(download.revision).to eq(SpiceDB::ZedToken.new('download-rev'))
    end
  ensure
    server&.stop
    thread&.join(5)
  end
end
