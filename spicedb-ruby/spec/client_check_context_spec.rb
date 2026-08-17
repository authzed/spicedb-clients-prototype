# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
# Struct#to_h / Struct.from_hash are google-protobuf's well-known-type
# convenience methods -- not auto-required by 'google/protobuf' itself.
# The client implementation deliberately does NOT depend on them (it builds
# Struct/Value directly, so it isn't sensitive to this require living
# somewhere else in the dependency graph); this spec pulls it in purely so
# assertions can read `item.context.to_h` instead of walking the raw
# fields/Value#kind oneof by hand.
require 'google/protobuf/well_known_types'

# Caveat context on the check surface (spec D3b).
#
# CheckBulkPermissionsRequestItem#context is the ONLY per-check context field
# on the wire (proto field 4) -- CheckBulkPermissionsRequest itself has none
# -- so a call-level context: must be fanned out onto every item at
# request-build time, and a per-item override (Relationship#check_context)
# must be merged onto that item's context, not swap it out wholesale.
#
# `check_context` is check-time-only and is a DIFFERENT concept from
# Relationship#caveat_context (written into the relationship at write time,
# embedded in optional_caveat on the wire). Conflating the two would leak
# check-time context into a write, silently altering stored relationships --
# see relationship_spec.rb for caveat_context's own coverage.
RSpec.describe 'SpiceDB::Client caveat context on checks' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  # Stubs @proto_client.permissions.check_bulk_permissions to return a
  # response built from pairs_config (mirrors client_check_result_spec.rb's
  # helper) while capturing every CheckBulkPermissionsRequest actually sent,
  # so specs can assert on the built request by value rather than only on
  # the (context-free) CheckResult it maps to.
  def stub_bulk_check_capturing(captured_requests, pairs_config: [{ permissionship: :PERMISSIONSHIP_HAS_PERMISSION }])
    pairs = pairs_config.map do |cfg|
      Authzed::Api::V1::CheckBulkPermissionsPair.new(
        item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(permissionship: cfg[:permissionship])
      )
    end
    response = Authzed::Api::V1::CheckBulkPermissionsResponse.new(
      pairs: pairs,
      checked_at: Authzed::Api::V1::ZedToken.new(token: 'zed-token-1')
    )

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:check_bulk_permissions) do |request|
      captured_requests << request
      response
    end

    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  def rel(id, subject = 'alice')
    SpiceDB::Relationship.from_triple('document', id, 'viewer', 'user', subject)
  end

  # C1 — call-level context alone reaches every item in a bulk request.
  it 'C1: fans call-level context onto every item in a bulk request' do
    captured = []
    stub_bulk_check_capturing(
      captured,
      pairs_config: Array.new(3) { { permissionship: :PERMISSIONSHIP_HAS_PERMISSION } }
    )

    rels = [rel('doc1'), rel('doc2'), rel('doc3')]
    client.check_permissions(SpiceDB::Consistency.full, 'view', *rels, context: { now: 42 })

    request = captured.first
    expect(request.items.length).to eq(3)
    request.items.each do |item|
      expect(item.context.to_h).to eq({ 'now' => 42 })
    end
  end

  # C2 — per-item context alone reaches only that item.
  it 'C2: per-item context (Relationship#check_context) reaches only that item' do
    captured = []
    stub_bulk_check_capturing(
      captured,
      pairs_config: Array.new(2) { { permissionship: :PERMISSIONSHIP_HAS_PERMISSION } }
    )

    with_context = rel('doc1').with_check_context({ now: 42 })
    without_context = rel('doc2', 'bob')

    client.check_permissions(SpiceDB::Consistency.full, 'view', with_context, without_context)

    request = captured.first
    expect(request.items[0].context.to_h).to eq({ 'now' => 42 })
    expect(request.items[1].context).to be_nil
  end

  # C3 — the merge rule: key-level, item wins. Call-level keys the item
  # doesn't mention are RETAINED, not dropped wholesale. Asserting only the
  # overriding item would also pass under wholesale-replacement semantics,
  # so this pins BOTH the overriding item and its context-free sibling.
  it 'C3: merges call-level and per-item context key-by-key, with the item winning on conflicts' do
    captured = []
    stub_bulk_check_capturing(
      captured,
      pairs_config: Array.new(2) { { permissionship: :PERMISSIONSHIP_HAS_PERMISSION } }
    )

    overriding = rel('doc1').with_check_context({ region: 'eu' })
    sibling = rel('doc2', 'bob') # supplies no per-item context at all

    client.check_permissions(
      SpiceDB::Consistency.full, 'view', overriding, sibling,
      context: { now: 42, region: 'us' }
    )

    request = captured.first
    overriding_item, sibling_item = request.items

    # The overriding item: its own 'region' wins, but 'now' (a call-level
    # key it never mentioned) is still present -- proving this is a merge,
    # not a wholesale replacement of the call-level context.
    expect(overriding_item.context.to_h).to eq({ 'now' => 42, 'region' => 'eu' })
    # The sibling supplied no per-item context, so it inherits the
    # call-level context completely unchanged.
    expect(sibling_item.context.to_h).to eq({ 'now' => 42, 'region' => 'us' })
  end

  # C4 — neither call-level nor per-item context supplied: no context field
  # is set on the wire (nil, not an empty Struct).
  it 'C4: sets no context field on the wire when neither call-level nor per-item context is supplied' do
    captured = []
    stub_bulk_check_capturing(captured)

    client.check_permissions(SpiceDB::Consistency.full, 'view', rel('doc1'))

    request = captured.first
    expect(request.items.first.context).to be_nil
  end

  # check_any/check_all are aggregates over the same BulkCheckPermissions
  # request and must evaluate caveats too -- confirm they thread a
  # call-level context: keyword through identically to check_permissions.
  it 'threads call-level context through check_permission, check_any, and check_all identically' do
    captured = []
    stub_bulk_check_capturing(captured)

    client.check_permission(SpiceDB::Consistency.full, 'view', rel('doc1'), context: { now: 1 })
    client.check_any(SpiceDB::Consistency.full, 'view', rel('doc1'), context: { now: 2 })
    client.check_all(SpiceDB::Consistency.full, 'view', rel('doc1'), context: { now: 3 })

    expect(captured.map { |r| r.items.first.context.to_h }).to eq(
      [{ 'now' => 1 }, { 'now' => 2 }, { 'now' => 3 }]
    )
  end

  # Existing call sites (no context: kwarg at all) must be entirely
  # unaffected -- this is the BINDING INVARIANT: adding caveat context must
  # not change any existing call site's behavior.
  it 'does not require context: at all -- existing no-context call sites are unaffected' do
    captured = []
    stub_bulk_check_capturing(captured)

    result = client.check_permission(SpiceDB::Consistency.full, 'view', rel('doc1'))

    expect(result).to be_a(SpiceDB::CheckResult)
    expect(captured.first.items.first.context).to be_nil
  end
end
