# frozen_string_literal: true

require 'grpc'
require 'spicedb_proto'

require_relative '../spec_helper'

# Demonstrates the two error codes a caller actually recovers from -- see root
# DESIGN.md, "RULE: Error mapping must not lose the server's detail".
#
# The rule names both consequences, and this example is those two recoveries
# written out as running code:
#
# - OUT_OF_RANGE is SpiceDB's signal that a ZedToken has expired or been
#   garbage-collected. Recovery is mechanical: discard the stale token and
#   re-read at full consistency. Collapsed into a generic error, every caller
#   would have to string-match a message to recover something the client already
#   knew the shape of.
# - UNAUTHENTICATED is the most common error a new integration produces.
#   Distinguishing it is what lets a caller write "refresh credentials on auth
#   failure, page someone on internal error".
#
# Why this example stands up its own server: neither code is reachable from the
# SpiceDB the integration job starts, which was verified rather than assumed. A
# garbage ZedToken returns INVALID_ARGUMENT, and the in-memory datastore never
# collects the revision (with a 5s GC window and 35s elapsed, a snapshot read at
# the old token still succeeded). And a wrong preshared key comes back
# PERMISSION_DENIED, not UNAUTHENTICATED -- which the last example asserts
# against the real server, so a reader does not write a credential-refresh
# branch that can never run.
STALE_TOKEN = 'stale-zedtoken'

# The stand-in services this example drives, namespaced so the file defines one
# top-level module rather than two loose classes.
module ErrorMappingStandIns
  # A minimal SpiceDB that answers only what this example asks of it.
  class StandInService < Authzed::Api::V1::PermissionsService::Service
    def check_bulk_permissions(request, _call)
      # A read pinned to a token the server no longer has.
      raise GRPC::OutOfRange, 'the specified revision has expired or been garbage collected' if request.consistency&.at_least_as_fresh&.token == STALE_TOKEN

      # Anything else: re-reading at full consistency succeeds. That is the whole
      # point of the recovery -- dropping the stale token is sufficient.
      Authzed::Api::V1::CheckBulkPermissionsResponse.new(
        pairs: request.items.map do
          Authzed::Api::V1::CheckBulkPermissionsPair.new(
            item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(permissionship: 2)
          )
        end
      )
    end
  end

  class RotatedTokenService < Authzed::Api::V1::PermissionsService::Service
    def check_bulk_permissions(_request, _call)
      raise GRPC::Unauthenticated, 'invalid token'
    end
  end
end

RSpec.describe 'Error mapping' do
  def with_stand_in(service_class, &block)
    server = GRPC::RpcServer.new(pool_size: 4)
    port = server.add_http2_port('127.0.0.1:0', :this_port_is_insecure)
    server.handle(service_class)
    thread = Thread.new { server.run }
    server.wait_till_running
    begin
      SpiceDB::Client.new_plaintext("127.0.0.1:#{port}", 'some-token', &block)
    ensure
      server.stop
      thread.join(5)
    end
  end

  it 'recovers from a stale ZedToken without parsing a message', :no_spicedb do
    with_stand_in(ErrorMappingStandIns::StandInService) do |c|
      rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')

      expect { c.check_permission(SpiceDB::Consistency.at_least(STALE_TOKEN), 'view', rel) }
        .to raise_error(SpiceDB::OutOfRangeError)

      # The recovery the rule calls mechanical, in full: drop the token, re-read
      # at full consistency. Nothing here parses a message.
      result = c.check_permission(SpiceDB::Consistency.full, 'view', rel)
      expect(result.has_permission?).to be(true)
    end
  end

  it 'reports a rotated token as distinct from a transport fault', :no_spicedb do
    with_stand_in(ErrorMappingStandIns::RotatedTokenService) do |c|
      rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
      error = nil
      begin
        c.check_permission(SpiceDB::Consistency.full, 'view', rel)
      rescue StandardError => e
        error = e
      end

      expect(error).to be_a(SpiceDB::UnauthenticatedError)
      # Asserting the negative is the half that would silently rot if every code
      # collapsed into one class.
      expect(error).not_to be_a(SpiceDB::UnavailableError)
    end
  end

  it 'gets PERMISSION_DENIED, not UNAUTHENTICATED, from a real SpiceDB with a bad key' do
    # Recorded because it is the case a reader will actually hit first, and
    # assuming otherwise is how a credential-refresh branch ends up unreachable.
    SpiceDB::Client.new_plaintext(SPICEDB_ENDPOINT, 'definitely-the-wrong-key') do |bad|
      expect { bad.read_schema }.to raise_error(SpiceDB::PermissionDeniedError)
    end
  end
end
