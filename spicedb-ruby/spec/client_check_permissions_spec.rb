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

RSpec.describe 'SpiceDB::Client#check_permissions response/request length guard' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:relationship1) { SpiceDB::Relationship.from_triple('document', 'doc1', 'viewer', 'user', 'alice') }
  let(:relationship2) { SpiceDB::Relationship.from_triple('document', 'doc2', 'viewer', 'user', 'bob') }

  def stub_bulk_check_response(pairs:)
    response = Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: pairs)

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:check_bulk_permissions).and_return(response)

    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  # The proto guarantees pairs are returned in request order but says nothing
  # about count. A short response would otherwise silently desync results[i]
  # from relationships[i] for every item after the gap -- one resource's
  # answer attributed to another.
  it 'raises SpiceDB::Error when the response has fewer pairs than request items' do
    item = Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(
      permissionship: :PERMISSIONSHIP_HAS_PERMISSION
    )
    stub_bulk_check_response(pairs: [Authzed::Api::V1::CheckBulkPermissionsPair.new(item: item)])

    expect do
      client.check_permissions(SpiceDB::Consistency.full, 'view', relationship1, relationship2)
    end.to raise_error(SpiceDB::Error, /1 pair.*2 request item/)
  end

  # A CheckBulkPermissionsPair whose `response` oneof is unset (neither
  # `item` nor `error`) must fail loudly, not silently dereference a nil
  # `item`. The proto schema guarantees a well-behaved server never sends
  # this, but nothing on the wire prevents it.
  it 'raises SpiceDB::Error on a malformed pair instead of crashing on a nil item' do
    stub_bulk_check_response(pairs: [Authzed::Api::V1::CheckBulkPermissionsPair.new])

    expect do
      client.check_permission(SpiceDB::Consistency.full, 'view', relationship1)
    end.to raise_error(SpiceDB::Error, /check item 0/)
  end
end

RSpec.describe 'SpiceDB::Client#check_permissions error pair with an empty message' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:relationship) { SpiceDB::Relationship.from_triple('document', 'doc1', 'viewer', 'user', 'alice') }

  # google.rpc.Status requires a code, never a message, so a server is entitled
  # to send an error pair whose message is empty. The per-item guard used to
  # dispatch on `!pair.error.message.empty?`, so such a pair fell past it, then
  # past the malformed-oneof guard (the oneof IS set to :error), and
  # dereferenced `pair.item.permissionship` on nil -- a NoMethodError raised
  # from inside the client rather than the typed error the caller can rescue.
  it 'raises the typed error, not NoMethodError, when the per-item status carries no message' do
    error_status = Google::Rpc::Status.new(code: 7, message: '')
    pair = Authzed::Api::V1::CheckBulkPermissionsPair.new(error: error_status)
    response = Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: [pair])

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:check_bulk_permissions).and_return(response)
    client.instance_variable_set(:@proto_client, double('proto_client', permissions: permissions_service))

    expect do
      client.check_permissions(SpiceDB::Consistency.full, 'view', relationship)
    end.to raise_error(SpiceDB::PermissionDeniedError)
  end
end
