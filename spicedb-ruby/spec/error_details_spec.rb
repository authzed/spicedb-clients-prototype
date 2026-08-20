# frozen_string_literal: true

require_relative '../lib/spicedb'
# These tests build a Google::Rpc::Status directly, so this spec needs the proto
# types loaded rather than relying on a sibling spec having required them first
# -- `rspec spec/error_details_spec.rb` on its own must pass. (The library
# itself does not need this: grpc lazily requires google/rpc/status_pb the first
# time a rich status trailer shows up.)
require 'spicedb_proto'
require 'google/protobuf/well_known_types'

RSpec.describe SpiceDB::ErrorDetails do
  # SpiceDB's structured explanation of a failure -- the google.rpc.ErrorInfo
  # detail on the status -- must survive the mapping into a typed exception, so
  # a caller can branch on the reason and read its metadata instead of
  # string-matching a message. See root DESIGN.md, "RULE: Error mapping must not
  # lose the server's detail".
  describe 'ErrorReason is reachable' do
    def status_with_error_info(code, message, reason, metadata, domain = 'authzed.com')
      Google::Rpc::Status.new(
        code: code,
        message: message,
        details: [
          Google::Protobuf::Any.pack(
            Google::Rpc::ErrorInfo.new(reason: reason, domain: domain, metadata: metadata)
          )
        ]
      )
    end

    it 'surfaces the reason, domain, and metadata from a rich status' do
      status = status_with_error_info(
        8, 'max depth exceeded', 'ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED', { 'maximum_depth_allowed' => '50' }
      )
      err = SpiceDB.to_spicedb_error(status)

      expect(err).to be_a(SpiceDB::ResourceExhaustedError)
      # The exposed reason is exactly the authzed.api.v1.ErrorReason enum name,
      # so a caller can compare against the generated enum without this client
      # carrying a hand-maintained copy of it.
      expect(err.reason).to eq('ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED')
      expect(err.reason).to eq(
        Authzed::Api::V1::ErrorReason
          .lookup(Authzed::Api::V1::ErrorReason::ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED).to_s
      )
      expect(err.reason_domain).to eq('authzed.com')
      expect(err.reason_metadata).to eq({ 'maximum_depth_allowed' => '50' })
    end

    it 'keeps the metadata naming which precondition failed' do
      status = status_with_error_info(
        9, 'precondition failed', 'ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE',
        { 'precondition_resource_id' => 'firstdoc', 'precondition_relation' => 'viewer' }
      )
      err = SpiceDB.to_spicedb_error(status)

      expect(err).to be_a(SpiceDB::FailedPreconditionError)
      expect(err.reason).to eq('ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE')
      expect(err.reason_metadata['precondition_resource_id']).to eq('firstdoc')
      expect(err.reason_metadata['precondition_relation']).to eq('viewer')
    end

    it 'reads the reason out of a GRPC::BadStatus trailer' do
      status = status_with_error_info(
        9, 'precondition failed', 'ERROR_REASON_EMPTY_PRECONDITION', { 'k' => 'v' }
      )
      # 'grpc-status-details-bin' is the wire location the gRPC spec fixes for
      # a rich status; grpc's own GoogleRpcStatusUtils is what reads it back.
      bad_status = GRPC::FailedPrecondition.new(
        'precondition failed',
        { 'grpc-status-details-bin' => Google::Rpc::Status.encode(status) }
      )
      err = SpiceDB.to_spicedb_error(bad_status)

      expect(err).to be_a(SpiceDB::FailedPreconditionError)
      expect(err.reason).to eq('ERROR_REASON_EMPTY_PRECONDITION')
      expect(err.reason_metadata).to eq({ 'k' => 'v' })
    end

    it 'passes an unrecognized reason through without raising' do
      # A reason a newer server knows and this client does not is
      # server-supplied: root DESIGN.md's "RULE: A conversion that cannot
      # preserve meaning must fail" requires it to degrade safely, not to raise.
      status = status_with_error_info(
        3, 'from the future', 'ERROR_REASON_INVENTED_BY_A_NEWER_SERVER', { 'k' => 'v' }
      )
      err = SpiceDB.to_spicedb_error(status)

      expect(err).to be_a(SpiceDB::InvalidArgumentError)
      expect(err.reason).to eq('ERROR_REASON_INVENTED_BY_A_NEWER_SERVER')
      expect(err.reason_metadata).to eq({ 'k' => 'v' })
    end

    it 'leaves the reason empty when the server attached no ErrorInfo' do
      err = SpiceDB.to_spicedb_error(double('grpc_error', code: 5, details: 'nope'))
      expect(err.reason).to eq('')
      expect(err.reason_domain).to eq('')
      expect(err.reason_metadata).to eq({})
    end

    it 'does not lose the code mapping when the trailer will not decode' do
      bad_status = GRPC::NotFound.new('nope', { 'grpc-status-details-bin' => "\xFF\xFF\xFF\xFF".b })
      err = SpiceDB.to_spicedb_error(bad_status)
      expect(err).to be_a(SpiceDB::NotFoundError)
      expect(err.reason).to eq('')
    end

    it 'copies the metadata rather than aliasing the caller\'s Hash' do
      supplied = { 'k' => 'v' }
      err = SpiceDB::Error.new('boom', reason_metadata: supplied)

      supplied['k'] = 'mutated'
      expect(err.reason_metadata).to eq({ 'k' => 'v' })
      expect { err.reason_metadata['k'] = 'x' }.to raise_error(FrozenError)
      expect(supplied).to eq({ 'k' => 'mutated' })
    end

    it 'is empty for a locally constructed error' do
      expect(SpiceDB::Error.new('local problem').reason).to eq('')
      expect(SpiceDB::Error.new('local problem').reason_metadata).to eq({})
    end
  end
end
