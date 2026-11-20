# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'

# Root DESIGN.md, "Batch resource lookups on the wire without changing what
# callers iterate": lookup_resources asks for batched responses by default,
# reads either wire form, counts results rather than messages, and puts a
# batch's one cursor on the last item yielded from it.
module LookupBatchFixtures
  def client = @client ||= SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken')

  def arguments
    { resource_type: 'document', permission: 'view',
      subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')),
      consistency: SpiceDB::Consistency.full }
  end

  def granted = :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
  def conditional = :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION

  def result(id, permissionship = granted, **)
    Authzed::Api::V1::LookupResourcesResult.new(resource_object_id: id, permissionship: permissionship, **)
  end

  def batch(results, after, looked_up_at: 'rev1')
    Authzed::Api::V1::LookupResourcesResponse.new(
      looked_up_at: Authzed::Api::V1::ZedToken.new(token: looked_up_at),
      batched: Authzed::Api::V1::LookupResourcesBatch.new(results: results, after_cursor: after && Authzed::Api::V1::Cursor.new(token: after))
    )
  end

  def single(id, after, permissionship = granted)
    Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: id, permissionship: permissionship,
                                                  after_result_cursor: Authzed::Api::V1::Cursor.new(token: after))
  end

  # Serves one fixed list of responses per request, recording each request's
  # [cursor, limit, batch size].
  def serve(*pages)
    requests = []
    permissions = double('permissions')
    allow(permissions).to receive(:lookup_resources) do |request, **|
      requests << [request.optional_cursor&.token, request.optional_limit, request.optional_response_batch_size]
      FixtureOperation.new(pages.fetch(requests.length - 1))
    end
    client.instance_variable_set(:@proto_client, double('proto', permissions: permissions))
    requests
  end

  # Serves +permissionships+ (one result each, IDs and cursors counting from
  # 0 and 1) as SpiceDB does: a page of the request's limit from its cursor,
  # cut into batches of the request's batch size, or one result per message
  # when the request asks for none (or +batching+ is false, a server that
  # predates batching).
  def serve_results(permissionships, batching: true)
    requests = []
    permissions = double('permissions')
    allow(permissions).to receive(:lookup_resources) do |request, **|
      start = request.optional_cursor&.token.to_i
      size = request.optional_response_batch_size
      requests << [start, request.optional_limit, size]
      page = permissionships.each_with_index.to_a.drop(start)
      page = page.first(request.optional_limit) unless request.optional_limit.zero?
      FixtureOperation.new(
        if batching && size.positive?
          page.each_slice(size).map { |slice| batch(slice.map { |value, index| result(index.to_s, value) }, (slice.last.last + 1).to_s) }
        else
          page.map { |value, index| single(index.to_s, (index + 1).to_s, value) }
        end
      )
    end
    client.instance_variable_set(:@proto_client, double('proto', permissions: permissions))
    requests
  end
end

RSpec.describe 'LookupResources response batching: the requested batch size' do
  include LookupBatchFixtures

  def requested(**)
    requests = serve([])
    client.lookup_resources(**arguments, **).to_a
    requests.map(&:last)
  end

  it 'defaults to the page size, capped at 1000' do
    expect(requested).to eq([1000])
    expect(requested(page_size: 50)).to eq([50])
    expect(requested(page_size: 5000)).to eq([1000])
  end

  it 'defaults to the limit, capped at 1000, or 1000 without one, in single-request mode' do
    mode = SpiceDB::Paging::SINGLE_REQUEST
    expect(requested(paging: mode, page_size: 5)).to eq([1000])
    expect(requested(paging: mode, limit: 20)).to eq([20])
    expect(requested(paging: mode, limit: 5000)).to eq([1000])
  end

  it 'sends an explicit size as given, without clamping it' do
    expect(requested(response_batch_size: 7)).to eq([7])
    expect(requested(response_batch_size: 5000, page_size: 10)).to eq([5000])
    expect(requested(response_batch_size: 7, paging: SpiceDB::Paging::SINGLE_REQUEST, limit: 3)).to eq([7])
  end

  it 'sends none when batching is disabled' do
    expect(requested(batching: SpiceDB::LookupBatching::DISABLED)).to eq([0])
    expect(requested(batching: SpiceDB::LookupBatching::DISABLED, response_batch_size: 7)).to eq([0])
  end

  [0, -1, 1.5, '10'].each do |invalid|
    it "rejects response_batch_size #{invalid.inspect} before transport" do
      requests = serve([])
      expect { client.lookup_resources(**arguments, response_batch_size: invalid) }
        .to raise_error(SpiceDB::LocalValidationError, /response_batch_size must be a positive integer/)
      expect(requests).to be_empty
    end
  end

  [true, nil, :disabled, SpiceDB::Paging::SINGLE_REQUEST].each do |invalid|
    it "rejects batching #{invalid.inspect} before transport" do
      expect { client.lookup_resources(**arguments, batching: invalid) }.to raise_error(SpiceDB::LocalValidationError, /LookupBatching/)
    end
  end

  it 'is not an option of lookup_subjects or read_relationships' do
    resource = SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'a')
    expect do
      client.lookup_subjects(resource: resource, permission: 'view', subject_type: 'user', consistency: SpiceDB::Consistency.full,
                             response_batch_size: 5)
    end.to raise_error(ArgumentError, /response_batch_size/)
    expect do
      client.read_relationships(SpiceDB::Filter.new(resource_type: 'document'), consistency: SpiceDB::Consistency.full,
                                                                                batching: SpiceDB::LookupBatching::DISABLED)
    end.to raise_error(ArgumentError, /batching/)
  end
