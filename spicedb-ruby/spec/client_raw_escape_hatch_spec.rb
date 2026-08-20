# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

# The escape hatch, SpiceDB::Client#proto_client, exists so a request the
# idiomatic surface cannot express has a workaround short of forking the gem --
# root DESIGN.md, "What NOT To Do", permits exactly this as "clearly marked
# secondary API". Asserting the reader returns something non-nil would prove
# none of that. What matters is whether a caller can drive a generated stub
# through it and get an answer out of a real server, with this client's bearer
# token attached.
#
# So these specs run a real GRPC::RpcServer and assert the `authorization`
# metadata the server actually received -- a `double`-based mock (as used
# elsewhere in this suite) could not, since the interceptor and credentials
# that attach the token live below the mock.
#
# The RPC driven here is CheckPermission, the single-check call the idiomatic
# client never makes (#check_permission routes every check through
# CheckBulkPermissions), so the gap is genuine rather than contrived.

# Records the authorization metadata of every call it serves, in arrival order.
class AuthRecordingPermissionsService < Authzed::Api::V1::PermissionsService::Service
  attr_reader :authorizations

  def initialize
    @authorizations = []
    super
  end

  def check_permission(_request, call)
    @authorizations << call.metadata['authorization']
    Authzed::Api::V1::CheckPermissionResponse.new(
      permissionship: :PERMISSIONSHIP_HAS_PERMISSION,
      checked_at: Authzed::Api::V1::ZedToken.new(token: 'rev-raw')
    )
  end

  def check_bulk_permissions(request, call)
    @authorizations << call.metadata['authorization']
    pairs = request.items.map do
      Authzed::Api::V1::CheckBulkPermissionsPair.new(
        item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(
          permissionship: :PERMISSIONSHIP_HAS_PERMISSION
        )
      )
    end
    Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: pairs)
  end
end

RSpec.describe 'SpiceDB::Client#proto_client escape hatch' do
  def start_server
    service = AuthRecordingPermissionsService.new
    server = GRPC::RpcServer.new(pool_size: 4, pool_keep_alive: 0.1)
    port = server.add_http2_port('localhost:0', :this_port_is_insecure)
    server.handle(service)
    Thread.new { server.run }
    server.wait_till_running(5)
    [server, port, service]
  end

  def check_permission_request
    Authzed::Api::V1::CheckPermissionRequest.new(
      consistency: Authzed::Api::V1::Consistency.new(fully_consistent: true),
      resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'readme'),
      permission: 'view',
      subject: Authzed::Api::V1::SubjectReference.new(
        object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'jimmy')
      )
    )
  end

  let(:rel) { SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'jimmy') }

  it 'drives a real generated stub against a real server, authenticated' do
    server, port, service = start_server
    begin
      client = SpiceDB::Client.new_plaintext("localhost:#{port}", 'test-token')
      response = client.proto_client.permissions.check_permission(check_permission_request)

      expect(response.permissionship).to eq(:PERMISSIONSHIP_HAS_PERMISSION)
      expect(response.checked_at.token).to eq('rev-raw')
      # The bearer token rides the stub this client built, so a raw call is
      # authenticated exactly as an idiomatic one is -- nothing extra to pass.
      expect(service.authorizations).to eq(['Bearer test-token'])
      client.close
    ensure
      server.stop
    end
  end

  it 'hands back the connection the idiomatic methods use, not a second one' do
    server, port, service = start_server
    begin
      client = SpiceDB::Client.new_plaintext("localhost:#{port}", 'test-token')
      expect(client.proto_client).to be(client.proto_client)

      expect(client.check_permission(SpiceDB::Consistency.full, 'view', rel).has_permission?).to be(true)
      client.proto_client.permissions.check_permission(check_permission_request)

      # One idiomatic call (via CheckBulkPermissions) and one raw call (via the
      # single-check RPC), both authenticated, both over this client's own
      # connection.
      expect(service.authorizations).to eq(['Bearer test-token', 'Bearer test-token'])
      client.close
    ensure
      server.stop
    end
  end

  # The hatch must never grow into a way to build a connection. Root DESIGN.md,
  # "RULE: Credentials over insecure transport require an explicit opt-in", is
  # enforced in the constructor, on the single path that builds a channel.
  # Handing back an already-built proto client cannot bypass that; accepting an
  # endpoint, token, or transport setting would.
  it 'is an accessor, not a second construction path' do
    expect(SpiceDB::Client.instance_method(:proto_client).arity).to eq(0)

    # And the guard still refuses what it always did.
    expect do
      SpiceDB::Client.new_plaintext('evil.example.com:50051', 'test-token')
    end.to raise_error(SpiceDB::InvalidArgumentError, /allow_insecure_remote_credentials/)
  end
end
