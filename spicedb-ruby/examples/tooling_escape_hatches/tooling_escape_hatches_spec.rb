# frozen_string_literal: true

require 'grpc'
require 'spicedb_proto'

require_relative '../spec_helper'

# What tooling (backup/restore, CLIs, MCP servers) needs from the native client
# without dropping to `proto_client`: resumable paging with cursors and
# revisions, SpiceDB's full tuple syntax, the server's real import status,
# all-or-nothing and progress-reporting deletes, response metadata, a client
# over an existing channel, watch schema events, and errors that ordinary
# grpc-ruby status handling understands.
TOOLING_SCHEMA = <<~SCHEMA
  use expiration

  caveat on_day(day string, allowed list<string>) {
    day in allowed
  }

  definition user {}

  definition document {
  	relation viewer: user | user with on_day | user with expiration
  	relation editor: user
  	relation owner: user
  	permission view = viewer + editor + owner
  	permission edit = editor + owner
  	permission delete = owner
  }
SCHEMA

RSpec.describe 'Tooling escape hatches' do
  let(:documents) { SpiceDB::Filter.new(resource_type: 'document') }

  def touch_all(tuples)
    txn = SpiceDB::Transaction.new
    tuples.each { |tuple| txn.touch(SpiceDB::Relationship.from_tuple(tuple)) }
    client.write(txn).revision
  end

  it 'reads across several pages with stable arguments and resumes from a cursor' do
    touch_all(Array.new(5) { |index| "document:doc#{index}#viewer@user:alice" })
    # SpiceDB hashes consistency and page size into every cursor, so a pager
    # that changed either after the first page would fail on page two.
    items = client.read_relationships(documents, consistency: SpiceDB::Consistency.full, page_size: 2).to_a
    expect(items.map(&:resource_id)).to match_array(%w[doc0 doc1 doc2 doc3 doc4])
    expect(items).to all(have_attributes(read_at: be_a(SpiceDB::ZedToken), cursor: be_a(String)))

    resumed = client.read_relationships(documents, consistency: SpiceDB::Consistency.full, page_size: 2, cursor: items[1].cursor).to_a
    expect(resumed.map(&:resource_id)).to eq(items.drop(2).map(&:resource_id))

    alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
    looked_up = client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice,
                                        consistency: SpiceDB::Consistency.full, page_size: 2, limit: 4).to_a
    expect(looked_up.length).to eq(4)
    expect(looked_up.last.cursor).not_to be_nil
  end

  it 'writes and reads back the full tuple syntax, caveats and expiration included' do
    client.write_schema(TOOLING_SCHEMA)
    tuples = ['document:caveated#viewer@user:bob[on_day:{"allowed":["monday","tuesday"]}]',
              'document:expiring#viewer@user:carol[expiration:2099-01-02T03:04:05Z]']
    touch_all(tuples)
    read = client.read_relationships(documents.with_relation('viewer'), consistency: SpiceDB::Consistency.full).map(&:to_s)
    expect(read).to match_array(tuples)
  end

  it "reports the server's status when it aborts an import" do
    duplicate = SpiceDB::Relationship.from_tuple('document:dup#viewer@user:alice')
    touch_all([duplicate.to_s])
    expect { client.import_relationships([duplicate], batch_size: 1) }.to raise_error(SpiceDB::AlreadyExistsError) do |error|
      expect(error.code).to eq(GRPC::Core::StatusCodes::ALREADY_EXISTS)
    end
  end

  it 'deletes all-or-nothing, and reports delete-all progress per batch' do
    touch_all(Array.new(3) { |index| "document:del#{index}#viewer@user:alice" })
    expect { client.delete_relationships(documents, limit: 2, partial: SpiceDB::DeletePartial::DISALLOWED) }
      .to raise_error(GRPC::BadStatus)
    expect(client.read_relationships(documents, consistency: SpiceDB::Consistency.full).count).to eq(3)

    progress = []
    result = client.delete_all_relationships_non_atomic(documents, page_size: 2, on_progress: ->(step) { progress << step.deleted_count })
    expect(result.deleted_count).to eq(3)
    expect(progress.last).to eq(3)
  end

  it 'captures response metadata and works over a caller-owned channel' do
    capture = SpiceDB::ResponseMetadata.new
    client.read_schema(call_options: SpiceDB::CallOptions.new(metadata: { 'x-request-id' => 'tooling-example' }, response_metadata: capture))
    # SpiceDB reports per-request dispatch metadata in trailers.
    expect(capture.trailer).to include('io.spicedb.respmeta.dispatchedoperationscount')

    channel = GRPC::Core::Channel.new(SPICEDB_ENDPOINT, {}, :this_channel_is_insecure)
    owned = SpiceDB::Client.from_channel(channel, interceptors: [SpiceDBProto::BearerTokenInterceptor.new(SPICEDB_TOKEN)])
    expect(owned.read_schema.schema).to include('definition document')
    owned.close
    expect(SpiceDB::Client.from_channel(channel, interceptors: [SpiceDBProto::BearerTokenInterceptor.new(SPICEDB_TOKEN)])
      .read_schema.revision).to be_a(SpiceDB::ZedToken)
    channel.close
  end

  it 'reports schema changes on watch' do
    start = touch_all(['document:watched#viewer@user:alice'])
    events = []
    stream = client.watch(start_revision: start, content: [SpiceDB::WatchContent::RELATIONSHIPS, SpiceDB::WatchContent::SCHEMA], timeout: 20)
    writer = Thread.new do
      sleep 0.5
      client.write_schema(TOOLING_SCHEMA)
    end
    stream.each do |event|
      events << event
      break if event.schema_updated
    end
    writer.join
    expect(events.last.schema_updated).to be(true)
  end

  it 'raises errors that grpc-ruby status handling understands' do
    check = SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'nosuchtype', object_id: 'x'), permission: 'view',
                                         subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'a')))
    client.check(check, consistency: SpiceDB::Consistency.full)
    raise 'expected the check to fail'
  rescue GRPC::BadStatus => e
    expect(e).to be_a(SpiceDB::FailedPreconditionError)
    expect(e.code).to eq(GRPC::Core::StatusCodes::FAILED_PRECONDITION)
    expect(e.server_message).to include('nosuchtype')
  end
end