end

RSpec.describe 'LookupResources response batching: a batched stream' do
  include LookupBatchFixtures

  it 'yields every result in order, with its fields and the response revision; only batch-end items carry cursors' do
    caveat = Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: ['ip'])
    serve([batch([result('a'), result('b', conditional, partial_caveat_info: caveat), result('c')], 'c3', looked_up_at: 'rev1'),
           batch([result('d'), result('e')], 'c5', looked_up_at: 'rev2')])

    items = client.lookup_resources(**arguments).to_a

    expect(items.map(&:resource_id)).to eq(%w[a b c d e])
    expect(items.map(&:permissionship)).to eq(%i[has_permission conditional_permission has_permission has_permission has_permission])
    expect(items[1].partial_caveat).to eq(SpiceDB::PartialCaveatInfo.new(missing_required_context: ['ip']))
    expect(items.map(&:cursor)).to eq([nil, nil, 'c3', nil, 'c5'])
    expect(items.map { |item| item.looked_up_at.to_s }).to eq(%w[rev1 rev1 rev1 rev2 rev2])
  end

  it 'yields nothing for an empty batch and keeps reading' do
    serve([batch([], nil), batch([result('a')], 'c1'), batch([], nil)])
    items = client.lookup_resources(**arguments).to_a
    expect(items.map(&:resource_id)).to eq(%w[a])
    expect(items.map(&:cursor)).to eq(%w[c1])
  end

  it 'reads a stream that mixes both forms' do
    serve([single('a', 'c1'), batch([result('b'), result('c')], 'c3'), single('d', 'c4')])
    expect(client.lookup_resources(**arguments).map(&:cursor)).to eq(['c1', nil, 'c3', 'c4'])
  end

  it 'reads a batch even when batching is disabled' do
    serve([batch([result('a'), result('b')], 'c2')])
    items = client.lookup_resources(**arguments, batching: SpiceDB::LookupBatching::DISABLED).to_a
    expect(items.map(&:cursor)).to eq([nil, 'c2'])
  end

  it 'maps a permissionship newer than the client to :unspecified' do
    serve([batch([result('a', 99)], 'c1')])
    expect(client.lookup_resources(**arguments).map(&:permissionship)).to eq(%i[unspecified])
  end

  it 'keeps batches in single-request mode' do
    requests = serve([batch([result('a'), result('b')], 'c2'), batch([result('c')], 'c3')])
    items = client.lookup_resources(**arguments, paging: SpiceDB::Paging::SINGLE_REQUEST, limit: 3, response_batch_size: 2).to_a
    expect(items.map(&:cursor)).to eq([nil, 'c2', 'c3'])
    expect(requests).to eq([[nil, 3, 2]])
  end
end

