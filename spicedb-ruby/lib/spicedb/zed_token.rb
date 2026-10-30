# frozen_string_literal: true

module SpiceDB
  # An opaque revision. Persist it with +to_s+ and rebuild it with
  # +ZedToken.new(stored)+.
  #
  # A caller-supplied token must be a nonempty String: an empty or
  # whitespace-only one (usually an unset variable or column) raises
  # LocalValidationError. Any other content, surrounding whitespace included,
  # is kept as is.
  class ZedToken
    # @param value [String] the token, as returned by +to_s+
    # @raise [LocalValidationError] if +value+ is not a String, or is empty
    #   or whitespace-only
    def initialize(value)
      raise LocalValidationError, 'ZedToken: expected a string' unless value.is_a?(String)
      raise LocalValidationError, 'ZedToken: must not be empty or whitespace' if value.strip.empty?

      @value = value.dup.freeze
      freeze
    end

    def to_s = @value
    def ==(other) = other.is_a?(ZedToken) && to_s == other.to_s
    alias eql? ==
    def hash = [ZedToken, @value].hash

    def self.validate(value)
      raise LocalValidationError, 'revision: expected ZedToken' unless value.is_a?(self)

      value
    end

    # A token from a server response, kept losslessly as sent: the
    # caller-input check above does not apply to server data.
    def self.from_proto(value) = value && wire(value.token)

    def self.wire(token)
      allocate.tap do |zed|
        zed.instance_variable_set(:@value, token.dup.freeze)
        zed.freeze
      end
    end
    private_class_method :wire
  end
end
