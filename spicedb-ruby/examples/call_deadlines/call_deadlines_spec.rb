# frozen_string_literal: true

require 'socket'
require 'timeout'

require_relative '../spec_helper'

# The deadline handed to the calls against the wedged server below. Short,
# because the point is to watch it expire.
WEDGED_TIMEOUT = 2

# Wall-clock bound on a wedged call. If a call with a 2s deadline has not
# returned after this long, the deadline is not reaching the RPC -- and the
# example fails with that message instead of hanging the CI job.
WEDGED_WATCHDOG = 17

# Demonstrates the client-level `default_timeout:` construction parameter, a
# per-call `timeout:` override on a unary call, and that bulk import
# (`import_relationships`) is a client-streaming call that is NOT bounded by
# `default_timeout` -- see root DESIGN.md, "RULE: A unary call must have a
# deadline".
RSpec.describe 'Call deadlines' do
  it 'accepts default_timeout: on the real client construction path' do
    # default_timeout: applies to every unary call that doesn't pass its own
    # timeout: override. This is the documented, real construction path --
    # not a mock -- so a signature drift here (e.g. the keyword silently
    # disappearing from Client.new_plaintext) would fail this example, not
    # just a unit spec against a stalling stub.
    SpiceDB::Client.new_plaintext(SPICEDB_ENDPOINT, SPICEDB_TOKEN, default_timeout: 5) do |c|
      c.write_schema(TEST_SCHEMA)
      c.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'))

      txn = SpiceDB::Transaction.new
      txn.touch(SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'alice'))
      c.write(txn)

      rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
      result = c.check_permission(SpiceDB::Consistency.full, 'view', rel)
      expect(result.has_permission?).to be true

      c.delete_relationships(SpiceDB::Filter.new(resource_type: 'document').with_resource_id('readme'))
    end
  end

  it 'lets a per-call timeout: override the client default' do
    # 5 seconds is generous for a real call against a local SpiceDB -- this
    # exercises the real timeout: keyword end-to-end, not testing how small
    # a timeout can be.
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'alice'))
    client.write(txn, timeout: 5)

    rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
    result = client.check_permission(SpiceDB::Consistency.full, 'view', rel, timeout: 5)
    expect(result.has_permission?).to be true
  ensure
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document').with_resource_id('readme'))
  end

  it 'does not bound bulk import by the unary default' do
    # import_relationships (import_bulk_relationships) is client-streaming:
    # its duration scales with the size of the caller's dataset, not with
    # server latency, so it is explicitly excluded from default_timeout.
    # Calling it with no timeout: at all must still succeed.
    users = (1..50).map { |i| "user#{i}" }
    relationships = users.map do |u|
      SpiceDB::Relationship.from_triple('document', 'bulk', 'viewer', 'user', u)
    end
    num_loaded = client.import_relationships(relationships)
    expect(num_loaded).to eq(users.length)

    # A caller-supplied timeout: on the same client-streaming call must
    # still be honored -- the exclusion is from the *default*, not from the
    # ability to bound the call at all.
    more_relationships = users.map do |u|
      SpiceDB::Relationship.from_triple('document', 'bulk2', 'viewer', 'user', u)
    end
    num_loaded_bounded = client.import_relationships(more_relationships, timeout: 30)
    expect(num_loaded_bounded).to eq(users.length)
  ensure
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document').with_resource_id('bulk'))
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document').with_resource_id('bulk2'))
  end

  # A socket that accepts TCP connections and never speaks gRPC. The kernel
  # completes the handshake for connections sitting in the backlog, so a client
  # connects successfully and then waits forever for the HTTP/2 server preface.
  # That is what a wedged SpiceDB looks like from a client -- an open,
  # healthy-looking connection with no reply behind it -- and it is why "the
  # connection worked" is not a bound. Everything above this point passes
  # whether or not the deadline reaches the wire; the two examples below do not.
  def with_wedged_server
    listener = TCPServer.new('127.0.0.1', 0)
    yield "127.0.0.1:#{listener.addr[1]}"
  ensure
    listener&.close
  end

  # Runs the block under a watchdog, failing rather than hanging if a call that
  # was supposed to be bounded never comes back.
  def expect_deadline_to_fire(what, &block)
    Timeout.timeout(WEDGED_WATCHDOG, nil,
                    "a call with a #{WEDGED_TIMEOUT}s #{what} had not returned after " \
                    "#{WEDGED_WATCHDOG}s against a server that never answers: " \
                    'the deadline is not reaching the RPC') do
      # The specific error matters: `raise_error(StandardError)` is also
      # satisfied by SpiceDB::UnavailableError from a refused connection, which
      # says nothing at all about deadlines.
      expect(&block).to raise_error(SpiceDB::DeadlineExceededError)
    end
  end

  it 'expires default_timeout: against a server that never answers' do
    with_wedged_server do |endpoint|
      SpiceDB::Client.new_plaintext(endpoint, SPICEDB_TOKEN, default_timeout: WEDGED_TIMEOUT) do |wedged|
        rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
        expect_deadline_to_fire('default_timeout') do
          wedged.check_permission(SpiceDB::Consistency.full, 'view', rel)
        end
      end
    end
  end

  it 'expires a per-call timeout: against a server that never answers' do
    with_wedged_server do |endpoint|
      # No default_timeout: here, so only the per-call argument can bound this.
      # The override is a different code path, and one that accepted the
      # argument and dropped it would still pass every fast-local-call example
      # above.
      SpiceDB::Client.new_plaintext(endpoint, SPICEDB_TOKEN) do |wedged|
        rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
        expect_deadline_to_fire('per-call timeout') do
          wedged.check_permission(SpiceDB::Consistency.full, 'view', rel, timeout: WEDGED_TIMEOUT)
        end
      end
    end
  end
end