RSpec.describe 'LookupResources response batching: paging over batches' do
  include LookupBatchFixtures

  it 'sends each batch cursor with the next page request and ends on a short page' do
    requests = serve_results([granted] * 5)
    items = client.lookup_resources(**arguments, page_size: 2).to_a
    expect(items.map(&:resource_id)).to eq(%w[0 1 2 3 4])
    expect(items.map(&:cursor)).to eq([nil, '2', nil, '4', '5'])
    expect(requests).to eq([[0, 2, 2], [2, 2, 2], [4, 2, 2]])
  end

  it 'counts results, not messages, to decide whether a page is full' do
    # Each full page is two messages of two results: four results fill a
    # page of four, so another page follows.
    requests = serve_results([granted] * 6)
    items = client.lookup_resources(**arguments, page_size: 4, response_batch_size: 2).to_a
    expect(items.map(&:resource_id)).to eq(%w[0 1 2 3 4 5])
    expect(requests).to eq([[0, 4, 2], [4, 4, 2]])
  end

  it 'does not fetch another page after a page with fewer results than messages could suggest' do
    # One message holding the whole short page.
    requests = serve_results([granted] * 3)
    expect(client.lookup_resources(**arguments, page_size: 4).count).to eq(3)
    expect(requests).to eq([[0, 4, 4]])
  end

  it 'fails a full page whose batch cursor did not advance' do
    serve([batch([result('a'), result('b')], nil)])
    expect { client.lookup_resources(**arguments, page_size: 2).to_a }.to raise_error(SpiceDB::Error, /cursor did not advance/) do |error|
      expect(error.code).to eq(GRPC::Core::StatusCodes::INTERNAL)
    end
  end

  it 'resumes from a caller cursor with any batch size' do
    requests = serve_results([granted] * 5)
    items = client.lookup_resources(**arguments, page_size: 2, cursor: '2', response_batch_size: 1).to_a
    expect(items.map(&:resource_id)).to eq(%w[2 3 4])
    expect(requests.first).to eq([2, 2, 1])
  end

  it 'works against a server that answers one resource per message: every item has a cursor' do
    requests = serve_results([granted] * 5, batching: false)
    items = client.lookup_resources(**arguments, page_size: 2).to_a
    expect(items.map(&:resource_id)).to eq(%w[0 1 2 3 4])
    expect(items.map(&:cursor)).to eq(%w[1 2 3 4 5])
    expect(requests).to eq([[0, 2, 2], [2, 2, 2], [4, 2, 2]])
  end
end

RSpec.describe 'LookupResources response batching: the permissionship filter over a batch' do
  include LookupBatchFixtures

  let(:only) { SpiceDB::LookupPermissionshipFilter::HAS_PERMISSION_ONLY }

  it 'moves the cursor to the last kept item when trailing results are dropped' do
    serve([batch([result('a'), result('b', conditional), result('c'), result('d', conditional)], 'c4'),
           batch([result('e', conditional), result('f')], 'c6')])
    items = client.lookup_resources(**arguments, permissionship_filter: only).to_a
    expect(items.map(&:resource_id)).to eq(%w[a c f])
    expect(items.map(&:cursor)).to eq([nil, 'c4', 'c6'])
  end

  it 'puts no cursor anywhere for a wholly dropped batch, and still pages from its cursor' do
    requests = serve_results([granted, granted, conditional, conditional, granted])
    items = client.lookup_resources(**arguments, page_size: 2, permissionship_filter: only).to_a
    expect(items.map(&:resource_id)).to eq(%w[0 1 4])
    expect(items.map(&:cursor)).to eq([nil, '2', '5'])
    expect(requests).to eq([[0, 2, 2], [2, 2, 2], [4, 2, 2]])
  end

  it 'requests no batching with a limit, so the limited call ends on a cursor' do
    requests = serve_results([granted, conditional, granted, granted, granted])
    items = client.lookup_resources(**arguments, page_size: 4, limit: 2, permissionship_filter: only).to_a
    expect(items.map(&:resource_id)).to eq(%w[0 2])
    expect(items.last.cursor).to eq('3')
    expect(requests).to eq([[0, 4, 0]])
  end
end

RSpec.describe 'LookupResources response batching: a total limit' do
  include LookupBatchFixtures

  it 'shrinks the last page batch to the remaining count, so the last item carries a cursor' do
    requests = serve_results([granted] * 10)
    items = client.lookup_resources(**arguments, page_size: 4, limit: 6).to_a
    expect(items.map(&:resource_id)).to eq(%w[0 1 2 3 4 5])
    expect(items.map(&:cursor)).to eq([nil, nil, nil, '4', nil, '6'])
    # The wire limit stays the page size on every page.
    expect(requests).to eq([[0, 4, 4], [4, 4, 2]])
  end

  it 'keeps the batch size when it divides the remaining count' do
    requests = serve_results([granted] * 10)
    items = client.lookup_resources(**arguments, page_size: 10, limit: 6, response_batch_size: 3).to_a
    expect(items.map(&:cursor)).to eq([nil, nil, '3', nil, nil, '6'])
    expect(requests).to eq([[0, 10, 3]])
  end

  it 'turns batching off for the page when neither holds' do
    requests = serve_results([granted] * 10)
    items = client.lookup_resources(**arguments, page_size: 10, limit: 5, response_batch_size: 3).to_a
    expect(items.map(&:cursor)).to eq(%w[1 2 3 4 5])
    expect(requests).to eq([[0, 10, 0]])
  end

  it 'resumes an unlimited call from the limited call\'s last cursor' do
    serve_results([granted] * 10)
    first = client.lookup_resources(**arguments, page_size: 4, limit: 6).to_a
    rest = client.lookup_resources(**arguments, page_size: 4, cursor: first.last.cursor).to_a
    expect(rest.map(&:resource_id)).to eq(%w[6 7 8 9])
  end

  it 'never puts a batch cursor on an item when the limit cuts a batch short' do
    # A server that batched differently than asked: resuming from the
    # batch's cursor would skip its unyielded results.
    serve([batch([result('a'), result('b'), result('c')], 'c3')])
    items = client.lookup_resources(**arguments, limit: 2).to_a
    expect(items.map(&:resource_id)).to eq(%w[a b])
    expect(items.map(&:cursor)).to eq([nil, nil])
  end
