# frozen_string_literal: true

require 'grpc'
require 'spicedb_proto'
require 'google/protobuf/well_known_types'

require_relative '../spec_helper'

# Demonstrates `with_debug:`, which sets the new `with_debug` field on
# `LookupResourcesRequest`. As of this client's proto version, SpiceDB
# populates debug information only for a MaxDepthExceeded failure, attaching a
# `DebugInformation` to the failed call's error details -- there is no
# successful-response payload to attach it to, since the call errored.
#
# That payload does not get a dedicated client-native field. Root DESIGN.md's
# "RULE: Error mapping must not lose the server's detail" is already satisfied
# generically, because the `GRPC::BadStatus` underlying every mapped
# `SpiceDB::Error` survives as `cause`. This example proves two things: that
# `with_debug:` controls whether the server bothers attaching the detail at
# all, and the intended access path for reading it once attached --
# `SpiceDB::ErrorDetails.rich_status(error.cause).details`, unpacked to
# `Authzed::Api::V1::DebugInformation`.
#
# A real SpiceDB cannot be made to hit MaxDepthExceeded on demand without
# standing up dozens of chained schema definitions, so this stands up a
# minimal stand-in that returns the failure this example exists to recover
# from -- the same way examples/error_mapping and examples/retry_policy do for
# codes the real integration SpiceDB does not produce deterministically.
module LookupResourcesDebugStandIn
  # Always fails LookupResources with the code a real MaxDepthExceeded
  # produces, attaching a DebugInformation detail ONLY when the request opted
  # in via with_debug -- exactly how a real SpiceDB behaves, so a caller who
  # didn't ask for debug info doesn't pay for computing it.
  class PermissionsService < Authzed::Api::V1::PermissionsService::Service
    def lookup_resources(request, _call)
      details = 'max recursion depth exceeded'
      metadata = {}
      if request.with_debug
        info = Authzed::Api::V1::DebugInformation.new(schema_used: 'definition user {}')
        any = Google::Protobuf::Any.new
        any.pack(info)
        status = Google::Rpc::Status.new(
          code: GRPC::Core::StatusCodes::RESOURCE_EXHAUSTED,
          message: details,
          details: [any]
        )
        metadata = { 'grpc-status-details-bin' => Google::Rpc::Status.encode(status) }
      end
      raise GRPC::ResourceExhausted.new(details, metadata)
    end
  end
end

RSpec.describe 'LookupResources debug information' do
  def with_stand_in(&block)
    server = GRPC::RpcServer.new(pool_size: 4)
    port = server.add_http2_port('127.0.0.1:0', :this_port_is_insecure)
    server.handle(LookupResourcesDebugStandIn::PermissionsService)
    thread = Thread.new { server.run }
    server.wait_till_running
    begin
      SpiceDB::Client.new_plaintext("127.0.0.1:#{port}", 'some-token', &block)
    ensure
      server.stop
      thread.join(5)
    end
  end

  def debug_information(error)
    rich_status = SpiceDB::ErrorDetails.rich_status(error.cause)
    return nil if rich_status.nil?

    rich_status.details.filter_map { |d| d.unpack(Authzed::Api::V1::DebugInformation) }.first
  end

  it 'attaches no debug detail unless with_debug: true was passed', :no_spicedb do
    with_stand_in do |c|
      error = nil
      begin
        c.lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice').first
      rescue SpiceDB::ResourceExhaustedError => e
        error = e
      end

      expect(error).not_to be_nil
      expect(debug_information(error)).to be_nil,
                                          'did not pass with_debug: true, but a DebugInformation detail came back anyway'
    end
  end

  it 'attaches a DebugInformation detail when with_debug: true is passed', :no_spicedb do
    with_stand_in do |c|
      error = nil
      begin
        c.lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice', with_debug: true).first
      rescue SpiceDB::ResourceExhaustedError => e
        error = e
      end

      expect(error).not_to be_nil
      expect(error.cause).not_to be_nil,
                                 'expected the underlying GRPC::BadStatus to remain reachable through Error#cause'

      info = debug_information(error)
      expect(info).not_to be_nil,
                          'with_debug: true should have caused the server to attach a DebugInformation detail, ' \
                          "but none was found on the mapped error's underlying status"
      expect(info.schema_used).to eq('definition user {}')
    end
  end
end
