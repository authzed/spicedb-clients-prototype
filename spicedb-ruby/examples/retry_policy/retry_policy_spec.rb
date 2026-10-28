# frozen_string_literal: true

require 'grpc'
require 'spicedb_proto'

require_relative '../spec_helper'

# Demonstrates which calls this client retries on your behalf and which it
# deliberately does not -- see root DESIGN.md, "RULE: Automatic retry is for
# idempotent operations only".
#
# The rule exists because a silently retried mutation produces a confident wrong
# answer. If a WriteRelationships carrying OPERATION_CREATE commits and the
# response is lost, the retry comes back ALREADY_EXISTS -- and the caller
# concludes a write failed that in fact succeeded. Retrying reads is free;
# retrying mutations is only safe when the caller opted in knowing that.
#
# Attempts are counted *server-side*, which is the only way to tell a retry from
# its absence: from the caller's side a transparently-retried success and a
# first-try success are identical, and that is exactly the property that would
# rot unnoticed.
#
# It stands up a stand-in SpiceDB because a real one cannot be asked to fail
# transiently on demand.
# Fails a configurable number of opening attempts per RPC and counts every one.
class CountingService < Authzed::Api::V1::PermissionsService::Service
  attr_reader :check_attempts, :write_attempts

  def initialize(check_failures: 0, check_error: nil, write_failures: Float::INFINITY)
    @check_attempts = 0
    @write_attempts = 0
    @write_failures = write_failures
    @check_failures = check_failures
    @check_error = check_error || GRPC::Unavailable
    super()
  end

  def check_permission(_request, _call)
    @check_attempts += 1
    raise @check_error, 'transient, from the stand-in' if @check_attempts <= @check_failures

    Authzed::Api::V1::CheckPermissionResponse.new(permissionship: 2)
  end

  def write_relationships(_request, _call)
    @write_attempts += 1
    # Fails transiently (by default, always). A retrying client would come back.
    raise GRPC::Unavailable, 'transient, from the stand-in' if @write_attempts <= @write_failures

    Authzed::Api::V1::WriteRelationshipsResponse.new(written_at: Authzed::Api::V1::ZedToken.new(token: 'written'))
  end
end

