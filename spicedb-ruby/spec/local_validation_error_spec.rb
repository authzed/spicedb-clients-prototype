# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

# Root DESIGN.md, "Expose native errors without losing server diagnostics":
# local validation errors are also the language's standard invalid-input
# exception (ArgumentError); server invalid-argument errors are not.
RSpec.describe SpiceDB::LocalValidationError do
  # rubocop:disable Style/CaseEquality
  it 'is caught by rescue ArgumentError around a malformed tuple, and is still INVALID_ARGUMENT' do
    error = begin
      SpiceDB::Relationship.from_tuple('not a tuple')
    rescue ArgumentError => e
      e
    end
    expect(error).to be_a(described_class)
    expect(error).to be_a(SpiceDB::InvalidArgumentError)
    expect(error.code).to eq(GRPC::Core::StatusCodes::INVALID_ARGUMENT)
    expect(GRPC::InvalidArgument === error).to be(true)
  end

  it 'is caught by rescue ArgumentError for an empty object ID and an undialable endpoint' do
    expect { SpiceDB::ObjectRef.new(object_type: 'document', object_id: '') }.to raise_error(ArgumentError)
    expect { SpiceDB::Client.new_plaintext('ftp://host:1', 't') }.to raise_error(ArgumentError)
    expect { SpiceDB::Client.new_plaintext('spicedb.example.com:50051', 't') }.to raise_error(ArgumentError)
  end

  # Every endpoint and constructor refusal is a LocalValidationError, so
  # rescue ArgumentError catches it and nothing reaches grpc unchecked.
  {
    'a plaintext endpoint without a port' => -> { SpiceDB::Client.new_plaintext('localhost', 't') },
    'a nil endpoint' => -> { SpiceDB::Client.new_plaintext(nil, 't') },
    'a nil token' => -> { SpiceDB::Client.new_plaintext('localhost:50111', nil) },
    'an Integer token' => -> { SpiceDB::Client.new_plaintext('localhost:50111', 5) },
    'a non-String CA certificate' => -> { SpiceDB::Client.new_custom_tls('localhost:50111', 't', ca_cert: 5) },
    'a non-String client key' => -> { SpiceDB::Client.new_custom_tls('localhost:50111', 't', client_cert: 'x', client_key: :pem) },
    'an unknown transport' => -> { SpiceDB::Client.new(endpoint: 'localhost:50111', token: 't', transport: :x) },
    'an unknown option' => -> { SpiceDB::Client.new_plaintext('localhost:50111', 't', bogus: 1) },
    'a non-channel for from_channel' => -> { SpiceDB::Client.from_channel(5) }
  }.each do |name, build|
    it "raises LocalValidationError at construction for #{name}" do
      expect { build.call }.to raise_error(described_class)
      expect { build.call }.to raise_error(ArgumentError)
    end
  end

  it 'leaves a server INVALID_ARGUMENT out of rescue ArgumentError' do
    server = SpiceDB.to_spicedb_error(GRPC::InvalidArgument.new('bad filter'))
    built = SpiceDB::Error.from_code(GRPC::Core::StatusCodes::INVALID_ARGUMENT, 'bad filter')
    [server, built].each do |error|
      expect(error).to be_a(SpiceDB::InvalidArgumentError)
      expect(error).not_to be_a(described_class)
      expect(ArgumentError === error).to be(false)
    end
  end

  it 'extends only ArgumentError itself, not its subclasses' do
    local = described_class.new('x')
    expect(ArgumentError === local).to be(true)
    expect(KeyError === local).to be(false)
    expect(SpiceDBProto::InvalidEndpointError === local).to be(false)
    expect(ArgumentError === ArgumentError.new('plain')).to be(true)
    expect(ArgumentError === StandardError.new('other')).to be(false)
  end
  # rubocop:enable Style/CaseEquality
end
