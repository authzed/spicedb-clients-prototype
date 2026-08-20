# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

# Read-path coverage for write-time caveat context.
#
# Two defects lived on one line here. `Struct#fields` returns a
# Google::Protobuf::Map, not a Hash, so calling `transform_values` on it raised
# NoMethodError -- and since relationship_from_proto backs read_relationships,
# export_relationships AND updates/watch, no deployment using caveats could read,
# export, or watch relationships at all. Had it run, `&:string_value` would have
# returned "" for every non-string value, silently destroying stored context.
#
# The proto here is built BY HAND rather than through relationship_to_proto. That is
# deliberate and still load-bearing: it isolates the read path from the write path, so
# these assertions describe what the read path does with a proto Struct regardless of
# how that Struct was produced. A regression in the write path cannot mask a regression
# in the read path here, and vice versa.
#
# The write path no longer stringifies -- SpiceDB::CaveatContext::caveat_context_to_struct
# now converts each value to its proper Google::Protobuf::Value kind, so a round trip
# through relationship_to_proto and back preserves Ruby types. That correction did not
# require touching this spec, which is the point of building the proto by hand.
RSpec.describe 'SpiceDB::Client caveat context read path' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  def value(**kwargs)
    Google::Protobuf::Value.new(**kwargs)
  end

  def caveated_proto(context_struct)
    Authzed::Api::V1::Relationship.new(
      resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'doc1'),
      relation: 'viewer',
      subject: Authzed::Api::V1::SubjectReference.new(
        object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'alice')
      ),
      optional_caveat: Authzed::Api::V1::ContextualizedCaveat.new(
        caveat_name: 'only_bizhours',
        context: context_struct
      )
    )
  end

  it 'reads every Value kind back with its Ruby type intact' do
    nested = Google::Protobuf::Struct.new(fields: { 'inner' => value(string_value: 'deep') })
    list = Google::Protobuf::ListValue.new(values: [value(number_value: 1), value(string_value: 'two')])

    struct = Google::Protobuf::Struct.new(
      fields: {
        'a_string' => value(string_value: 'hello'),
        'a_number' => value(number_value: 42),
        'a_bool' => value(bool_value: true),
        'a_null' => value(null_value: :NULL_VALUE),
        'a_struct' => value(struct_value: nested),
        'a_list' => value(list_value: list)
      }
    )

    rel = client.send(:relationship_from_proto, caveated_proto(struct))

    expect(rel.caveat_name).to eq('only_bizhours')
    expect(rel.caveat_context['a_string']).to eq('hello')
    expect(rel.caveat_context['a_number']).to eq(42.0)
    expect(rel.caveat_context['a_bool']).to be(true)
    expect(rel.caveat_context['a_null']).to be_nil
    expect(rel.caveat_context['a_struct']).to eq({ 'inner' => 'deep' })
    expect(rel.caveat_context['a_list']).to eq([1.0, 'two'])
  end

  it 'does not collapse non-string values to empty strings' do
    struct = Google::Protobuf::Struct.new(fields: { 'n' => value(number_value: 7) })

    rel = client.send(:relationship_from_proto, caveated_proto(struct))

    expect(rel.caveat_context['n']).not_to eq('')
    expect(rel.caveat_context['n']).to eq(7.0)
  end

  it 'reads a caveat with an empty context' do
    rel = client.send(:relationship_from_proto, caveated_proto(Google::Protobuf::Struct.new))

    expect(rel.caveat_name).to eq('only_bizhours')
    expect(rel.caveat_context).to eq({})
  end

  # The three examples above call the private converter directly. This one drives it
  # through the public API a user actually touches, so the crash is caught at the
  # surface where it was really reachable -- relationship_from_proto backs
  # read_relationships, export_relationships and updates alike.
  it 'surfaces typed caveat context through the public read_relationships API' do
    response = Authzed::Api::V1::ReadRelationshipsResponse.new(
      relationship: caveated_proto(
        Google::Protobuf::Struct.new(fields: { 'n' => value(number_value: 7) })
      ),
      after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor-1')
    )

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:read_relationships).and_return([response])
    client.instance_variable_set(
      :@proto_client, double('proto_client', permissions: permissions_service)
    )

    results = client.read_relationships(
      SpiceDB::Consistency.full,
      SpiceDB::Filter.new(resource_type: 'document')
    ).to_a

    expect(results.length).to eq(1)
    expect(results.first.caveat_context['n']).to eq(7.0)
  end
end
