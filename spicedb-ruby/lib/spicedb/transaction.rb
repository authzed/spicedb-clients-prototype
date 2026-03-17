# frozen_string_literal: true

module SpiceDB
  # A transaction builder for batching relationship writes.
  #
  # Collects create, touch, and delete operations along with optional
  # preconditions, then submits them as a single atomic write.
  #
  # @example
  #   txn = SpiceDB::Transaction.new
  #   txn.create(relationship)
  #   txn.touch(other_relationship)
  #   txn.must_not_match(filter)
  #   revision = client.write(txn)
  class Transaction
    # @return [Array<Hash>] the list of relationship updates
    attr_reader :updates

    # @return [Array<Hash>] the list of preconditions
    attr_reader :preconditions

    def initialize
      @updates = []
      @preconditions = []
    end

    # Adds a relationship create to the transaction. Fails if the
    # relationship already exists.
    #
    # @param relationship [SpiceDB::Relationship]
    # @return [self]
    def create(relationship)
      @updates << { operation: :create, relationship: relationship }
      self
    end

    # Adds a relationship touch to the transaction. Creates or updates
    # the relationship.
    #
    # @param relationship [SpiceDB::Relationship]
    # @return [self]
    def touch(relationship)
      @updates << { operation: :touch, relationship: relationship }
      self
    end

    # Adds a relationship delete to the transaction.
    #
    # @param relationship [SpiceDB::Relationship]
    # @return [self]
    def delete(relationship)
      @updates << { operation: :delete, relationship: relationship }
      self
    end

    # Adds a precondition that no relationships match the given filter.
    #
    # @param filter [SpiceDB::Filter]
    # @return [self]
    def must_not_match(filter)
      @preconditions << { operation: :must_not_match, filter: filter }
      self
    end

    # Adds a precondition that at least one relationship matches the given filter.
    #
    # @param filter [SpiceDB::Filter]
    # @return [self]
    def must_match(filter)
      @preconditions << { operation: :must_match, filter: filter }
      self
    end

    # Returns true if this transaction has no updates.
    #
    # @return [Boolean]
    def empty?
      @updates.empty?
    end
  end
end
