# frozen_string_literal: true

require 'json'

module SpiceDB
  # One relationship change: +operation+ is :create, :touch or :delete (watch
  # events may also report :unspecified for an operation this client does not
  # know).
  Update = Values.record(:operation, :relationship) do
    # e.g. "TOUCH(document:readme#viewer@user:alice)"
    def to_s = "#{operation.to_s.upcase}(#{relationship})"

    def as_json(*) = { 'operation' => operation.to_s, 'relationship' => relationship.as_json }
    def to_json(*) = as_json.to_json(*)
  end

  # A write precondition: +operation+ is :must_match or :must_not_match.
  Precondition = Data.define(:operation, :filter) do
    def initialize(operation:, filter:)
      raise InvalidArgumentError, 'precondition: operation must be :must_match or :must_not_match' unless %i[must_match must_not_match].include?(operation)
      raise InvalidArgumentError, 'precondition: expected Filter' unless filter.is_a?(Filter)

      super
    end

    # e.g. "MUST_NOT_MATCH(document:*#viewer@*)"
    def to_s = "#{operation.to_s.upcase}(#{filter})"

    def as_json(*) = { 'operation' => operation.to_s, 'filter' => filter.as_json }
    def to_json(*) = as_json.to_json(*)
  end

  # A mutable transaction builder for batching relationship writes.
  #
  # Collects create, touch, and delete operations along with optional
  # preconditions, then submits them as a single atomic write. +updates+ and
  # +preconditions+ return frozen snapshots of immutable values.
  #
  # @example
  #   txn = SpiceDB::Transaction.new
  #   txn.create(relationship)
  #   txn.touch(other_relationship)
  #   txn.must_not_match(filter)
  #   revision = client.write(txn).revision
  class Transaction
    def initialize
      @updates = []
      @preconditions = []
    end

    # @return [Array<SpiceDB::Update>] the relationship updates, in order
    def updates = @updates.dup.freeze

    # @return [Array<SpiceDB::Precondition>] the preconditions, in order
    def preconditions = @preconditions.dup.freeze

    # Adds a relationship create. Fails at write time if it already exists.
    # @return [self]
    def create(relationship) = add(:create, relationship)

    # Adds a relationship touch (create or update).
    # @return [self]
    def touch(relationship) = add(:touch, relationship)

    # Adds a relationship delete.
    # @return [self]
    def delete(relationship) = add(:delete, relationship)

    # Adds a precondition that no relationships match the given filter.
    # @return [self]
    def must_not_match(filter)
      @preconditions << Precondition.new(operation: :must_not_match, filter: filter)
      self
    end

    # Adds a precondition that at least one relationship matches the given filter.
    # @return [self]
    def must_match(filter)
      @preconditions << Precondition.new(operation: :must_match, filter: filter)
      self
    end

    # @return [Boolean] true if this transaction has no updates
    def empty? = @updates.empty?

    # Whether replaying the transaction is safe: it contains only touches and
    # no preconditions. This is the rule the client uses to decide whether a
    # write may be retried (per call or by +operation_retry_policies:+).
    # @return [Boolean]
    def idempotent? = @preconditions.empty? && @updates.all? { |update| update.operation == :touch }

    # @return [Integer] the number of updates, without copying them
    def length = @updates.length
    alias size length

    def to_s = "Transaction(#{(@preconditions + @updates).join(', ')})"
    alias inspect to_s

    def as_json(*) = { 'preconditions' => @preconditions.map(&:as_json), 'updates' => @updates.map(&:as_json) }
    def to_json(*) = as_json.to_json(*)

    private

    def add(operation, relationship)
      relationship = relationship.relationship if relationship.is_a?(ReadRelationship) || relationship.is_a?(ExportedRelationship)
      raise InvalidArgumentError, "#{operation}: expected Relationship" unless relationship.is_a?(Relationship)

      @updates << Update.new(operation: operation, relationship: relationship)
      self
    end
  end
end
