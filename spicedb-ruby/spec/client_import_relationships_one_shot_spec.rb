# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

# Regression coverage for Task 8(b): #import_relationships used to be wrapped in #with_retry, so
# a retryable-looking error on a ONE-SHOT source (File.foreach(...).lazy, a DB cursor -- anything
# that can only be iterated once) meant the retry attempt fed an already-exhausted enumerable,
# sending zero relationships and returning num_loaded == 0 AS SUCCESS.
#
# Group C Task 2 moved #import_relationships onto the non-retrying #call_once path (a genuine
# pre-existing bug it found, overlapping this finding). This spec is the regression test that
# proves that already resolves both halves of Task 8(b): a one-shot source is consumed exactly
# once and fully delivered, and a failure on that single attempt raises rather than reporting
# success.

# Mimics File.foreach(...).lazy / a DB cursor: #each raises if called a second time. Defined at
# top level (not inside RSpec.describe) so it's a real, reusable class rather than a per-example
# constant rubocop's Lint/ConstantDefinitionInBlock flags.
class OneShotEnumerable
  include Enumerable

  def initialize(items)
    @items = items
    @consumed = false
  end

  def each(&)
    raise 'OneShotEnumerable consumed twice -- a real one-shot source would raise here too' if @consumed

    @consumed = true
    return enum_for(:each) unless block_given?

    @items.each(&)
  end
end

RSpec.describe 'SpiceDB::Client#import_relationships one-shot enumerable safety' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  def a_relationship(id)
    SpiceDB::Relationship.from_triple('document', id, 'viewer', 'user', 'alice')
  end

  it 'sends every relationship from a one-shot source exactly once' do
    items = [a_relationship('doc1'), a_relationship('doc2'), a_relationship('doc3')]
    one_shot = OneShotEnumerable.new(items)

    sent = []
    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:import_bulk_relationships) do |requests, **_kwargs|
      requests.each { |req| sent.concat(req.relationships) }
      Authzed::Api::V1::ImportBulkRelationshipsResponse.new(num_loaded: sent.length)
    end
    client.instance_variable_set(
      :@proto_client, double('proto_client', permissions: permissions_service)
    )

    num_loaded = client.import_relationships(one_shot)

    expect(sent.length).to eq(3)
    expect(num_loaded).to eq(3)
  end

  it 'raises rather than silently reporting num_loaded == 0 as success when the single attempt fails' do
    one_shot = OneShotEnumerable.new([a_relationship('doc1')])

    call_count = 0
    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:import_bulk_relationships) do |requests, **_kwargs|
      call_count += 1
      requests.each { |_req| } # a real gRPC client-streaming call fully drains the request enumerable
      raise GRPC::Unavailable, 'down' # transient-looking, but must NOT be retried
    end
    client.instance_variable_set(
      :@proto_client, double('proto_client', permissions: permissions_service)
    )

    expect { client.import_relationships(one_shot) }.to raise_error(SpiceDB::UnavailableError)
    expect(call_count).to eq(1),
                          'import_relationships must attempt exactly once -- it may be fed a one-shot ' \
                          'source that a retry could not safely re-iterate'
  end
end
