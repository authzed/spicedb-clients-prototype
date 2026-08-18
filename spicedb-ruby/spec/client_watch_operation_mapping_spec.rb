# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

# Proves that an unrecognized watch operation -- :OPERATION_UNSPECIFIED, or a
# future operation value added after this client shipped -- maps to
# :unspecified, and never to a write.
#
# Root DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail",
# clause 2: server-supplied values the client does not recognise MUST NOT
# raise, and MUST map to the safe, non-permissive default -- never a grant, and
# never a write. A cache or index mirror consuming the watch stream that
# upserted on an operation it could not interpret would turn a delete it
# doesn't understand into a silent write.
#
# :unspecified is also the symbol this client already uses for an unrecognized
# permissionship, so the watch mapper no longer needs a name of its own
# (previously :unknown).
RSpec.describe 'SpiceDB::Client#updates operation mapping' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  def rel_proto(subject_id)
    Authzed::Api::V1::Relationship.new(
      resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'readme'),
      relation: 'viewer',
      subject: Authzed::Api::V1::SubjectReference.new(
        object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: subject_id)
      )
    )
  end

  # Stubs the underlying proto watch stream with one response per (operation,
  # subject_id) pair. `operation` is passed through to the proto message as-is,
  # so a test can hand it a value the enum has no name for.
  def stub_watch_updates(pairs)
    responses = pairs.map do |operation, subject_id|
      Authzed::Api::V1::WatchResponse.new(
        updates: [
          Authzed::Api::V1::RelationshipUpdate.new(
            operation: operation,
            relationship: rel_proto(subject_id)
          )
        ]
      )
    end

    watch_service = double('watch_service')
    allow(watch_service).to receive(:watch).and_return(responses)

    proto_client = double('proto_client', watch: watch_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  it 'maps :OPERATION_UNSPECIFIED to :unspecified, not :touch' do
    stub_watch_updates([[:OPERATION_UNSPECIFIED, 'alice']])

    got = client.updates(['document']).to_a

    expect(got.length).to eq(1)
    expect(got.first.operation).to eq(:unspecified)
    expect(got.first.operation).not_to eq(:touch)
    expect(got.first.relationship.subject_id).to eq('alice')
  end

  it 'maps an unknown future operation value to :unspecified, not :touch' do
    # A discriminant no version of this client knows about, standing in for an
    # operation added to the proto after this client shipped. protobuf-ruby
    # surfaces an unknown enum value as its raw Integer rather than a Symbol.
    stub_watch_updates([[9999, 'bob']])

    got = client.updates(['document']).to_a

    expect(got.length).to eq(1)
    expect(got.first.operation).to eq(:unspecified)
    expect(got.first.relationship.subject_id).to eq('bob')
  end

  it 'still maps the three recognized operations to themselves' do
    stub_watch_updates([
                         [:OPERATION_CREATE, 'carol'],
                         [:OPERATION_TOUCH, 'dave'],
                         [:OPERATION_DELETE, 'erin']
                       ])

    got = client.updates(['document']).to_a

    expect(got.map(&:operation)).to eq(%i[create touch delete])
    expect(got.map { |u| u.relationship.subject_id }).to eq(%w[carol dave erin])
  end
end
