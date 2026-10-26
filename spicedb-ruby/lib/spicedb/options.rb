# frozen_string_literal: true

module SpiceDB
  # Closed, discoverable sets of immutable behavioral choices.
  class Option
    attr_reader :name

    def initialize(name)
      @name = name.freeze
      freeze
    end

    def self.cases(*names)
      @values = names.map { |name| const_set(name, new(name)) }.freeze
      private_class_method :new
    end

    private_class_method :cases

    def self.values = @values

    def self.validate(value)
      raise InvalidArgumentError, "expected #{self}: #{values.map(&:name).join(', ')}" unless values.include?(value)

      value
    end
  end

  class LookupPermissionshipFilter < Option
    cases :ALL, :HAS_PERMISSION_ONLY
  end

  class Tracing < Option
    cases :DISABLED, :ENABLED
  end

  class TransportSecurity < Option
    cases :TLS, :PLAINTEXT
  end

  class RemoteCredentials < Option
    cases :LOOPBACK_ONLY, :ALLOW_PLAINTEXT
  end

  class WatchContent < Option
    cases :RELATIONSHIPS, :CHECKPOINTS, :SCHEMA
  end
end
