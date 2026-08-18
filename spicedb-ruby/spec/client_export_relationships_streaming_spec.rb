# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'
require 'timeout'

# Regression coverage for the whole-dataset-buffering defect: #export_relationships used to
# fully drain #call_export_relationships' entire underlying server stream into a Ruby Array
# before yielding a single relationship to the caller. ExportBulkRelationships' page-size field
# (`optional_limit`) bounds only the number of relationships in a SINGLE response message --
# unlike every other paginated RPC's page-size field, which bounds the whole stream -- so the
# server keeps streaming further response messages on the SAME call until the entire dataset has
# been sent. Draining "the current page" therefore meant draining the entire export into memory
# first: an OOM risk for the one API most likely to face the largest dataset in the system.
#
# NeverEndingExportStream below simulates a still-open export (one response message delivered,
# then blocked forever, as a multi-million-relationship export in progress would be from the
# caller's point of view). Ruby's Enumerator#first uses a Fiber to pull values lazily: a
# yielder << call suspends the ENTIRE call stack at that exact point, including a loop that
# hasn't finished. The old implementation never called yielder << until the whole page's Array
# was already built (with no yield point inside that build), so nothing could interrupt the
# now-infinite drain -- #export_relationships(...).first would hang forever against a stream
# like this. The fix yields directly from inside the per-message loop, so .first returns after
# the very first message without ever touching the second (blocked) one.

# Defined at top level (not inside RSpec.describe) so it's a real, reusable class rather than a
# per-example constant rubocop's Lint/ConstantDefinitionInBlock flags.
class NeverEndingExportStream
  include Enumerable

  def initialize(first_response)
    @first_response = first_response
  end

  def each
    return enum_for(:each) unless block_given?

    yield @first_response
    # Never returns -- the real server would still be streaming further
    # response messages on this same call.
    loop { sleep 1 }
  end
end

RSpec.describe 'SpiceDB::Client#export_relationships streaming' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  def export_response(resource_id, cursor_token)
    Authzed::Api::V1::ExportBulkRelationshipsResponse.new(
      after_result_cursor: Authzed::Api::V1::Cursor.new(token: cursor_token),
      relationships: [
        Authzed::Api::V1::Relationship.new(
          resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: resource_id),
          relation: 'viewer',
          subject: Authzed::Api::V1::SubjectReference.new(
            object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'alice')
          )
        )
      ]
    )
  end

  it 'yields the first relationship without waiting for the (still-open) server stream to finish' do
    stream = NeverEndingExportStream.new(export_response('doc1', 'cursor-1'))
    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:export_bulk_relationships).and_return(stream)
    client.instance_variable_set(
      :@proto_client, double('proto_client', permissions: permissions_service)
    )

    first = nil
    begin
      Timeout.timeout(5) do
        first = client.export_relationships(SpiceDB::Consistency.full).first
      end
    rescue Timeout::Error
      raise 'export_relationships did not yield its first item within 5s -- it is buffering ' \
            'the whole (never-completing) stream before yielding anything'
    end

    expect(first.resource_id).to eq('doc1')
  end
end
