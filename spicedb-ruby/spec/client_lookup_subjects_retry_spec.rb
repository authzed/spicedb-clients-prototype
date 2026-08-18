# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

# Regression coverage for Task 8(a): #lookup_subjects used to wrap the ENTIRE streaming call in
# #with_retry, so a mid-stream failure (after some results had already been yielded to the
# caller) retried the whole call from scratch and re-yielded results the caller had already
# seen. LookupSubjects' proto marks its cursor unimplemented, so unlike #lookup_resources/
# #export_relationships there is no resume mechanism at all -- retry here was structurally
# wrong, not merely unsafe in the usual "retry after first yield" sense. Mirrors #updates'
# documented reasoning for the same defect shape.

# A fake streaming source that yields `responses` one at a time, then raises `error` instead of
# completing -- simulating a mid-stream failure after some results were already delivered.
# Defined at top level (not inside RSpec.describe) so it's a real, reusable class rather than a
# per-example constant rubocop's Lint/ConstantDefinitionInBlock flags.
class FailingSubjectStream
  include Enumerable

  def initialize(responses, error)
    @responses = responses
    @error = error
  end

  def each(&)
    return enum_for(:each) unless block_given?

    @responses.each(&)
    raise @error
  end
end

RSpec.describe 'SpiceDB::Client#lookup_subjects retry safety' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  def resolved_subject(id)
    Authzed::Api::V1::ResolvedSubject.new(
      subject_object_id: id, permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
    )
  end

  def lookup_subjects_response(id)
    Authzed::Api::V1::LookupSubjectsResponse.new(subject: resolved_subject(id))
  end

  it 'does not deliver duplicates or retry when the stream fails after two items' do
    responses = [lookup_subjects_response('sally'), lookup_subjects_response('jimmy')]
    stream = FailingSubjectStream.new(responses, GRPC::Unavailable.new('mid-stream failure'))

    permissions_service = double('permissions_service')
    call_count = 0
    allow(permissions_service).to receive(:lookup_subjects) do
      call_count += 1
      stream
    end
    client.instance_variable_set(
      :@proto_client, double('proto_client', permissions: permissions_service)
    )

    seen = []
    expect do
      client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user').each do |s|
        seen << s.subject.subject_id
      end
    end.to raise_error(SpiceDB::UnavailableError)

    expect(seen).to eq(%w[sally jimmy]), 'must see each result exactly once, not replayed by a retry'
    expect(call_count).to eq(1), 'a mid-stream failure must not trigger a retried call at all'
  end
end
