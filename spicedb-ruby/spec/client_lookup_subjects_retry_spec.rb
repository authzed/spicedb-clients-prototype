# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

# Retry safety for #lookup_subjects, which has to get BOTH halves right.
#
# It originally wrapped the entire streaming call in #with_retry, so a
# mid-stream failure -- after results had already reached the caller --
# retried from scratch and re-yielded them. LookupSubjects' proto marks its
# cursor unimplemented, so unlike #lookup_resources/#export_relationships
# there is no resume mechanism; retry after a yield is structurally wrong
# here, not merely unsafe in the usual sense.
#
# The first correction removed retry entirely, including for stream
# ESTABLISHMENT -- which over-corrected, and left this the only client of
# seven where a transient UNAVAILABLE while OPENING a LookupSubjects stream
# failed the caller outright instead of retrying. Nothing had been
# delivered at that point, so there was nothing a retry could replay: the
# zero-produced guard the other six use (and that #export_relationships two
# methods away already used) was the whole answer.
#
# Both specs below are therefore load-bearing in opposite directions. One
# alone can be satisfied by a wrong implementation.

# Fake streaming sources for the two failure shapes. Namespaced and defined outside
# RSpec.describe so they are real, reusable classes rather than per-example constants
# (rubocop's Lint/ConstantDefinitionInBlock).
module SubjectStreams
  # Yields `responses` one at a time, then raises `error` instead of completing --
  # a mid-stream failure, after results were already delivered.
  class FailsMidStream
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

  # The first `fail_times` opens raise before yielding anything -- an establishment
  # failure, with nothing delivered and so nothing a retry could replay.
  class FailsOnOpen
    include Enumerable

    def initialize(responses, error, fail_times: 1)
      @responses = responses
      @error = error
      @fail_times = fail_times
      @opens = 0
    end

    def each(&)
      return enum_for(:each) unless block_given?

      @opens += 1
      raise @error if @opens <= @fail_times

      @responses.each(&)
    end
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
    stream = SubjectStreams::FailsMidStream.new(responses, GRPC::Unavailable.new('mid-stream failure'))

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
  it 'retries establishment when the stream fails before yielding anything' do
    responses = [lookup_subjects_response('sally'), lookup_subjects_response('jimmy')]
    stream = SubjectStreams::FailsOnOpen.new(responses, GRPC::Unavailable.new('stream never opened'))

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:lookup_subjects).and_return(stream)
    client.instance_variable_set(
      :@proto_client, double('proto_client', permissions: permissions_service)
    )

    seen = client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user')
                 .map { |s| s.subject.subject_id }

    expect(seen).to eq(%w[sally jimmy]),
                    'a transient failure at establishment delivered nothing, so retrying it ' \
                    'must succeed rather than fail the caller -- every other client retries here'
  end
end
