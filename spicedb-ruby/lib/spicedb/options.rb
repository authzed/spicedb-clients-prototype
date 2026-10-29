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
      raise LocalValidationError, "expected #{self}: #{values.map(&:name).join(', ')}" unless values.include?(value)

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

  # Whether unary operations without their own +timeout:+ get the client's
  # default budget. DISABLED runs them unbounded.
  class DefaultDeadline < Option
    cases :ENABLED, :DISABLED
  end

  # Whether idempotent reads retry at all. DISABLED is for callers applying
  # their own policy; a per-call +retry_policy:+ still applies to that call.
  # Mutations never retry unless one call opts in (see Client#write).
  class Retries < Option
    cases :ENABLED, :DISABLED
  end

  # Whether a stream may be re-established before it has exposed an item.
  # DISABLED suits long-lived streams such as watch.
  class StreamRetries < Option
    cases :ENABLED, :DISABLED
  end

  # How read_relationships and lookup_resources fetch results. AUTOMATIC (the
  # default) pages until results or the limit run out; SINGLE_REQUEST sends
  # exactly one request, as the raw API does: its limit is +limit:+ (or
  # unbounded without one), +page_size:+ is ignored and no follow-up page is
  # requested. Items still carry their cursors.
  class Paging < Option
    cases :AUTOMATIC, :SINGLE_REQUEST
  end

  # How check_bulk reports a per-item failure: RAISED fails the call with the
  # first item's error; RETURNED puts each item's error on its CheckResult.
  class BulkItemErrors < Option
    cases :RAISED, :RETURNED
  end

  # ALLOWED deletes up to the limit and reports partial progress. DISALLOWED
  # is all-or-nothing: the server rejects a delete exceeding its limit.
  class DeletePartial < Option
    cases :ALLOWED, :DISALLOWED
  end
end
