# frozen_string_literal: true

require_relative '../spicedb'

module SpiceDB
  # Test support. Load it explicitly: `require 'spicedb/testing'`.
  module Testing
    # An in-memory stand-in for SpiceDB::Client. Its #write,
    # #read_relationships, #delete_relationships, #import_relationships,
    # #export_relationships, #read_schema, #write_schema, #reflect_schema,
    # #check and #check_bulk take the same arguments and return the same
    # native values as the client's, so code written against those methods
    # accepts either.
    #
    # It applies SpiceDB's relationship semantics -- Filter#matches?,
    # preconditions, CREATE conflicts, atomic transactions and imports -- and
    # raises errors with the same classes, codes and message shape as the
    # client: a server failure's +message+ and +server_message+ are the
    # server's message (for example "could not CREATE relationship ..."), and
    # local validation raises InvalidArgumentError without a server message.
    # Empty writes check their preconditions and return the current revision,
    # as SpiceDB does.
    #
    # It evaluates no schema: checks return results registered with
    # #set_check_result (anything unregistered is :no_permission), and
    # #reflect_schema returns the result registered with
    # #set_reflect_schema_result. Read and export cursors and limits, the
    # export filter, and +call_options:+ response metadata (filled from
    # #set_response_metadata) are honored. Consistency, timeouts, paging and
    # retry policies are accepted and otherwise ignored, except that a
    # +retry_policy:+ on a non-idempotent mutation is rejected as the client
    # rejects it. Every option is a plain keyword argument, so a test that
    # needs one can inspect it directly. It is safe for concurrent use.
    # Unused keyword arguments keep the real client's exact signatures.
    # rubocop:disable-next Lint/UnusedMethodArgument
    class Store
      NO_SCHEMA = 'no schema has been defined; please call WriteSchema to start'
      private_constant :NO_SCHEMA

      def initialize(relationships = [])
        @mutex = Mutex.new
        @relationships = {}
        @checks = {}
        @revision = 0
        @schema = nil
        @reflection = nil
        @header = {}
        @trailer = {}
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

      # Registers the result #reflect_schema returns (stamped with the
      # store's current revision). Without one it returns no definitions and
      # no caveats, as SpiceDB does for an empty schema.
      def set_reflect_schema_result(result) # rubocop:disable Naming/AccessorMethodName -- pairs with set_check_result
        raise InvalidArgumentError, 'set_reflect_schema_result: expected ReflectSchemaResult' unless result.is_a?(ReflectSchemaResult)

        @mutex.synchronize { @reflection = result }
        self
      end

      # Sets the headers and trailers every operation records into a
      # +call_options:+ ResponseMetadata.
      def set_response_metadata(header: {}, trailer: {})
        @mutex.synchronize do
          @header = header.to_h.dup.freeze
          @trailer = trailer.to_h.dup.freeze
        end
        self
      end

      def write(transaction, timeout: nil, metadata: nil, call_options: nil, retry_policy: nil)
        raise InvalidArgumentError, 'write: expected Transaction' unless transaction.is_a?(Transaction)

        Retrying.mutation_policy(retry_policy, Retrying.idempotent_write?(transaction), 'write')
        operation(call_options) do
          transaction.preconditions.each { |precondition| check_precondition(precondition) }
          next WriteResult.new(revision: current_revision) if transaction.empty?

          staged = @relationships.dup
          transaction.updates.each do |update|
            identity = key(update.relationship)
            case update.operation
            when :create
              raise recreate_error(update.relationship) if staged.key?(identity)

              staged[identity] = update.relationship
            when :touch then staged[identity] = update.relationship
            when :delete then staged.delete(identity)
            end
          end
          @relationships = staged
          WriteResult.new(revision: next_revision)
        end
      end

      def read_relationships(filter, consistency:, page_size: 1000, limit: nil, timeout: nil, cursor: nil, call_options: nil,
                             retry_policy: nil, paging: Paging::AUTOMATIC, &)
        raise InvalidArgumentError, 'expected Filter' unless filter.is_a?(Filter)

        validate_paging(page_size, limit, retry_policy)
        Paging.validate(paging)
        items = operation(call_options) do
          token = current_revision
          page(cursor, limit) { |relationship| filter.matches?(relationship) }.map do |relationship, after|
            ReadRelationship.new(relationship: relationship, cursor: after, read_at: token)
          end
        end
        return items.each(&) if block_given?

        items.freeze
      end

      def export_relationships(filter = nil, consistency:, page_size: 1000, timeout: nil, limit: nil, cursor: nil, call_options: nil,
                               retry_policy: nil, &)
        raise InvalidArgumentError, 'expected Filter' unless filter.nil? || filter.is_a?(Filter)

        validate_paging(page_size, limit, retry_policy)
        items = operation(call_options) do
          page(cursor, limit) { |relationship| filter.nil? || filter.matches?(relationship) }.map do |relationship, after|
            ExportedRelationship.new(relationship: relationship, cursor: after)
          end
        end
        return items.each(&) if block_given?

        items.freeze
      end

      # Adds every relationship in one transaction, as SpiceDB does: if any
      # already exists (or repeats within the import), nothing is added and
      # AlreadyExistsError is raised. Returns the number imported.
      def import_relationships(relationships, timeout: nil, batch_size: Client::DEFAULT_IMPORT_BATCH_SIZE, call_options: nil, retry_policy: nil)
        Retrying.mutation_policy(retry_policy, false, 'import_relationships')
        positive(batch_size, 'batch_size')
        batch = relationships.to_a
        batch.each { |relationship| raise InvalidArgumentError, 'import: expected Relationship' unless relationship.is_a?(Relationship) }
        operation(call_options) do
          staged = @relationships.dup
          batch.each do |relationship|
            raise recreate_error(relationship) if staged.key?(key(relationship))

            staged[key(relationship)] = relationship
          end
          @relationships = staged
          next_revision
          batch.length
        end
      end

      # Removes up to +limit+ matching relationships. With
      # DeletePartial::DISALLOWED it deletes all or nothing: more matches than
      # +limit+ raise InvalidArgumentError, as SpiceDB does.
      def delete_relationships(filter, limit: nil, partial: DeletePartial::ALLOWED, must_match: [], must_not_match: [], timeout: nil,
                               cursor: nil, call_options: nil, retry_policy: nil)
        Retrying.mutation_policy(retry_policy, false, 'delete_relationships')
        DeletePartial.validate(partial)
        positive(limit, 'limit') unless partial == DeletePartial::DISALLOWED && limit.nil?
        operation(call_options) do
          must_match.each { |item| check_precondition(Precondition.new(operation: :must_match, filter: item)) }
          must_not_match.each { |item| check_precondition(Precondition.new(operation: :must_not_match, filter: item)) }
          matched = @relationships.select { |_identity, relationship| filter.matches?(relationship) }.keys
          if limit && matched.length > limit && partial == DeletePartial::DISALLOWED
            raise server_error(GRPC::Core::StatusCodes::INVALID_ARGUMENT,
                               "found more than #{limit} relationships to be deleted and partial deletion was not requested")
          end

          deleted = limit ? matched.first(limit) : matched
          deleted.each { |identity| @relationships.delete(identity) }
          DeleteResult.new(revision: next_revision, deleted_count: deleted.length,
                           progress: deleted.length < matched.length ? :partial : :complete, cursor: nil)
        end
      end

      # Returns the schema last written; NotFoundError before any, as SpiceDB does.
      def read_schema(timeout: nil, call_options: nil, retry_policy: nil)
        validate_policy(retry_policy)
        operation(call_options) do
          raise server_error(GRPC::Core::StatusCodes::NOT_FOUND, NO_SCHEMA) if @schema.nil?

          SchemaResult.new(schema: @schema, revision: current_revision)
        end
      end

      # Stores the schema text without compiling or validating it.
      def write_schema(schema, timeout: nil, call_options: nil, retry_policy: nil)
        Retrying.mutation_policy(retry_policy, true, 'write_schema')
        raise InvalidArgumentError, 'write_schema: expected String' unless schema.is_a?(String)

        operation(call_options) do
          @schema = schema.dup.freeze
          next_revision
        end
      end

      def reflect_schema(consistency:, filters: [], timeout: nil, call_options: nil, retry_policy: nil)
        validate_policy(retry_policy)
        operation(call_options) do
          reflection = @reflection || ReflectSchemaResult.new(definitions: [], caveats: [], revision: nil)
          reflection.with(revision: current_revision)
        end
      end

      def check(check, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, call_options: nil, retry_policy: nil)
        raise InvalidArgumentError, 'check: expected PermissionCheck' unless check.is_a?(PermissionCheck)

        validate_policy(retry_policy)
        operation(call_options) { lookup_check(check) }
      end

      def check_bulk(checks, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, item_errors: BulkItemErrors::RAISED,
                     call_options: nil, retry_policy: nil)
        BulkItemErrors.validate(item_errors)
        validate_policy(retry_policy)
        checks = checks.to_a
        checks.each { |item| raise InvalidArgumentError, 'check: expected PermissionCheck' unless item.is_a?(PermissionCheck) }
        operation(call_options) { checks.map { |item| lookup_check(item) } }.freeze
      end

      private

      # Runs +block+ under the lock and records the registered response
      # metadata, on success or failure, as the client does.
      def operation(call_options, &)
        raise InvalidArgumentError, 'call_options: expected SpiceDB::CallOptions' unless call_options.nil? || call_options.is_a?(CallOptions)

        @mutex.synchronize do
          call_options&.response_metadata&.record(header: @header, trailer: @trailer)
          yield
        end
      end

      # The relationships matching +block+ after +cursor+ (a 1-based position
      # in matching order), up to +limit+, each paired with its own cursor.
      def page(cursor, limit, &)
        start = 0
        if cursor && !cursor.empty?
          raise server_error(GRPC::Core::StatusCodes::INVALID_ARGUMENT, "error decoding cursor: #{cursor}") unless cursor.match?(/\A\d+\z/)

          start = cursor.to_i
        end
        matching = @relationships.values.select(&)
        items = matching.each_with_index.map { |relationship, index| [relationship, (index + 1).to_s] }.drop(start)
        limit ? items.first(limit) : items
      end

      def validate_paging(page_size, limit, retry_policy)
        positive(page_size, 'page_size')
        positive(limit, 'limit') if limit
        validate_policy(retry_policy)
      end

      def validate_policy(policy)
        raise InvalidArgumentError, 'retry_policy: expected SpiceDB::RetryPolicy' unless policy.nil? || policy.is_a?(RetryPolicy)
      end

      def positive(value, name)
        raise InvalidArgumentError, "#{name} must be a positive integer" unless value.is_a?(Integer) && value.positive?
      end

      # The error the client raises when SpiceDB answers with +code+ and +message+.
      def server_error(code, message, reason: '')
        Error.from_code(code, message, server_message: message, reason: reason, reason_domain: reason.empty? ? '' : 'authzed.com')
      end

      def recreate_error(relationship)
        server_error(GRPC::Core::StatusCodes::ALREADY_EXISTS,
                     "could not CREATE relationship `#{relationship}`, as it already existed. If this is persistent, please switch " \
                     'to TOUCH operations or specify a precondition', reason: 'ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP')
      end

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

        raise server_error(GRPC::Core::StatusCodes::FAILED_PRECONDITION, "unable to satisfy write precondition `#{precondition}`",
                           reason: 'ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE')
      end

      def next_revision
        @revision += 1
        current_revision
      end

      def current_revision = ZedToken.new(@revision.to_s)
    end
  end
end
