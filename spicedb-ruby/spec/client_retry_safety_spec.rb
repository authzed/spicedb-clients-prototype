# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

# Retry safety, per root DESIGN.md "RULE: Automatic retry is for idempotent
# operations only":
#
# - Reads (e.g. #read_schema/ReadSchema) retry on a transient error.
# - Mutations (e.g. #write/WriteRelationships) are attempted exactly once,
#   even on a retryable error -- a WriteRelationships carrying
#   OPERATION_CREATE or preconditions is not idempotent, and retrying a lost
#   response would surface ALREADY_EXISTS/FAILED_PRECONDITION for a write
#   that in fact succeeded.
# - RESOURCE_EXHAUSTED is never retried, on either a read or a mutation.
#
# See spec/errors_spec.rb for the inverted TRANSIENT_CODES/.transient?
# coverage of the RESOURCE_EXHAUSTED half of this guarantee.
RSpec.describe 'SpiceDB::Client retry safety' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:rel) { SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'jimmy') }

  # Stubs @proto_client.schema.read_schema to raise `errors.shift` on each
  # call until the queue is empty, then return a successful response.
  def stub_read_schema(errors)
    schema_service = double('schema_service')
    calls = []
    allow(schema_service).to receive(:read_schema) do
      calls << :call
      err = errors.shift
      raise err if err

      Authzed::Api::V1::ReadSchemaResponse.new(
        schema_text: 'definition user {}',
        read_at: Authzed::Api::V1::ZedToken.new(token: 'rev1')
      )
    end

    proto_client = double('proto_client', schema: schema_service)
    client.instance_variable_set(:@proto_client, proto_client)
    calls
  end

  # Stubs @proto_client.permissions.write_relationships to raise `error` on
  # every call (there should only ever be one).
  def stub_write_relationships_always_fails(error)
    permissions_service = double('permissions_service')
    calls = []
    allow(permissions_service).to receive(:write_relationships) do
      calls << :call
      raise error
    end

    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
    calls
  end

  def transaction_with_create(rel)
    txn = SpiceDB::Transaction.new
    txn.create(rel)
    txn
  end

  describe 'reads' do
    it 'retries a transient error and eventually succeeds' do
      calls = stub_read_schema([GRPC::Unavailable.new('down')])

      schema_text, revision = client.read_schema

      expect(schema_text).to eq('definition user {}')
      expect(revision).to eq('rev1')
      expect(calls.length).to eq(2), 'expected exactly one retry (initial attempt + 1 retry)'
    end

    it 'never retries RESOURCE_EXHAUSTED' do
      calls = stub_read_schema([GRPC::ResourceExhausted.new('quota')])

      expect { client.read_schema }.to raise_error(SpiceDB::ResourceExhaustedError)
      expect(calls.length).to eq(1)
    end
  end

  describe 'mutations' do
    it 'attempts #write exactly once on a retryable (transient) error' do
      calls = stub_write_relationships_always_fails(GRPC::Unavailable.new('down'))

      expect { client.write(transaction_with_create(rel)) }.to raise_error(SpiceDB::UnavailableError)
      expect(calls.length).to eq(1), 'a mutation must be attempted exactly once, even on a retryable error'
    end

    it 'never retries RESOURCE_EXHAUSTED' do
      calls = stub_write_relationships_always_fails(GRPC::ResourceExhausted.new('quota'))

      expect { client.write(transaction_with_create(rel)) }.to raise_error(SpiceDB::ResourceExhaustedError)
      expect(calls.length).to eq(1)
    end
  end

  describe 'backoff jitter' do
    it 'varies between calls instead of following a fixed schedule' do
      seen = Array.new(50) { client.send(:backoff_delay, 3) }

      expect(seen.uniq.length).to be > 1
      cap = SpiceDB::Client::BASE_RETRY_DELAY * (2**2)
      expect(seen).to all(be_between(0, cap))
    end
  end
end
