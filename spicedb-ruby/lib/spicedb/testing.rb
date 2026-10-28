# frozen_string_literal: true

require_relative '../spicedb'

module SpiceDB
  # Test support. Load it explicitly: `require 'spicedb/testing'`.
  module Testing
    # An in-memory stand-in for the relationship and check operations of
    # SpiceDB::Client. Its methods take the same arguments and return the same
    # native values as the client's #write, #read_relationships,
    # #delete_relationships, #check and #check_bulk, so code written against
    # those methods accepts either.
    #
    # It applies SpiceDB's relationship semantics -- Filter#matches?,
    # preconditions, CREATE conflicts and atomic transactions -- and raises
    # the same error classes (FailedPreconditionError, AlreadyExistsError). It
    # evaluates no schema: checks return results registered with
    # #set_check_result, and anything unregistered is :no_permission.
    # Consistency, timeouts and call options are accepted and ignored. It is
    # safe for concurrent use.
    # Unused keyword arguments keep the real client's exact signatures.
    # rubocop:disable-next Lint/UnusedMethodArgument
    class Store
      def initialize(relationships = [])
        @mutex = Mutex.new
        @relationships = {}
        @checks = {}
        @revision = 0
        relationships.each { |relationship| @relationships[key(relationship)] = relationship }
      end

      # @return [Array<SpiceDB::Relationship>] the stored relationships, in insertion order
      def relationships = @mutex.synchronize { @relationships.values.freeze }

      # Registers the result #check returns for +check+ (a PermissionCheck;
      # its context is ignored). +result+ is a CheckResult or a permissionship
      # symbol such as :has_permission.
      def set_check_result(check, result)
        result = CheckResult.new(permissionship: result, missing_context: [], checked_at: nil) if result.is_a?(Symbol)
        raise InvalidArgumentError, 'set_check_result: expected CheckResult or permissionship Symbol' unless result.is_a?(CheckResult)

        @mutex.synchronize { @checks[check_key(check)] = result }
        self
      end

      def write(transaction, timeout: nil, metadata: nil, call_options: nil)
        @mutex.synchronize do
          transaction.preconditions.each { |precondition| check_precondition(precondition) }
          staged = @relationships.dup
          transaction.updates.each do |update|
            identity = key(update.relationship)
            case update.operation
            when :create
              raise AlreadyExistsError, "relationship already exists: #{update.relationship}" if staged.key?(identity)

              staged[identity] = update.relationship
            when :touch then staged[identity] = update.relationship
            when :delete then staged.delete(identity)
            end
          end
          @relationships = staged
          WriteResult.new(revision: next_revision)
        end
      end

      def read_relationships(filter, consistency:, page_size: 1000, limit: nil, timeout: nil, cursor: nil, call_options: nil, &)
        raise InvalidArgumentError, 'expected Filter' unless filter.is_a?(Filter)

        items = @mutex.synchronize do
          token = current_revision
          @relationships.values.select { |relationship| filter.matches?(relationship) }.each_with_index.map do |relationship, index|
            ReadRelationship.new(relationship: relationship, cursor: (index + 1).to_s, read_at: token)
          end
        end
        items = items.drop(cursor.to_i) if cursor
        items = items.first(limit) if limit
        return items.each(&) if block_given?

        items.freeze
      end

      def delete_relationships(filter, limit: nil, partial: DeletePartial::ALLOWED, must_match: [], must_not_match: [], timeout: nil,
                               cursor: nil, call_options: nil)
        DeletePartial.validate(partial)
        @mutex.synchronize do
          must_match.each { |item| check_precondition(Precondition.new(operation: :must_match, filter: item)) }
          must_not_match.each { |item| check_precondition(Precondition.new(operation: :must_not_match, filter: item)) }
          matched = @relationships.select { |_identity, relationship| filter.matches?(relationship) }.keys
          if limit && matched.length > limit && partial == DeletePartial::DISALLOWED
            raise FailedPreconditionError, "delete matched #{matched.length} relationships, more than the limit of #{limit}"
          end

          deleted = limit ? matched.first(limit) : matched
          deleted.each { |identity| @relationships.delete(identity) }
          DeleteResult.new(revision: next_revision, deleted_count: deleted.length,
                           progress: deleted.length < matched.length ? :partial : :complete, cursor: nil)
        end
      end

      def check(check, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, call_options: nil)
        raise InvalidArgumentError, 'check: expected PermissionCheck' unless check.is_a?(PermissionCheck)

        @mutex.synchronize { lookup_check(check) }
      end

      def check_bulk(checks, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, item_errors: BulkItemErrors::RAISED,
                     call_options: nil)
        BulkItemErrors.validate(item_errors)
        checks.to_a.map { |item| check(item, consistency: consistency) }.freeze
      end

      private

      def key(relationship)
        [relationship.resource.object_type, relationship.resource.object_id, relationship.relation,
         relationship.subject.object.object_type, relationship.subject.object.object_id, relationship.subject.relation]
      end

      def check_key(check) = [check.resource, check.permission, check.subject]

      def lookup_check(check)
        @checks.fetch(check_key(check)) { CheckResult.new(permissionship: :no_permission, missing_context: [], checked_at: current_revision) }
      end

      def check_precondition(precondition)
        found = @relationships.each_value.any? { |relationship| precondition.filter.matches?(relationship) }
        return if found == (precondition.operation == :must_match)

        raise FailedPreconditionError, "unable to satisfy write precondition #{precondition}"
      end

      def next_revision
        @revision += 1
        current_revision
      end

      def current_revision = ZedToken.new(@revision.to_s)
    end
  end
end
