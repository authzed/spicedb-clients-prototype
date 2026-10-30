# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

RSpec.describe 'native token and option contracts' do
  it 'snapshots opaque tokens losslessly and compares native values' do
    source = +"  opaque\x00token  "
    token = SpiceDB::ZedToken.new(source)
    source.clear
    expect(token.to_s).to eq("  opaque\x00token  ")
    expect(token).to eq(SpiceDB::ZedToken.new(token.to_s))
    expect(token).to be_frozen
    expect(token.to_s).to be_frozen
    expect(SpiceDB::ZedToken.new(' opaque ').to_s).to eq(' opaque ')
    expect { SpiceDB::ZedToken.new(nil) }.to raise_error(SpiceDB::InvalidArgumentError)
  end

  # Root DESIGN.md, "Reject unusable credentials and trust material at
  # construction": a caller-supplied revision token that is empty or
  # whitespace-only (usually an unset variable) is a local validation error.
  ['', ' ', "\t\n", "  \r\n  "].each do |blank|
    it "rejects the caller-supplied token #{blank.inspect} like an empty one" do
      expect { SpiceDB::ZedToken.new(blank) }
        .to raise_error(SpiceDB::LocalValidationError, 'ZedToken: must not be empty or whitespace')
    end
  end

  it 'requires native consistency tokens and keeps an empty token the server sent' do
    expect { SpiceDB::Consistency.at_least('raw') }.to raise_error(SpiceDB::InvalidArgumentError)
    token = SpiceDB::ZedToken.from_proto(Authzed::Api::V1::ZedToken.new(token: ''))
    expect(token.to_s).to eq('')
    expect(token).to be_frozen
    expect(SpiceDB::Consistency.at_least_or_full(token).revision).to eq(token)
    expect(SpiceDB::Consistency.snapshot(token).to_proto).to eq(at_exact_snapshot: { token: '' })
  end

  it 'lists named cases and rejects boolean or unknown behavioral inputs' do
    expect(SpiceDB::Tracing.values).to contain_exactly(SpiceDB::Tracing::DISABLED, SpiceDB::Tracing::ENABLED)
    expect { SpiceDB::Tracing.validate(true) }.to raise_error(SpiceDB::InvalidArgumentError)
    expect { SpiceDB::Client.new(endpoint: 'localhost:50051', token: 'key', transport: :tls) }.to raise_error(SpiceDB::InvalidArgumentError)
  end

  it 'permits explicit remote plaintext through the normal factory' do
    expect { SpiceDB::Client.new_plaintext('remote.example:50051', 'key') }.to raise_error(SpiceDB::InvalidArgumentError)
    SpiceDB::Client.new_plaintext('remote.example:50051', 'key', remote_credentials: SpiceDB::RemoteCredentials::ALLOW_PLAINTEXT, &:close)
    expect { SpiceDB::Client.new_plaintext('remote.example:50051', 'key', remote_credentials: true) }.to raise_error(SpiceDB::InvalidArgumentError)
  end

  it 'validates token fields on native result construction' do
    expect { SpiceDB::SchemaResult.new(schema: '', revision: 'raw') }.to raise_error(SpiceDB::InvalidArgumentError)
    expect { SpiceDB::SchemaResult.new(schema: '', revision: false) }.to raise_error(SpiceDB::InvalidArgumentError)
    expect(SpiceDB::SchemaResult.new(schema: '', revision: nil).revision).to be_nil
  end

  it 'validates tracing even for an empty bulk request' do
    SpiceDB::Client.new_plaintext('localhost:50051', 'key') do |client|
      expect(client.proto_client.permissions).not_to receive(:check_bulk_permissions)
      expect { client.check_bulk([], consistency: SpiceDB::Consistency.full, tracing: false) }.to raise_error(SpiceDB::InvalidArgumentError)
    end
  end

  it 'maps exact watch content and native resumption tokens' do
    SpiceDB::Client.new_plaintext('localhost:50051', 'key') do |client|
      request = client.send(:watch_request, [], SpiceDB::ZedToken.from_proto(Authzed::Api::V1::ZedToken.new(token: '')),
                            [SpiceDB::WatchContent::SCHEMA])
      expect(request.optional_update_kinds).to eq([:WATCH_KIND_INCLUDE_SCHEMA_UPDATES])
      expect(request.optional_start_cursor.token).to eq('')
      expect { client.watch(content: [true]) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.watch(content: []) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.watch(start_revision: 'raw') }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.watch(start_revision: false) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.experimental_download_permission_sets(at_revision: 'raw') }.to raise_error(SpiceDB::InvalidArgumentError)
    end
  end

  it 'preserves absent and opaque revisions through schema and materialize responses' do
    SpiceDB::Client.new_plaintext('localhost:50051', 'key') do |client|
      token = Authzed::Api::V1::ZedToken.new(token: ' opaque ')
      allow(client.proto_client.schema).to receive(:read_schema).and_return(
        Authzed::Api::V1::ReadSchemaResponse.new(schema_text: 'schema', read_at: token)
      )
      expect(client.read_schema.revision).to eq(SpiceDB::ZedToken.new(' opaque '))
      allow(client.proto_client.schema).to receive(:write_schema).and_return(Authzed::Api::V1::WriteSchemaResponse.new(written_at: token))
      expect(client.write_schema('schema')).to eq(SpiceDB::ZedToken.new(' opaque '))
      allow(client.proto_client.permission_sets).to receive(:download_permission_sets) do |request, **|
        expect(request.optional_at_revision.token).to eq(' opaque ')
        Authzed::Api::Materialize::V0::DownloadPermissionSetsResponse.new(at_revision: token)
      end
      expect(client.experimental_download_permission_sets(at_revision: SpiceDB::ZedToken.new(' opaque ')).revision).to eq(SpiceDB::ZedToken.new(' opaque '))
      event = client.send(:watch_event_from_proto, Authzed::Api::V1::WatchResponse.new)
      expect(event.changes_through).to be_nil
    end
  end
end
