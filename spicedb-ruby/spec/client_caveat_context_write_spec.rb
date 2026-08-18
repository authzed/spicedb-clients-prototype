# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

# Write-path coverage for write-time caveat context (Relationship#caveat_context,
# stored via Relationship.optional_caveat.context).
#
# relationship_to_proto used to convert every caveat context value with
# `Google::Protobuf::Value.new(string_value: v.to_s)` regardless of its Ruby
# type -- a number, a boolean, nil, a nested Hash, or a nested Array were all
# flattened to a string. A caveat like `now < 100` stored against the string
# "50" fails to evaluate, and fails *silently*, as a CONDITIONAL_PERMISSION
# result rather than an error. Worse than the equivalent check-time gap: a bad
# check-time context fails one call, but a bad write-time context is
# *persisted* -- every future check against that relationship mis-evaluates,
# and re-checking with correct context never repairs it, only rewriting the
# relationship does.
#
# relationship_to_proto now dispatches through SpiceDB::CaveatContext's
# #caveat_context_to_struct, the same value-level converter
# (#check_context_value) the check surface already used correctly.
RSpec.describe 'SpiceDB::Client caveat context write path' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  def relationship_with_context(context)
    SpiceDB::Relationship.from_triple('document', 'doc1', 'viewer', 'user', 'alice')
                         .with_caveat('only_bizhours', context)
  end

  it 'dispatches every Value kind by Ruby type instead of stringifying' do
    context = {
      'a_string' => 'hello',
      'an_int' => 42,
      'a_float' => 3.5,
      'a_bool' => true,
      'a_null' => nil,
      'a_map' => { 'nested' => 'value' },
      'a_list' => ['one', 2, false]
    }

    proto = client.send(:relationship_to_proto, relationship_with_context(context))
    fields = proto.optional_caveat.context.fields

    expect(fields['a_string'].kind).to eq(:string_value)
    expect(fields['a_string'].string_value).to eq('hello')

    # google.protobuf.Value.number_value is a double, so an integer
    # legitimately round-trips as a float (42 -> 42.0). That is inherent to
    # the proto, not a defect in this conversion.
    expect(fields['an_int'].kind).to eq(:number_value)
    expect(fields['an_int'].number_value).to eq(42.0)

    expect(fields['a_float'].kind).to eq(:number_value)
    expect(fields['a_float'].number_value).to eq(3.5)

    expect(fields['a_bool'].kind).to eq(:bool_value)
    expect(fields['a_bool'].bool_value).to be(true)

    expect(fields['a_null'].kind).to eq(:null_value)

    expect(fields['a_map'].kind).to eq(:struct_value)
    expect(fields['a_map'].struct_value.fields['nested'].string_value).to eq('value')

    expect(fields['a_list'].kind).to eq(:list_value)
    list_values = fields['a_list'].list_value.values
    expect(list_values.length).to eq(3)
    expect(list_values[0].kind).to eq(:string_value)
    expect(list_values[1].kind).to eq(:number_value)
    expect(list_values[2].kind).to eq(:bool_value)
  end

  it 'does not collapse a numeric value to a string' do
    proto = client.send(:relationship_to_proto, relationship_with_context({ 'n' => 7 }))

    expect(proto.optional_caveat.context.fields['n'].kind).not_to eq(:string_value)
    expect(proto.optional_caveat.context.fields['n'].number_value).to eq(7.0)
  end

  it 'round-trips every type through relationship_to_proto and relationship_from_proto' do
    context = {
      'a_string' => 'hello',
      'an_int' => 42,
      'a_float' => 3.5,
      'a_bool' => true,
      'a_null' => nil,
      'a_map' => { 'nested' => 'value' },
      'a_list' => ['one', 2, false]
    }

    original = relationship_with_context(context)
    proto = client.send(:relationship_to_proto, original)
    restored = client.send(:relationship_from_proto, proto)

    expect(restored.caveat_context['a_string']).to eq('hello')
    expect(restored.caveat_context['an_int']).to eq(42.0)
    expect(restored.caveat_context['a_float']).to eq(3.5)
    expect(restored.caveat_context['a_bool']).to be(true)
    expect(restored.caveat_context).to have_key('a_null')
    expect(restored.caveat_context['a_null']).to be_nil
    expect(restored.caveat_context['a_map']).to eq({ 'nested' => 'value' })
    expect(restored.caveat_context['a_list']).to eq(['one', 2.0, false])
  end

  it 'raises SpiceDB::InvalidArgumentError naming the offending key for an unrepresentable value' do
    context = { 'good' => 'fine', 'bad' => Object.new }

    expect do
      client.send(:relationship_to_proto, relationship_with_context(context))
    end.to raise_error(SpiceDB::InvalidArgumentError, /"bad"/)
  end

  it 'sets no context field on the wire when the caveat has no context' do
    rel = SpiceDB::Relationship.from_triple('document', 'doc1', 'viewer', 'user', 'alice')
                               .with_caveat('always_true')

    proto = client.send(:relationship_to_proto, rel)

    expect(proto.optional_caveat.caveat_name).to eq('always_true')
    expect(proto.optional_caveat.context).to be_nil
  end
end
