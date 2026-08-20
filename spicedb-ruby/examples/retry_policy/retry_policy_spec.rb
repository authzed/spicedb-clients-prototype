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

  def initialize(check_failures: 0, check_error: nil)
    @check_attempts = 0
    @write_attempts = 0
    @check_failures = check_failures
    @check_error = check_error || GRPC::Unavailable
    super()
  end

  def check_bulk_permissions(request, _call)
    @check_attempts += 1
    raise @check_error, 'transient, from the stand-in' if @check_attempts <= @check_failures

    Authzed::Api::V1::CheckBulkPermissionsResponse.new(
      pairs: request.items.map do
        Authzed::Api::V1::CheckBulkPermissionsPair.new(
          item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(permissionship: 2)
        )
      end
    )
  end

  def write_relationships(_request, _call)
    @write_attempts += 1
    # Always fails, transiently. A retrying client would come back.
    raise GRPC::Unavailable, 'transient, from the stand-in'
  end
end

RSpec.describe 'Retry policy' do
  def with_stand_in(service, &block)
    server = GRPC::RpcServer.new(pool_size: 4)
    port = server.add_http2_port('127.0.0.1:0', :this_port_is_insecure)
    server.handle(service)
    thread = Thread.new { server.run }
    server.wait_till_running
    begin
      SpiceDB::Client.new_plaintext("127.0.0.1:#{port}", 'some-token', &block)
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
      rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
      result = c.check_permission(SpiceDB::Consistency.full, 'view', rel)
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
      txn.touch(SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'alice'))
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
      rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
      expect { c.check_permission(SpiceDB::Consistency.full, 'view', rel) }
        .to raise_error(SpiceDB::ResourceExhaustedError)
    end

    expect(service.check_attempts).to eq(1),
                                      'RESOURCE_EXHAUSTED must not be retried: retrying turns a ' \
                                      'load-shedding SpiceDB into a client-driven retry storm ' \
                                      "(saw #{service.check_attempts} attempts)"
  end
end
