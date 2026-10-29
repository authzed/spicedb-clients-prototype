# frozen_string_literal: true

require_relative '../spec_helper'
require 'open3'
require 'rbconfig'

# One SpiceDB::Client per process, shared by every thread; built before a
# fork (a Rails initializer under Puma's preload_app!) it still works in each
# worker, because it opens its connection on first use. See README.md,
# "Concurrency and forking".
RSpec.describe 'Concurrency and forking' do
  it 'serves many threads from one shared client' do
    owners = (0...40).map { |index| SpiceDB::Relationship.from_tuple("document:shared#{index}#owner@user:#{index.even? ? 'alice' : 'bob'}") }
    written = client.write(owners.each_with_object(SpiceDB::Transaction.new) { |relationship, txn| txn.touch(relationship) })
    consistency = SpiceDB::Consistency.at_least(written.revision)
    alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: :user, object_id: 'alice'))

    results = Array.new(8) do |thread|
      Thread.new do
        Array.new(40) do |index|
          id = (thread + index) % 40
          check = SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: :document, object_id: "shared#{id}"),
                                               permission: :delete, subject: alice)
          [id, client.check(check, consistency: consistency).has_permission?]
        end
      end
    end.flat_map(&:value)

    expect(results.length).to eq(320)
    expect(results).to all(satisfy { |id, granted| granted == id.even? })
  end

  it 'works in a forked worker when built before the fork', if: Process.respond_to?(:fork) do
    # A fresh process: this one has already used grpc, and grpc refuses any
    # use in a child of a process that has.
    script = <<~RUBY
      require 'spicedb'
      client = SpiceDB::Client.new_plaintext(#{SPICEDB_ENDPOINT.inspect}, #{SPICEDB_TOKEN.inspect}) # e.g. a Rails initializer
      reader, writer = IO.pipe
      pid = fork do # e.g. a Puma worker
        client.connect!(timeout: 5) # on_worker_boot: fail at startup if SpiceDB is unreachable
        writer.puts client.read_schema.schema.include?('definition document')
        exit!(0)
      end
      Process.wait(pid)
      writer.close
      print reader.read
    RUBY
    out, err, status = Open3.capture3(RbConfig.ruby, '-W0', '-e', script)
    expect(status.success?).to be(true), err
    expect(out.strip).to eq('true')
  end

  it 'checks readiness at startup' do
    expect(client.connect!(timeout: 5)).to be(client)
    unreachable = SpiceDB::Client.new_plaintext('127.0.0.1:1', SPICEDB_TOKEN)
    expect { unreachable.connect!(timeout: 1) }.to raise_error(SpiceDB::UnavailableError)
    unreachable.close
  end

  it 'explains a client that was already connected before the fork', if: Process.respond_to?(:fork) do
    client.read_schema # connected in this (parent) process
    reader, writer = IO.pipe
    pid = fork do
      begin
        client.read_schema
        writer.puts 'no error'
      rescue SpiceDB::ForkedClientError => e
        writer.puts e.message
      end
      exit!(0)
    end
    Process.wait(pid)
    writer.close
    expect(reader.read).to include('on_worker_boot')
  end
end
