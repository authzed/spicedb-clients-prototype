# frozen_string_literal: true

# Bulk-check chunking.
#
# SpiceDB rejects a CheckBulkPermissions request carrying more items than
# +maxBulkCheckCount+ -- 10,000, a hard-coded const in
# +internal/services/v1/bulkcheck.go+ with no flag to raise or lower it --
# with +ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST+. Nothing in the proto
# enforces this (+CheckBulkPermissionsRequest.items+ carries only a per-item
# +required+ rule, not a collection-size rule), so the client is what has to
# split large inputs.

require_relative '../lib/spicedb'
require 'spicedb_proto'

RSpec.describe 'SpiceDB::Client#check_permissions chunking' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  batch_size = SpiceDB::Client::DEFAULT_CHECK_BATCH_SIZE
  total = (batch_size * 2) + 7

  # Records the item count of every request and answers each one, echoing the
  # item's resource ID back through missing_required_context so a caller can
  # prove which request item each result came from -- and therefore that
  # concatenating chunk responses preserved input order.
  #
  # +short_at+, when given, makes the request at that index (0-based) return
  # one fewer pair than it was asked for.
  def stub_echo_server(short_at: nil, malformed_at_absolute: nil)
    sizes = []

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:check_bulk_permissions) do |request, **_kwargs|
      index = sizes.length
      base = sizes.sum
      sizes << request.items.length

      items = request.items.to_a
      items = items[0...-1] if short_at == index && !items.empty?

      pairs = items.each_with_index.map do |(item), i|
        # `response` oneof left unset entirely.
        next Authzed::Api::V1::CheckBulkPermissionsPair.new if malformed_at_absolute == base + i

        Authzed::Api::V1::CheckBulkPermissionsPair.new(
          item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(
            permissionship: :PERMISSIONSHIP_HAS_PERMISSION,
            # `msg['object_id']`, not `msg.object_id`: the generated reader is
            # shadowed by Kernel#object_id, so the dotted form silently yields
            # an Integer -- Ruby's object identity -- instead of the wire
            # value. The bracket accessor reads the protobuf field.
            partial_caveat_info: Authzed::Api::V1::PartialCaveatInfo.new(
              missing_required_context: [item.resource['object_id']]
            )
          )
        )
      end

      Authzed::Api::V1::CheckBulkPermissionsResponse.new(
        pairs: pairs,
        checked_at: Authzed::Api::V1::ZedToken.new(token: 'tok')
      )
    end

    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
    sizes
  end

  # n relationships whose resource IDs are their zero-padded index.
  def numbered_rels(count)
    (0...count).map do |i|
      SpiceDB::Relationship.from_triple('document', format('%05d', i), 'viewer', 'user', 'alice')
    end
  end

  it 'splits an oversized input into requests of at most DEFAULT_CHECK_BATCH_SIZE' do
    sizes = stub_echo_server

    results = client.check_permissions(SpiceDB::Consistency.full, 'view', numbered_rels(total))

    expect(results.length).to eq(total)
    expect(sizes).to eq([batch_size, batch_size, 7])
  end

  it 'keeps chunked results in input order' do
    # The echo carries each item's own resource ID, so a reordering -- or a
    # chunk's results landing under the wrong offset -- is visible on every
    # one of the 2,007 results, not just at the seams.
    stub_echo_server

    results = client.check_permissions(SpiceDB::Consistency.full, 'view', numbered_rels(total))

    expect(results.map { |r| r.missing_context.first }).to eq((0...total).map { |i| format('%05d', i) })
  end

  [1, 999, batch_size].each do |n|
    it "sends exactly one request for #{n} relationship(s)" do
      # The common case must not regress into a loop with per-chunk overhead.
      sizes = stub_echo_server

      results = client.check_permissions(SpiceDB::Consistency.full, 'view', numbered_rels(n))

      expect(results.length).to eq(n)
      expect(sizes).to eq([n])
    end
  end

  it 'sends no request at all for an empty input' do
    # Zero relationships costs zero round trips -- not one request carrying an
    # empty item list -- and returns [] rather than raising.
    sizes = stub_echo_server

    expect(client.check_permissions(SpiceDB::Consistency.full, 'view')).to eq([])
    expect(sizes).to eq([])
  end

  it 'check_all on an empty input is false and sends no request' do
    # Chunking must not resurrect the vacuous-true bug: an aggregate over zero
    # checks is false, and it costs no request.
    sizes = stub_echo_server

    expect(client.check_all(SpiceDB::Consistency.full, 'view')).to be(false)
    expect(sizes).to eq([])
  end

  it 'fires the pair-count guard on a later chunk, not just the first' do
    # The guard is evaluated per chunk, not once against the caller's total:
    # the first chunk answers in full, the second returns 999 pairs for 1,000
    # items. Without a per-chunk guard the shortfall would silently desync
    # every result from the second chunk onward.
    sizes = stub_echo_server(short_at: 1)

    expect do
      client.check_permissions(SpiceDB::Consistency.full, 'view', numbered_rels(total))
    end.to raise_error(SpiceDB::Error, /999 pair\(s\) for 1000 request item\(s\)/)

    # Two requests went out before the guard fired -- proof the failure was
    # detected on the second chunk, not on the whole input up front.
    expect(sizes).to eq([batch_size, batch_size])
  end

  it "reports the caller's absolute index in a per-item message, not the chunk-relative one" do
    # Chunking made every "check item N" message chunk-relative: a failure at
    # relationship 1003 read as "check item 3", so a caller who logs or parses
    # it acts on relationship 3 -- one resource's answer attributed to another,
    # the same failure family the pair-count guard exists to prevent, relocated
    # into the diagnostic.
    failing = batch_size + 3
    stub_echo_server(malformed_at_absolute: failing)

    expect do
      client.check_permissions(SpiceDB::Consistency.full, 'view', numbered_rels(batch_size * 2))
    end.to raise_error(SpiceDB::Error, /check item #{failing}: malformed/)
  end
end
