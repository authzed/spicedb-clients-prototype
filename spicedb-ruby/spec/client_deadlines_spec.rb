# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

# How long a stalling handler stalls before finally answering. Every
# per-spec deadline below is far smaller than this, so a passing spec proves
# the client's timeout fired -- it did not just get lucky with scheduling.
# Also short enough that RpcServer#stop's forced worker-thread teardown
# (bounded by pool_keep_alive) stays fast.
STALL_SECONDS = 1.5

# Wall-clock budget for a single watchdog-wrapped call -- see
# run_with_watchdog below.
WATCHDOG_SECONDS = 10

# gRPC service handlers used to prove deadline enforcement. check_bulk_permissions
# and write_relationships back the unary specs (never respond inside the spec's
# deadline). read_relationships backs the streaming non-inheritance spec: it
# stalls past the client's tiny unary default before finally yielding, proving
# the stream was never bound by it.
class StallingPermissionsService < Authzed::Api::V1::PermissionsService::Service
  def check_bulk_permissions(_request, _call)
    sleep(STALL_SECONDS)
    Authzed::Api::V1::CheckBulkPermissionsResponse.new
  end

  def write_relationships(_request, _call)
    sleep(STALL_SECONDS)
    Authzed::Api::V1::WriteRelationshipsResponse.new
  end

  def read_relationships(_request, _call)
    sleep(STALL_SECONDS)
    [
      Authzed::Api::V1::ReadRelationshipsResponse.new(
        relationship: Authzed::Api::V1::Relationship.new(
          resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'a'),
          relation: 'viewer',
          subject: Authzed::Api::V1::SubjectReference.new(
            object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'jimmy')
          )
        ),
        after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor1')
      )
    ]
  end
end

# Call-deadline enforcement. Root DESIGN.md, "RULE: A unary call must have a deadline".
#
# Runs a real GRPC::RpcServer whose handlers deliberately stall, so these
# specs exercise the real `deadline:` plumbing through grpc-ruby end to end
# -- a `double`-based mock (as used elsewhere in this suite) can't prove a
# deadline is actually enforced, since grpc's deadline machinery lives below
# the mock. Each call is wrapped in `run_with_watchdog`, which fails the spec
# (instead of hanging it, and CI along with it) if a regression reintroduces
# an unbounded call.
RSpec.describe 'SpiceDB::Client call deadlines' do
  def start_server
    server = GRPC::RpcServer.new(pool_size: 4, pool_keep_alive: 0.1)
    port = server.add_http2_port('localhost:0', :this_port_is_insecure)
    server.handle(StallingPermissionsService.new)
    Thread.new { server.run }
    server.wait_till_running(5)
    [server, port]
  end

  # Runs +block+ on a background thread; fails the spec if it outlives
  # +seconds+. Without this, a regression that drops `deadline:` entirely
  # could hang this spec -- and the CI job running it -- instead of failing.
  def run_with_watchdog(seconds = WATCHDOG_SECONDS)
    result = nil
    error = nil
    t = Thread.new do
      result = yield
    rescue StandardError => e
      error = e
    end
    unless t.join(seconds)
      t.kill
      raise "call did not return within #{seconds}s -- deadline enforcement regressed"
    end
    raise error if error

    result
  end

  let(:rel) { SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'jimmy') }

  it 'fails a unary call against a stub that never responds with DeadlineExceededError, not a hang' do
    server, port = start_server
    begin
      client = SpiceDB::Client.new_plaintext("localhost:#{port}", 'testtoken', default_timeout: 0.2)
      start = Time.now
      expect do
        run_with_watchdog { client.check_permissions(SpiceDB::Consistency.full, 'view', rel) }
      end.to raise_error(SpiceDB::DeadlineExceededError)
      elapsed = Time.now - start
      client.close
    ensure
      server.stop
    end
    expect(elapsed).to be < STALL_SECONDS,
                       "the call must fail at the ~0.2s client default, not wait out the server's " \
                       "#{STALL_SECONDS}s stall (elapsed=#{elapsed.round(2)}s)"
  end

  it 'lets a per-call timeout: override a much larger client default' do
    server, port = start_server
    begin
      # Client default is larger than the server's stall -- if the per-call
      # override did not take effect, this call would not fail quickly.
      client = SpiceDB::Client.new_plaintext("localhost:#{port}", 'testtoken', default_timeout: STALL_SECONDS * 10)
      start = Time.now
      expect do
        run_with_watchdog { client.check_permissions(SpiceDB::Consistency.full, 'view', rel, timeout: 0.2) }
      end.to raise_error(SpiceDB::DeadlineExceededError)
      elapsed = Time.now - start
      client.close
    ensure
      server.stop
    end
    expect(elapsed).to be < STALL_SECONDS,
                       "the per-call timeout: 0.2 must override the large client default (elapsed=#{elapsed.round(2)}s)"
  end

  it 'does not cut a streaming call off at the (tiny) unary default' do
    server, port = start_server
    begin
      # default_timeout is far smaller than the server's stall. If
      # read_relationships inherited it, this would raise
      # DeadlineExceededError instead of yielding the item.
      client = SpiceDB::Client.new_plaintext("localhost:#{port}", 'testtoken', default_timeout: 0.1)
      filter = SpiceDB::Filter.new(resource_type: 'document')
      start = Time.now
      got = run_with_watchdog { client.read_relationships(SpiceDB::Consistency.full, filter).to_a }
      elapsed = Time.now - start
      client.close
    ensure
      server.stop
    end
    expect(got.map(&:resource_id)).to eq(['a'])
    expect(elapsed).to be >= STALL_SECONDS,
                       'the stream must outlive the tiny unary default -- it should have waited out the ' \
                       "server's #{STALL_SECONDS}s stall (elapsed=#{elapsed.round(2)}s)"
  end

  describe 'default_timeout' do
    it 'defaults to 30 seconds, mirroring authzed-node DEFAULT_DEADLINE_MS' do
      client = SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken')
      expect(client.instance_variable_get(:@default_timeout)).to eq(30)
    end

    it 'falls back to the client default when no per-call timeout is given' do
      client = SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken', default_timeout: 7.5)
      expect(client.send(:effective_timeout, nil)).to eq(7.5)
      expect(client.send(:effective_timeout, 1.5)).to eq(1.5)
    end
  end
end
