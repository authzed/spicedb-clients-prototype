# frozen_string_literal: true

module SpiceDB
  # Consistency strategies for SpiceDB read operations.
  #
  # Every read operation on the SpiceDB client requires an explicit consistency
  # strategy. This prevents silent defaults and makes consistency choices visible.
  #
  # @example
  #   cs = SpiceDB::Consistency.full
  #   cs = SpiceDB::Consistency.at_least(revision)
  module Consistency
    # Strategy wraps a consistency requirement for read operations.
    Strategy = Data.define(:type, :revision) do
      # Returns the proto-compatible consistency hash for this strategy.
      # @return [Hash] consistency configuration for the proto client
      def to_proto
        case type
        when :full
          { fully_consistent: true }
        when :min_latency
          { minimize_latency: true }
        when :at_least
          { at_least_as_fresh: { token: revision } }
        when :snapshot
          { at_exact_snapshot: { token: revision } }
        end
      end
    end

    module_function

    # Returns a strategy that requires full consistency. This is the least
    # performant option but guarantees the most up-to-date results.
    # @return [Strategy]
    def full
      Strategy.new(type: :full, revision: nil)
    end

    # Returns a strategy that uses SpiceDB's preferred revision for optimal
    # performance. This is the recommended default for most read operations.
    # @return [Strategy]
    def min_latency
      Strategy.new(type: :min_latency, revision: nil)
    end

    # Returns a strategy that ensures results are at least as fresh as the
    # given revision. Use this for read-after-write consistency.
    # @param revision [String] a ZedToken revision string
    # @return [Strategy]
    def at_least(revision)
      Strategy.new(type: :at_least, revision: revision)
    end

    # Returns a strategy that reads at the exact given revision.
    # @param revision [String] a ZedToken revision string
    # @return [Strategy]
    def snapshot(revision)
      Strategy.new(type: :snapshot, revision: revision)
    end

    # Returns AtLeast(revision) if revision is non-nil and non-empty,
    # otherwise Full. Use this when you have an optional revision from a
    # previous write and want the safest fallback.
    # @param revision [String, nil] an optional ZedToken revision string
    # @return [Strategy]
    def at_least_or_full(revision)
      if revision.nil? || revision.empty?
        full
      else
        at_least(revision)
      end
    end

    # Returns AtLeast(revision) if revision is non-nil and non-empty,
    # otherwise MinLatency. Use this when you have an optional revision but
    # prefer performance over full consistency as the fallback.
    # @param revision [String, nil] an optional ZedToken revision string
    # @return [Strategy]
    def at_least_or_min_latency(revision)
      if revision.nil? || revision.empty?
        min_latency
      else
        at_least(revision)
      end
    end
  end
end