RSpec.describe 'Retry policy' do
  def with_stand_in(service, **options, &block)
    server = GRPC::RpcServer.new(pool_size: 4)
    port = server.add_http2_port('127.0.0.1:0', :this_port_is_insecure)
    server.handle(service)
    thread = Thread.new { server.run }
    server.wait_till_running
    begin
      SpiceDB::Client.new_plaintext("127.0.0.1:#{port}", 'some-token', **options, &block)
    ensure
      server.stop
      thread.join(5)
    end
  end

  it 'retries a read transparently', :no_spicedb do
    # Two UNAVAILABLE responses, then success. The caller sees one successful
    # check and never learns the first two attempts happened -- the entire value
    # of retrying reads, and safe precisely because a repeated read changes
    # nothing.
    service = CountingService.new(check_failures: 2)
    with_stand_in(service) do |c|
      rel = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme'), relation: 'view',
                                      subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
      result = c.check(SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'view', subject: rel.subject), consistency: SpiceDB::Consistency.full)
      expect(result.has_permission?).to be(true)
    end

    expect(service.check_attempts).to eq(3),
                                      'expected 2 failures plus 1 success = 3 attempts, got ' \
                                      "#{service.check_attempts} (0 or 1 means reads are not " \
                                      'being retried at all)'
  end

  it 'does not retry a mutation', :no_spicedb do
    # The same transient code, on a write. The error reaches the caller on the
    # first attempt, so the caller -- who alone knows whether a replay is safe
    # for the transaction they built -- decides what happens next.
    service = CountingService.new
    with_stand_in(service) do |c|
      txn = SpiceDB::Transaction.new
      txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme'), relation: 'viewer',
                                          subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                          object_id: 'alice'), relation: nil)))
      expect { c.write(txn) }.to raise_error(SpiceDB::UnavailableError)
    end

    expect(service.write_attempts).to eq(1),
                                      'a mutation must not be retried silently: a lost response ' \
                                      'would otherwise leave the caller believing a committed ' \
                                      "write had failed (saw #{service.write_attempts} attempts)"
  end

  it 'does not retry RESOURCE_EXHAUSTED, even on a read', :no_spicedb do
    # In SpiceDB this code means memory load-shed or a deterministic
    # MaxDepthExceeded. Retrying the first makes the overload worse; the second
    # can never succeed however many times it is tried.
    service = CountingService.new(check_failures: 99, check_error: GRPC::ResourceExhausted)
    with_stand_in(service) do |c|
      rel = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme'), relation: 'view',
                                      subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
      expect { c.check(SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'view', subject: rel.subject), consistency: SpiceDB::Consistency.full) }
        .to raise_error(SpiceDB::ResourceExhaustedError)
    end

    expect(service.check_attempts).to eq(1),
                                      'RESOURCE_EXHAUSTED must not be retried: retrying turns a ' \
                                      'load-shedding SpiceDB into a client-driven retry storm ' \
                                      "(saw #{service.check_attempts} attempts)"
  end

  def check_readme(client)
    alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
    client.check(SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme'),
                                              permission: 'view', subject: alice), consistency: SpiceDB::Consistency.full)
  end

  it 'lets a caller replace the policy, opting into RESOURCE_EXHAUSTED and logging each retry', :no_spicedb do
    # A tool with its own policy (zed's --max-retries) supplies it client-wide
    # instead of stacking it on the default. Mutations still never retry.
    retries = []
    policy = SpiceDB::RetryPolicy.new(max_retries: 1, initial_backoff: 0.01,
                                      retryable_codes: [GRPC::Core::StatusCodes::RESOURCE_EXHAUSTED, GRPC::Core::StatusCodes::UNAVAILABLE],
                                      on_retry: ->(event) { retries << [event.operation, event.attempt, event.error.class] })
    service = CountingService.new(check_failures: 1, check_error: GRPC::ResourceExhausted)
    with_stand_in(service, retry_policy: policy) { |c| expect(check_readme(c).has_permission?).to be(true) }

    expect(service.check_attempts).to eq(2)
    expect(retries).to eq([['check_permission', 1, SpiceDB::ResourceExhaustedError]])
  end

  it 'turns every automatic retry off with a named mode', :no_spicedb do
    service = CountingService.new(check_failures: 1)
    with_stand_in(service, retries: SpiceDB::Retries::DISABLED) do |c|
      expect { check_readme(c) }.to raise_error(SpiceDB::UnavailableError)
    end
    expect(service.check_attempts).to eq(1)
  end

  def readme_viewer(operation)
    alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
    relationship = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme'), relation: 'viewer',
                                             subject: alice)
    SpiceDB::Transaction.new.public_send(operation, relationship)
  end

  it 'lets one idempotent write opt in to retries, and refuses the opt-in elsewhere', :no_spicedb do
    # A touch-only write without preconditions converges to the same state
    # however often it is replayed, so the caller may opt that one call in.
    # A create (or a delete, or anything preconditioned) is refused before
    # any request: replaying it after a lost response would misreport it.
    once = SpiceDB::RetryPolicy.new(max_retries: 1, initial_backoff: 0.01)
    service = CountingService.new(write_failures: 1)
    with_stand_in(service) do |c|
      expect(c.write(readme_viewer(:touch), retry_policy: once).revision.to_s).to eq('written')
      expect { c.write(readme_viewer(:create), retry_policy: once) }.to raise_error(SpiceDB::InvalidArgumentError)
    end
    expect(service.write_attempts).to eq(2), 'one failure, one retried success, and no request for the refused create'
  end

  it 'exempts one call from the client policy', :no_spicedb do
    # A per-call policy wins for that call (and, for a stream, its
    # establishment): here one check is never retried while the client keeps
    # retrying every other read.
    service = CountingService.new(check_failures: 1)
    with_stand_in(service) do |c|
      alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
      check = SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme'), permission: 'view',
                                           subject: alice)
      expect { c.check(check, consistency: SpiceDB::Consistency.full, retry_policy: SpiceDB::RetryPolicy.new(max_retries: 0)) }
        .to raise_error(SpiceDB::UnavailableError)
    end
    expect(service.check_attempts).to eq(1)
  end

  it 'sets a policy for every call of one operation, client-wide', :no_spicedb do
    # operation_retry_policies sits between a per-call retry_policy (which
    # still wins) and the client-wide policy. A write policy retries only
    # idempotent transactions (Transaction#idempotent?) and leaves others
    # unretried, without an error; deletes, imports and counter registration
    # reject a retrying policy when the client is built.
    once = SpiceDB::RetryPolicy.new(max_retries: 1, initial_backoff: 0.01)
    never = SpiceDB::RetryPolicy.new(max_retries: 0)
    service = CountingService.new(check_failures: 1, write_failures: 1)
    with_stand_in(service, operation_retry_policies: { check: never, write: once }) do |c|
      expect { check_readme(c) }.to raise_error(SpiceDB::UnavailableError)
      expect(readme_viewer(:touch)).to be_idempotent
      expect(c.write(readme_viewer(:touch)).revision.to_s).to eq('written')
    end
    expect(service.check_attempts).to eq(1)
    expect(service.write_attempts).to eq(2)

    expect { SpiceDB::Client.new_plaintext('127.0.0.1:1', 'some-token', operation_retry_policies: { delete_relationships: once }) }
      .to raise_error(SpiceDB::InvalidArgumentError, /not idempotent/)
  end

  it 'records calls and injects faults in the in-memory test double', :no_spicedb do
    # A restore test: the third import batch fails, and nothing is applied.
    require 'spicedb/testing'
    store = SpiceDB::Testing::Store.new
    store.fault_hook = lambda do |fault|
      next unless fault.stage == SpiceDB::Testing::FaultStage::IMPORT_BATCH && fault.item == 2

      SpiceDB::Testing::Store.server_error(GRPC::Core::StatusCodes::ALREADY_EXISTS, 'duplicate relationship')
    end
    relationships = (0...10).map { |index| SpiceDB::Relationship.from_tuple("document:d#{index}#viewer@user:alice") }
    expect { store.import_relationships(relationships, batch_size: 3) }.to raise_error(SpiceDB::AlreadyExistsError)
    expect(store.relationships).to be_empty
    expect(store.calls_of(:import_relationships).first.options[:batch_size]).to eq(3)
  end
end
