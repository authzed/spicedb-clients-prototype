# frozen_string_literal: true

require_relative '../lib/spicedb'

RSpec.describe 'SpiceDB::Errors' do
  describe 'exception hierarchy' do
    it 'all errors inherit from SpiceDB::Error' do
      expect(SpiceDB::PermissionDeniedError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::NotFoundError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::AlreadyExistsError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::InvalidArgumentError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::FailedPreconditionError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::UnavailableError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::CancelledError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::DeadlineExceededError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::ResourceExhaustedError.superclass).to eq(SpiceDB::Error)
    end

    it 'SpiceDB::Error inherits from StandardError' do
      expect(SpiceDB::Error.superclass).to eq(StandardError)
    end

    it 'errors can be raised and rescued' do
      expect do
        raise SpiceDB::PermissionDeniedError, 'access denied'
      end.to raise_error(SpiceDB::Error, 'access denied')
    end

    it 'specific errors can be rescued individually' do
      expect do
        raise SpiceDB::NotFoundError, 'not found'
      end.to raise_error(SpiceDB::NotFoundError, 'not found')
    end
  end

  describe 'GRPC_CODE_TO_ERROR' do
    it 'maps gRPC status codes to error classes' do
      expect(SpiceDB::GRPC_CODE_TO_ERROR[1]).to eq(SpiceDB::CancelledError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[3]).to eq(SpiceDB::InvalidArgumentError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[4]).to eq(SpiceDB::DeadlineExceededError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[5]).to eq(SpiceDB::NotFoundError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[6]).to eq(SpiceDB::AlreadyExistsError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[7]).to eq(SpiceDB::PermissionDeniedError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[8]).to eq(SpiceDB::ResourceExhaustedError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[9]).to eq(SpiceDB::FailedPreconditionError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[14]).to eq(SpiceDB::UnavailableError)
    end

    it 'is frozen' do
      expect(SpiceDB::GRPC_CODE_TO_ERROR).to be_frozen
    end
  end

  describe 'TRANSIENT_CODES' do
    it 'includes ABORTED and UNAVAILABLE, but NOT RESOURCE_EXHAUSTED' do
      # RESOURCE_EXHAUSTED must NOT be retried -- inverted from an earlier
      # assertion that it was included. See DESIGN.md, "Automatic retry is
      # for idempotent operations only".
      expect(SpiceDB::TRANSIENT_CODES).not_to include(8) # RESOURCE_EXHAUSTED
      expect(SpiceDB::TRANSIENT_CODES).to include(10) # ABORTED
      expect(SpiceDB::TRANSIENT_CODES).to include(14) # UNAVAILABLE
    end
  end

  describe '.to_spicedb_error' do
    it 'converts a gRPC-like error to a typed exception' do
      grpc_err = double('grpc_error', code: 7, details: 'permission denied')
      err = SpiceDB.to_spicedb_error(grpc_err)
      expect(err).to be_a(SpiceDB::PermissionDeniedError)
      expect(err.message).to eq('permission denied')
    end

    it 'falls back to SpiceDB::Error for unknown codes' do
      grpc_err = double('grpc_error', code: 99, details: 'unknown error')
      err = SpiceDB.to_spicedb_error(grpc_err)
      expect(err).to be_a(SpiceDB::Error)
      expect(err.message).to eq('unknown error')
    end

    it 'handles errors without code method' do
      plain_err = StandardError.new('plain error')
      err = SpiceDB.to_spicedb_error(plain_err)
      expect(err).to be_a(SpiceDB::Error)
      expect(err.message).to eq('plain error')
    end

    it 'falls back to .message when .details responds but is not a usable string ' \
       '(e.g. google.rpc.Status, whose #details is a repeated Any field, not the message)' do
      rich_status = double('rich_status', code: 3, details: [], message: 'invalid argument: bad caveat context')
      err = SpiceDB.to_spicedb_error(rich_status)
      expect(err).to be_a(SpiceDB::InvalidArgumentError)
      expect(err.message).to eq('invalid argument: bad caveat context')
    end
  end

  describe '.transient?' do
    it 'returns true for unavailable errors' do
      expect(SpiceDB.transient?(SpiceDB::UnavailableError.new)).to be true
    end

    it 'returns false for resource exhausted errors' do
      # Inverted from "returns true" -- RESOURCE_EXHAUSTED must NOT be
      # retried. In SpiceDB it signals memory load-shed or a deterministic
      # MaxDepthExceeded, never a transient hiccup.
      expect(SpiceDB.transient?(SpiceDB::ResourceExhaustedError.new)).to be false
    end

    it 'returns false for gRPC-like errors with RESOURCE_EXHAUSTED code' do
      grpc_err = double('grpc_error', code: 8)
      expect(SpiceDB.transient?(grpc_err)).to be false
    end

    it 'returns false for permission denied errors' do
      expect(SpiceDB.transient?(SpiceDB::PermissionDeniedError.new)).to be false
    end

    it 'returns true for gRPC-like errors with transient codes' do
      grpc_err = double('grpc_error', code: 14)
      expect(SpiceDB.transient?(grpc_err)).to be true
    end

    it 'returns false for gRPC-like errors with non-transient codes' do
      grpc_err = double('grpc_error', code: 7)
      expect(SpiceDB.transient?(grpc_err)).to be false
    end

    it 'returns false for the newly mapped codes' do
      expect(SpiceDB.transient?(double('grpc_error', code: 11))).to be false # OUT_OF_RANGE
      expect(SpiceDB.transient?(double('grpc_error', code: 16))).to be false # UNAUTHENTICATED
    end
  end

  describe 'newly mapped codes' do
    it 'maps OUT_OF_RANGE to its own type' do
      # OUT_OF_RANGE is SpiceDB's code for an expired or garbage-collected
      # ZedToken. Recovery is mechanical -- drop the token, re-read at full
      # consistency -- so it must be distinguishable by type rather than by
      # message. See root DESIGN.md, "RULE: Error mapping must not lose the
      # server's detail".
      err = SpiceDB.to_spicedb_error(double('grpc_error', code: 11, details: 'revision no longer available'))
      expect(err).to be_a(SpiceDB::OutOfRangeError)
      expect(err).not_to be_a(SpiceDB::InvalidArgumentError)
      expect(err.message).to eq('revision no longer available')
    end

    it 'maps UNAUTHENTICATED to its own type' do
      # A wrong, expired, or rotated token must be distinguishable from an
      # internal server fault, so a caller can refresh credentials on one and
      # page someone on the other.
      err = SpiceDB.to_spicedb_error(double('grpc_error', code: 16, details: 'bad token'))
      expect(err).to be_a(SpiceDB::UnauthenticatedError)
      expect(err).not_to be_a(SpiceDB::PermissionDeniedError)
      expect(err.instance_of?(SpiceDB::Error)).to be false
    end

    it 'puts both new types in the hierarchy' do
      expect(SpiceDB::OutOfRangeError.superclass).to eq(SpiceDB::Error)
      expect(SpiceDB::UnauthenticatedError.superclass).to eq(SpiceDB::Error)
    end

    it 'maps both codes in GRPC_CODE_TO_ERROR' do
      expect(SpiceDB::GRPC_CODE_TO_ERROR[11]).to eq(SpiceDB::OutOfRangeError)
      expect(SpiceDB::GRPC_CODE_TO_ERROR[16]).to eq(SpiceDB::UnauthenticatedError)
    end
  end

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
        Authzed::Api::V1::ErrorReason.descriptor.lookup_value(19).to_s
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

    it 'is empty for a locally constructed error' do
      expect(SpiceDB::Error.new('local problem').reason).to eq('')
      expect(SpiceDB::Error.new('local problem').reason_metadata).to eq({})
    end
  end
end
