# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

RSpec.describe 'SpiceDB::Client#check_permissions per-item error mapping' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:relationship) { SpiceDB::Relationship.from_triple('document', 'doc1', 'viewer', 'user', 'alice') }

  # Stubs @proto_client.permissions.check_bulk_permissions to return a
  # CheckBulkPermissionsResponse whose single pair carries a per-item
  # Google::Rpc::Status error (as opposed to a transport-level gRPC failure).
  def stub_bulk_check_item_error(code:, message:)
    error_status = Google::Rpc::Status.new(code: code, message: message)
    pair = Authzed::Api::V1::CheckBulkPermissionsPair.new(error: error_status)
    response = Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: [pair])

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:check_bulk_permissions).and_return(response)

    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  it 'raises SpiceDB::InvalidArgumentError (not the base SpiceDB::Error) for an INVALID_ARGUMENT per-item error' do
    stub_bulk_check_item_error(code: 3, message: 'invalid argument: bad caveat context')

    # raise_error(SomeClass, msg) matches via `SomeClass === raised_error`, so this
    # both pins the exact subclass (fails if the base SpiceDB::Error is raised
    # instead) and the message text.
    expect do
      client.check_permission(SpiceDB::Consistency.full, 'view', relationship)
    end.to raise_error(SpiceDB::InvalidArgumentError, 'invalid argument: bad caveat context')
  end

  it 'raises through check_permissions/check_any/check_all as well, since they all share call_bulk_check' do
    stub_bulk_check_item_error(code: 5, message: 'not found: no such resource')

    expect do
      client.check_any(SpiceDB::Consistency.full, 'view', relationship)
    end.to raise_error(SpiceDB::NotFoundError, 'not found: no such resource')
  end
end
