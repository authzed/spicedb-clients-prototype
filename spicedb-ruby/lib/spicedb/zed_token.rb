# frozen_string_literal: true

module SpiceDB
  # An opaque revision value; serialization preserves the exact supplied string.
  class ZedToken
    def initialize(value)
      raise LocalValidationError, 'ZedToken: expected a string' unless value.is_a?(String)

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

    def self.from_proto(value) = value && new(value.token)
  end
end
