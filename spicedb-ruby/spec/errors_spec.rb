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
    it 'includes RESOURCE_EXHAUSTED, ABORTED, and UNAVAILABLE' do
      expect(SpiceDB::TRANSIENT_CODES).to include(8)  # RESOURCE_EXHAUSTED
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

    it 'returns true for resource exhausted errors' do
      expect(SpiceDB.transient?(SpiceDB::ResourceExhaustedError.new)).to be true
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
  end
end
