# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'open3'
require 'rbconfig'

# One client is shared by every thread of a process, and connects lazily so a
# client built before a fork (a Rails initializer under Puma's preload_app!)
# works in the forked workers. See README.md, "Concurrency and forking".
module ConcurrencyFakes
  # Answers CheckPermission with HAS_PERMISSION for even resource IDs and
  # NO_PERMISSION for odd ones, yielding the thread mid-call so concurrent
  # calls interleave.
  class ParityPermissions
    attr_reader :calls

    def initialize
      @calls = 0
      @lock = Mutex.new
    end

    def check_permission(request, **)
      @lock.synchronize { @calls += 1 }
      Thread.pass
      granted = request.resource['object_id'].to_i.even? # protobuf keeps Object#object_id
      Authzed::Api::V1::CheckPermissionResponse.new(
        permissionship: granted ? :PERMISSIONSHIP_HAS_PERMISSION : :PERMISSIONSHIP_NO_PERMISSION,
        checked_at: Authzed::Api::V1::ZedToken.new(token: 'rev')
      )
    end
  end

  FakeProto = Struct.new(:permissions) do
    def close = nil
  end
end

RSpec.describe SpiceDB::Client do
  include ConcurrencyFakes

  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }

  def check_for(id)
    SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'doc', object_id: id), permission: 'view', subject: alice)
  end

  it 'answers every call correctly when one client is shared by many threads' do
    client = described_class.new_plaintext('localhost:50051', 'token')
    service = ConcurrencyFakes::ParityPermissions.new
    connects = 0
    client.instance_variable_set(:@connect, lambda {
      connects += 1
      ConcurrencyFakes::FakeProto.new(service)
    })
    results = Array.new(8) do |thread|
      Thread.new do
        Array.new(50) do |index|
          id = (thread * 50) + index
          [id, client.check(check_for(id), consistency: SpiceDB::Consistency.min_latency).has_permission?]
        end
      end
    end.flat_map(&:value)
    expect(results.length).to eq(400)
    expect(results).to all(satisfy { |id, granted| granted == id.even? })
    expect(service.calls).to eq(400)
    expect(connects).to eq(1)
  end

  it 'opens no connection until first use, and none after close' do
    client = described_class.new_plaintext('localhost:50051', 'token')
    expect(client.instance_variable_get(:@proto_client)).to be_nil
    expect(client.proto_client).to be_a(SpiceDBProto::Client)
    unused = described_class.new_plaintext('localhost:50051', 'token')
    unused.close
    expect { unused.proto_client }.to raise_error(SpiceDB::Error, 'client is closed')
    client.close
  end

  it 'still refuses remote plaintext and bad TLS material at construction' do
    expect { described_class.new_plaintext('evil.example.com:1234', 'token') }
      .to raise_error(SpiceDB::InvalidArgumentError, /allow_insecure_remote_credentials/)
    expect { described_class.new_custom_tls('spicedb.example.com:443', 'token', client_cert: 'pem') }
      .to raise_error(SpiceDB::InvalidArgumentError, /client_key/)
  end

  describe '#connect!' do
    it 'fails within the timeout for an unreachable endpoint' do
      client = described_class.new_plaintext('127.0.0.1:1', 'token') # nothing listens on port 1
      started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      expect { client.connect!(timeout: 1) }.to raise_error(SpiceDB::Error) do |error|
        expect([SpiceDB::UnavailableError, SpiceDB::DeadlineExceededError]).to include(error.class)
        expect(error.message).to include('127.0.0.1:1')
        expect(error.server_message).to eq(error.message)
      end
      expect(Process.clock_gettime(Process::CLOCK_MONOTONIC) - started).to be < 3
      client.close
    end

    it 'reports a refused connection as UNAVAILABLE' do
      client = described_class.new_plaintext('127.0.0.1:1', 'token')
      expect { client.connect!(timeout: 2) }.to raise_error(SpiceDB::UnavailableError, /could not connect/) do |error|
        expect(error.code).to eq(GRPC::Core::StatusCodes::UNAVAILABLE)
      end
      client.close
    end

    it 'validates its timeout before connecting' do
      client = described_class.new_plaintext('localhost:50051', 'token')
      expect { client.connect!(timeout: 0) }.to raise_error(SpiceDB::InvalidArgumentError, /got Integer \(0\)/)
      expect(client.instance_variable_get(:@proto_client)).to be_nil
    end

    it 'is a no-op on the test double' do
      require_relative '../lib/spicedb/testing'
      store = SpiceDB::Testing::Store.new
      expect(store.connect!).to be(store)
      expect(store.calls).to be_empty
    end
  end

  describe 'across a fork', if: Process.respond_to?(:fork) do
    # Each scenario runs in a fresh Ruby process: once this spec process has
    # used grpc, no forked child of it can.
    def run_ruby(script)
      lib = File.expand_path('../lib', __dir__)
      out, err, status = Open3.capture3(RbConfig.ruby, '-W0', '-I', lib, '-e', script)
      raise "subprocess failed: #{err}" unless status.success?

      out
    end

    it 'works in a child when the client was built, but not used, before the fork' do
      out = run_ruby(<<~RUBY)
        require 'spicedb'
        client = SpiceDB::Client.new_plaintext('127.0.0.1:1', 'token') # nothing listens on port 1
        reader, writer = IO.pipe
        pid = fork do
          begin
            client.read_schema(timeout: 0.5)
            writer.puts 'connected'
          rescue StandardError => e
            writer.puts e.class.name
          end
          exit!(0)
        end
        Process.wait(pid)
        writer.close
        print reader.read
      RUBY
      # The child opened its own channel: the call reached the transport and
      # failed only because nothing listens, not because of the fork.
      expect(out.strip).to eq('SpiceDB::UnavailableError').or eq('SpiceDB::DeadlineExceededError')
    end

    [
      ['the parent connected a client', "SpiceDB::Client.new_plaintext('127.0.0.1:1', 'token').proto_client"],
      ['the parent used gRPC directly', "GRPC::Core::Channel.new('127.0.0.1:1', {}, :this_channel_is_insecure)"]
    ].each do |label, parent_use|
      it "raises ForkedClientError for a new client built in a child when #{label}" do
        out = run_ruby(<<~RUBY)
          require 'spicedb'
          #{parent_use}
          reader, writer = IO.pipe
          pid = fork do
            fresh = SpiceDB::Client.new_plaintext('127.0.0.1:1', 'token') # built in the child
            results = [-> { fresh.connect!(timeout: 0.5) }, -> { fresh.read_schema(timeout: 0.5) }].map do |call|
              call.call
              'no error'
            rescue SpiceDB::Error => e
              [e.class.name, e.message.include?('does not help'), e.message.include?('on_worker_boot')].join(' ')
            rescue StandardError => e
              "untyped \#{e.class}"
            end
            writer.puts results.join(',')
            exit!(0)
          end
          Process.wait(pid)
          writer.close
          print reader.read
        RUBY
        expect(out.strip).to eq('SpiceDB::ForkedClientError true true,SpiceDB::ForkedClientError true true')
      end
    end

    it 'raises ForkedClientError in a child when the parent had already connected' do
      out = run_ruby(<<~RUBY)
        require 'spicedb'
        client = SpiceDB::Client.new_plaintext('127.0.0.1:1', 'token')
        client.proto_client # connects in the parent
        reader, writer = IO.pipe
        pid = fork do
          begin
            client.read_schema(timeout: 0.5)
          rescue SpiceDB::Error => e
            writer.puts [e.class.name, e.code, e.connected_pid == Process.ppid, e.message.include?('on_worker_boot')].join(' ')
          end
          exit!(0)
        end
        Process.wait(pid)
        writer.close
        print reader.read
      RUBY
      expect(out.strip).to eq("SpiceDB::ForkedClientError #{GRPC::Core::StatusCodes::FAILED_PRECONDITION} true true")
    end
  end
end
