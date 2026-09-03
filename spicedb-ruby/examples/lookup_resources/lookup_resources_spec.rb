# frozen_string_literal: true

require_relative '../spec_helper'
require 'spicedb_proto'
require 'grpc'

# Stand-in PermissionsService that records whether the most recent
# LookupResources request carried with_debug, so `debug:` can be proven to
# reach the wire without needing to construct a real maximum-recursion-depth
# failure against a live SpiceDB.
class DebugCapturingPermissionsService < Authzed::Api::V1::PermissionsService::Service
  attr_reader :got_with_debug

  def lookup_resources(request, _call)
    @got_with_debug = request.with_debug
    [
      Authzed::Api::V1::LookupResourcesResponse.new(
        resource_object_id: 'doc1',
        permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
        after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor-1')
      )
    ]
  end
end

RSpec.describe 'LookupResources' do
  it 'finds all resources a subject can access' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'seconddoc', 'editor', 'user', 'alice'))
    client.write(txn)

    # alice can view both documents (viewer implies view, editor implies view)
    results = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'view', 'user', 'alice'
    ).to_a

    resource_ids = results.map(&:resource_id)
    expect(resource_ids).to include('firstdoc')
    expect(resource_ids).to include('seconddoc')

    # Each result is a native SpiceDB::LookupResource, not a bare ID — callers
    # MUST check permissionship before treating a result as a full grant.
    expect(results).to all(be_a(SpiceDB::LookupResource))
    expect(results.map(&:permissionship)).to all(eq(:has_permission))
  end

  it 'returns only resources matching the requested permission' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'seconddoc', 'owner', 'user', 'alice'))
    client.write(txn)

    # Only seconddoc should appear for "delete" (owner implies delete)
    resource_ids = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'delete', 'user', 'alice'
    ).map(&:resource_id)

    expect(resource_ids).to include('seconddoc')
    expect(resource_ids).not_to include('firstdoc')
  end

  it 'returns empty when subject has no access' do
    client.write_schema(TEST_SCHEMA)

    results = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'view', 'user', 'nobody'
    ).to_a

    expect(results).to be_empty
  end

  it 'surfaces a conditional permissionship and missing caveat context for caveated grants' do
    client.write_schema(<<~SCHEMA)
      definition user {}

      caveat has_valid_ip(ip_address string) {
        ip_address == "127.0.0.1"
      }

      definition document {
        relation viewer: user with has_valid_ip
        permission view = viewer
      }
    SCHEMA

    txn = SpiceDB::Transaction.new
    txn.touch(
      SpiceDB::Relationship.from_triple('document', 'caveated', 'viewer', 'user', 'alice')
                            .with_caveat('has_valid_ip', {})
    )
    client.write(txn)

    result = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'view', 'user', 'alice'
    ).find { |r| r.resource_id == 'caveated' }

    # Without caveat context supplied, the server can't fully evaluate the
    # grant — permissionship MUST be checked before treating this as access.
    expect(result.permissionship).to eq(:conditional_permission)
    expect(result.partial_caveat.missing_required_context).to include('ip_address')
  ensure
    # Clean up the caveated relationship so the next example's around-hook
    # can restore TEST_SCHEMA (which drops the `has_valid_ip` caveat type)
    # without SpiceDB rejecting it for a dangling relationship reference.
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'))
  end

  it 'reaches the wire with debug: when a LookupResources failure needs extra recursion-depth context' do
    # `debug:` sets LookupResourcesRequest#with_debug, asking the server to
    # attach additional debug context to the error when a call fails by
    # exceeding the maximum permission-check recursion depth. That context
    # rides the same google.rpc.ErrorInfo detail this client already parses
    # onto SpiceDB::Error#reason/#reason_metadata -- there's no separate
    # accessor for it. Provoking a real depth-exceeded failure needs a deeply
    # recursive schema this example doesn't otherwise need, so this proves
    # with_debug reaches the wire request against a stand-in PermissionsService
    # instead.
    service = DebugCapturingPermissionsService.new
    server = GRPC::RpcServer.new(pool_size: 1)
    port = server.add_http2_port('localhost:0', :this_port_is_insecure)
    server.handle(service)
    server_thread = Thread.new { server.run }
    server.wait_till_running(5)

    debug_client = SpiceDB::Client.new_plaintext("localhost:#{port}", 'debug-token')

    debug_client.lookup_resources(SpiceDB::Consistency.min_latency, 'document', 'view', 'user', 'alice').to_a
    expect(service.got_with_debug).to be(false), 'with_debug should be false when debug: is not passed'

    debug_client.lookup_resources(SpiceDB::Consistency.min_latency, 'document', 'view', 'user', 'alice',
                                  debug: true).to_a
    expect(service.got_with_debug).to be(true), 'debug: true should have set with_debug on the wire request'
  ensure
    debug_client&.close
    server&.stop
    server_thread&.join(5)
  end
end