end

RSpec.describe 'SpiceDB::LookupBatches.page_batch_size' do
  include LookupBatchFixtures

  def size(batch_size, page_size:, limit:, delivered: 0, single: false, has_permission_only: false)
    SpiceDB::LookupBatches.page_batch_size(batch_size, page_size: page_size, limit: limit, delivered: delivered, single: single,
                                                       has_permission_only: has_permission_only)
  end

  it 'is the configured size without a limit, in single-request mode, or while a full page remains' do
    expect(size(100, page_size: 100, limit: nil)).to eq(100)
    expect(size(100, page_size: 100, limit: 7, single: true)).to eq(100)
    expect(size(100, page_size: 100, limit: 250)).to eq(100)
    expect(size(100, page_size: 100, limit: 250, delivered: 100)).to eq(100)
    expect(size(30, page_size: 100, limit: 100)).to eq(30)
  end

  it 'is the remaining count when that is smaller than the batch size' do
    expect(size(100, page_size: 100, limit: 250, delivered: 200)).to eq(50)
    expect(size(100, page_size: 100, limit: 1)).to eq(1)
  end

  it 'is unchanged when it divides the remaining count' do
    expect(size(25, page_size: 100, limit: 250, delivered: 200)).to eq(25)
    expect(size(50, page_size: 100, limit: 50)).to eq(50)
  end

  it 'is 0 when the remaining count is larger and not a multiple' do
    expect(size(30, page_size: 100, limit: 250, delivered: 200)).to eq(0)
  end

  it 'is 0 with a limit and the unconditional-only filter, and stays 0 when batching is disabled' do
    expect(size(100, page_size: 100, limit: 250, has_permission_only: true)).to eq(0)
    expect(size(100, page_size: 100, limit: nil, has_permission_only: true)).to eq(100)
    expect(size(0, page_size: 100, limit: 50)).to eq(0)
  end
end

RSpec.describe 'LookupResources response batching: conversion back to the protocol' do
  include LookupBatchFixtures

  it 'converts a native result back to the one-resource protocol form' do
    serve([batch([result('a'), result('b')], 'c2')])
    protos = client.lookup_resources(**arguments).map { |item| SpiceDB::ProtoConv.lookup_resource_to_proto(item) }
    expect(protos.map(&:resource_object_id)).to eq(%w[a b])
    expect(protos.map(&:batched)).to eq([nil, nil])
    expect(protos.map { |proto| proto.after_result_cursor&.token }).to eq([nil, 'c2'])
  end
end

# A result inside a response batch is validated like a one-resource response;
# the whole batch is validated before any of it is yielded.
RSpec.describe 'LookupResources response batching: a malformed batch result' do
  include LookupBatchFixtures

  def expect_internal(pattern, requests)
    seen = []
    expect { client.lookup_resources(**arguments).each { |item| seen << item.resource_id } }.to raise_error(SpiceDB::Error, pattern) do |error|
      expect(error.code).to eq(GRPC::Core::StatusCodes::INTERNAL)
    end
    expect(seen).to eq(%w[a])
    expect(requests.length).to eq(1), 'a malformed response is not retried'
  end

  it 'fails a result with no resource ID' do
    broken = Authzed::Api::V1::LookupResourcesResult.new(permissionship: granted)
    requests = serve([batch([result('a')], 'c1'), batch([result('b'), broken], 'c3')])
    expect_internal(/client detected malformed response from server: lookup_resources: resource_object_id is missing/, requests)
  end

  it 'fails a result with an unset permissionship' do
    broken = Authzed::Api::V1::LookupResourcesResult.new(resource_object_id: 'c')
    requests = serve([batch([result('a')], 'c1'), batch([broken, result('b')], 'c3')])
    expect_internal(/client detected malformed response from server: lookup_resources: permissionship is unset/, requests)
  end
end
