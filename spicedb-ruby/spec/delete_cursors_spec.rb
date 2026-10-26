# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

RSpec.describe 'non-atomic deletion cursor forwarding' do
  ['second', '', nil].each do |middle_cursor|
    [false, true].each do |failure|
      it "forwards #{middle_cursor.inspect} and retains acknowledged progress on failure=#{failure}" do
        client = SpiceDB::Client.new_plaintext('localhost:50051', 'key')
        responses = ['first', middle_cursor, nil].each_with_index.map do |cursor, index|
          response = Authzed::Api::V1::DeleteRelationshipsResponse.new(
            deleted_at: Authzed::Api::V1::ZedToken.new(token: index.to_s), relationships_deleted_count: 2,
            deletion_progress: index == 2 ? :DELETION_PROGRESS_COMPLETE : :DELETION_PROGRESS_PARTIAL
          )
          response.after_result_cursor = Authzed::Api::V1::Cursor.new(token: cursor) unless cursor.nil?
          response
        end
        error = SpiceDB::UnavailableError.new('later batch failed')
        responses[-1] = error if failure
        requests = []
        allow(client.proto_client.permissions).to receive(:delete_relationships) do |request, **_kwargs|
          requests << request
          response = responses.shift
          raise response if response.is_a?(Exception)

          response
        end
        result = nil
        begin
          result = client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'document'), page_size: 2)
        rescue SpiceDB::UnavailableError => e
          expect(failure).to be(true)
          expect(e).to equal(error)
          result = e.delete_progress
        end
        expect(result.deleted_count).to eq(failure ? 4 : 6)
        expect(result.revision).to eq(SpiceDB::ZedToken.new(failure ? '1' : '2'))
        expect(result.cursor).to eq(failure ? middle_cursor : nil)
        expect(requests.length).to eq(3)
        expect(requests[0].optional_cursor).to be_nil
        expect(requests[1].optional_cursor.token).to eq('first')
        if middle_cursor && !middle_cursor.empty?
          expect(requests[2].optional_cursor.token).to eq(middle_cursor)
        else
          expect(requests[2].optional_cursor).to be_nil
        end
      ensure
        client&.close
      end
    end
  end
end
