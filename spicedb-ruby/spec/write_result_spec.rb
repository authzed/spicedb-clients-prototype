# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

RSpec.describe SpiceDB::WriteResult do
  [nil, '', " opaque\x00☃ "].each do |token|
    it "preserves revision #{token.inspect} and the write request" do
      client = SpiceDB::Client.new_plaintext('localhost:50051', 'key')
      response = Authzed::Api::V1::WriteRelationshipsResponse.new
      response.written_at = Authzed::Api::V1::ZedToken.new(token: token) unless token.nil?
      allow(client.proto_client.permissions).to receive(:write_relationships) do |request, **_kwargs|
        expect(request.optional_transaction_metadata.fields['request'].string_value).to eq('one')
        response
      end
      result = client.write(SpiceDB::Transaction.new, metadata: { 'request' => 'one' })
      expect(result).to be_a(described_class)
      expect(result).to be_frozen
      if token.nil?
        expect(result.revision).to be_nil
      else
        expect(result.revision).to be_a(SpiceDB::ZedToken)
        expect(result.revision.to_s).to eq(token)
        expect(SpiceDB::Consistency.at_least(result.revision).to_proto).to eq(at_least_as_fresh: { token: token })
      end
      expect { SpiceDB::Consistency.at_least(result) }.to raise_error(SpiceDB::InvalidArgumentError)
    ensure
      client&.close
    end
  end

  it 'rejects raw revision strings' do
    expect { described_class.new(revision: 'raw') }.to raise_error(SpiceDB::InvalidArgumentError)
  end
end
