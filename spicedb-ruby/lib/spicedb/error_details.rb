# frozen_string_literal: true

require 'grpc'
require 'grpc/google_rpc_status_utils'
require 'google/rpc/error_details_pb'

module SpiceDB
  # Reads SpiceDB's structured explanation of a failure -- the
  # `google.rpc.ErrorInfo` detail attached to a status -- off whichever shape
  # the error arrived in: a `GRPC::BadStatus` (whole-call failure, details in
  # the `grpc-status-details-bin` trailer) or a `Google::Rpc::Status` (per-item
  # bulk failure, details on the message itself).
  #
  # Extracted from errors.rb rather than added to it, so the error hierarchy
  # stays the small, readable thing it is. See root DESIGN.md, "RULE: Error
  # mapping must not lose the server's detail".
  module ErrorDetails
    module_function

    # Returns keyword arguments for {SpiceDB::Error#initialize} describing the
    # server's reason, or an empty Hash when the server attached none.
    #
    # @param err [GRPC::BadStatus, Google::Rpc::Status, Object]
    # @return [Hash]
    def reason_kwargs(err)
      info = error_info(rich_status(err))
      return {} if info.nil?

      {
        reason: info.reason,
        reason_domain: info.domain,
        reason_metadata: info.metadata.to_h
      }
    end

    # Returns the `Google::Rpc::Status` behind `err`, or nil.
    #
    # The trailer lookup and decode are grpc's own
    # (`GoogleRpcStatusUtils.extract_google_rpc_status`), not a hand-rolled
    # read of `grpc-status-details-bin`. A trailer that will not decode yields
    # nil rather than propagating: the code-to-class mapping is the
    # load-bearing part of the conversion and must not be lost because an
    # optional detail was malformed.
    def rich_status(err)
      # `Google::Rpc::Status` is loaded lazily -- by the proto client, or by
      # `extract_google_rpc_status` below the first time a rich trailer shows
      # up -- so the constant is checked rather than assumed. If nothing has
      # loaded it, no caller can be holding one either.
      return err if defined?(Google::Rpc::Status) && err.is_a?(Google::Rpc::Status)
      return nil unless err.respond_to?(:to_status)

      GRPC::GoogleRpcStatusUtils.extract_google_rpc_status(err.to_status)
    rescue StandardError
      nil
    end

    # Returns the `Google::Rpc::ErrorInfo` detail on `status`, or nil.
    #
    # `Any#unpack` returns nil for a detail of another type, so an unfamiliar
    # detail never hides the familiar one.
    def error_info(status)
      return nil if status.nil?

      status.details.each do |detail|
        info = begin
          detail.unpack(Google::Rpc::ErrorInfo)
        rescue StandardError
          nil
        end
        return info unless info.nil?
      end
      nil
    end
  end
end
