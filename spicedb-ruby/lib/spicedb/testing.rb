# frozen_string_literal: true

require_relative '../spicedb'

module SpiceDB
  # Test support. Load it explicitly: `require 'spicedb/testing'`.
  module Testing
    # One recorded Store operation: +operation+ is the method name (one of
    # Store::OPERATIONS), +index+ counts calls of that operation from 0,
    # +request+ is its positional argument (the transaction, filter, check,
    # checks, relationships, schema or counter name; nil for operations
    # taking only keywords, such as read_schema or lookup_resources), and
    # +options+ is a frozen Hash of every keyword argument as the call bound
    # it, defaults included.
    Call = Data.define(:operation, :index, :request, :options)

    # Where in an operation a fault hook runs. CALL: before the operation
    # does anything. IMPORT_BATCH: before import batch Fault#item (0-based,
    # of the call's +batch_size:+). IMPORT_COMMIT: after every batch, before
    # the import commits. ITEM: before a read or export yields item
    # Fault#item (0-based).
    class FaultStage < Option
      cases :CALL, :IMPORT_BATCH, :IMPORT_COMMIT, :ITEM
    end

    # The point at which the fault hook is consulted; +item+ is the batch or
    # item index for IMPORT_BATCH and ITEM, nil otherwise; +batch+ is the
    # frozen Array of that batch's relationships at IMPORT_BATCH, nil
    # otherwise.
    Fault = Data.define(:call, :stage, :item, :batch) do
      def initialize(call:, stage:, item: nil, batch: nil) = super
    end

    # Whether Store#read_schema reports a revision. INCLUDED (the default)
    # reports the store's revision; OMITTED reports none (+revision+ is nil),
    # as some hosted SpiceDB deployments do, so code that falls back to
    # another revision source can be tested.
    class SchemaRevision < Option
      cases :INCLUDED, :OMITTED
    end

    # An in-memory stand-in for SpiceDB::Client. It has every public method
    # of the client with the same parameters (a spec keeps the two in sync),
    # so code written against the client accepts either. #write,
    # #read_relationships, #delete_relationships,
    # #delete_all_relationships_non_atomic, #import_relationships,
    # #export_relationships, #read_schema, #write_schema, #reflect_schema,
    # #check and #check_bulk return the same native values as the client's;
    # #close does nothing. The other operations (lookups, watch, expand,
    # schema introspection and the experimental operations) have no
    # in-memory semantics: they are recorded and consult the fault hook like
    # any call, then raise the client's error for an UNIMPLEMENTED status
    # (a SpiceDB::Error whose +code+ is UNIMPLEMENTED); streaming ones raise
    # it when iterated.
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
    #
    # Every call is recorded (#calls, #calls_of) with its request and keyword
    # options, so tests can assert on what was sent without a strict mock.
    # Faults can be injected (#fault_hook=, #fail_next) before any operation,
    # before each import batch, before an import commits, and before each read
    # or export item, so failure paths need no fake gRPC server. Reads and
    # exports return a re-iterable Enumerator (or yield to a block), so a
    # fault can interrupt them after some items.
    # Unused keyword arguments keep the real client's exact signatures.
    class Store
      NO_SCHEMA = 'no schema has been defined; please call WriteSchema to start'
      private_constant :NO_SCHEMA

      # The operations the store records, as in Call#operation and accepted
      # by #fail_next: every client operation (Client::OPERATIONS).
      OPERATIONS = Client::OPERATIONS

      # Builds the error the client raises when SpiceDB answers with +code+
      # (a GRPC::Core::StatusCodes value) and +message+, for fault hooks: its
      # +message+ and +server_message+ are +message+, and a +reason+ is
      # SpiceDB's ErrorInfo reason.
      def self.server_error(code, message, reason: '')
        Error.from_code(code, message, server_message: message, reason: reason, reason_domain: reason.empty? ? '' : 'authzed.com')
      end

      # The cursor that resumes a read or export after the first +count+
      # matching relationships (in insertion order): the same cursor the
      # store puts on item number +count+ (1-based). The format, +count+ in
      # decimal, is stable for tests.
      def self.cursor_after(count)
        raise InvalidArgumentError, 'cursor_after: expected a non-negative Integer' unless count.is_a?(Integer) && !count.negative?

        count.to_s
      end

      def initialize(relationships = [])
        @mutex = Mutex.new
        @relationships = {}
        @checks = {}
        @revision = 0
        @schema = nil
        @reflection = nil
        @header = {}
        @trailer = {}
        @calls = []
        @counts = Hash.new(0)
        @fault_hook = nil
        @schema_revision = SchemaRevision::INCLUDED
        relationships.each { |relationship| @relationships[key(relationship)] = relationship }
      end

      # Installs +hook+ (anything responding to +call+, or nil to remove it),
      # called with a Fault at every fault point. When it returns an
      # exception, the operation raises it at that point; a failed write or
      # import applies nothing. Build server-shaped errors with
      # Store.server_error. The hook must not call the store.
      def fault_hook=(hook)
        raise InvalidArgumentError, 'fault_hook: expected a callable or nil' unless hook.nil? || hook.respond_to?(:call)

        @mutex.synchronize { @fault_hook = hook }
      end

      # Makes the next call of +operation+ (one of OPERATIONS) raise +error+
      # before doing anything; later calls succeed. Replaces any fault hook.
      def fail_next(operation, error)
        raise InvalidArgumentError, "fail_next: unknown operation #{operation.inspect}" unless OPERATIONS.include?(operation)

        failed = false
        self.fault_hook = lambda do |fault|
          next nil if failed || fault.stage != FaultStage::CALL || fault.call.operation != operation

          failed = true
          error
        end
        self
      end

      # @return [Array<Call>] every recorded call, in order
      def calls = @mutex.synchronize { @calls.dup.freeze }

      # @return [Array<Call>] the recorded calls of +operation+, in order
      def calls_of(operation) = calls.select { |call| call.operation == operation }.freeze

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

      # Selects whether #read_schema reports a revision (a SchemaRevision).
      def read_schema_revision=(mode)
        SchemaRevision.validate(mode)
        @mutex.synchronize { @schema_revision = mode }
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
        begin_call(:write, transaction, binding)
        raise InvalidArgumentError, 'write: expected Transaction' unless transaction.is_a?(Transaction)

        Retrying.mutation_policy(retry_policy, transaction.idempotent?, 'write')
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
        call = begin_call(:read_relationships, filter, binding)
        raise InvalidArgumentError, 'expected Filter' unless filter.is_a?(Filter)

        validate_paging(page_size, limit, retry_policy)
        Paging.validate(paging)
        items = operation(call_options) do
          token = current_revision
          page(cursor, limit) { |relationship| filter.matches?(relationship) }.map do |relationship, after|
            ReadRelationship.new(relationship: relationship, cursor: after, read_at: token)
          end
        end
        stream = item_stream(call, items)
        block_given? ? stream.each(&) : stream
      end

      def export_relationships(filter = nil, consistency:, page_size: 1000, timeout: nil, limit: nil, cursor: nil, call_options: nil,
                               retry_policy: nil, &)
        call = begin_call(:export_relationships, filter, binding)
        raise InvalidArgumentError, 'expected Filter' unless filter.nil? || filter.is_a?(Filter)

        validate_paging(page_size, limit, retry_policy)
        items = operation(call_options) do
          page(cursor, limit) { |relationship| filter.nil? || filter.matches?(relationship) }.map do |relationship, after|
            ExportedRelationship.new(relationship: relationship, cursor: after)
          end
        end
        stream = item_stream(call, items)
        block_given? ? stream.each(&) : stream
      end

      # Adds every relationship in one transaction, as SpiceDB does: if any
      # already exists (or repeats within the import), nothing is added and
      # AlreadyExistsError is raised. Returns the number imported.
      def import_relationships(relationships, timeout: nil, batch_size: Client::DEFAULT_IMPORT_BATCH_SIZE, call_options: nil, retry_policy: nil)
        call = begin_call(:import_relationships, relationships, binding)
        Retrying.mutation_policy(retry_policy, false, 'import_relationships')
        positive(batch_size, 'batch_size')
        batch = relationships.to_a
        batch.each { |relationship| raise InvalidArgumentError, 'import: expected Relationship' unless relationship.is_a?(Relationship) }
        batch.each_slice(batch_size).with_index { |slice, index| fault(call, FaultStage::IMPORT_BATCH, index, slice.freeze) }
        fault(call, FaultStage::IMPORT_COMMIT)
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
        begin_call(:delete_relationships, filter, binding)
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

      # Deletes every matching relationship in one step after checking the
      # preconditions, calls +on_progress+ once with the result, and returns
      # it (complete, no cursor). +page_size+ is validated and +cursor+
      # accepted; neither otherwise matters.
      def delete_all_relationships_non_atomic(filter, page_size: 1000, must_match: [], must_not_match: [], timeout: nil, cursor: nil,
                                              on_progress: nil, call_options: nil, retry_policy: nil)
        begin_call(:delete_all_relationships_non_atomic, filter, binding)
        Retrying.mutation_policy(retry_policy, false, 'delete_all_relationships_non_atomic')
        raise InvalidArgumentError, 'on_progress must respond to call' unless on_progress.nil? || on_progress.respond_to?(:call)
        raise InvalidArgumentError, 'expected Filter' unless filter.is_a?(Filter)

        positive(page_size, 'page_size')
        result = operation(call_options) do
          must_match.each { |item| check_precondition(Precondition.new(operation: :must_match, filter: item)) }
          must_not_match.each { |item| check_precondition(Precondition.new(operation: :must_not_match, filter: item)) }
          matched = @relationships.select { |_identity, relationship| filter.matches?(relationship) }.keys
          matched.each { |identity| @relationships.delete(identity) }
          DeleteResult.new(revision: next_revision, deleted_count: matched.length, progress: :complete, cursor: nil)
        end
        on_progress&.call(result)
        result
      end

      # Returns the schema last written; NotFoundError before any, as SpiceDB does.
      # Its revision is nil after +read_schema_revision = SchemaRevision::OMITTED+.
      def read_schema(timeout: nil, call_options: nil, retry_policy: nil)
        begin_call(:read_schema, nil, binding)
        validate_policy(retry_policy)
        operation(call_options) do
          raise server_error(GRPC::Core::StatusCodes::NOT_FOUND, NO_SCHEMA) if @schema.nil?

          SchemaResult.new(schema: @schema, revision: @schema_revision == SchemaRevision::OMITTED ? nil : current_revision)
        end
      end

      # Stores the schema text without compiling or validating it.
      def write_schema(schema, timeout: nil, call_options: nil, retry_policy: nil)
        begin_call(:write_schema, schema, binding)
        Retrying.mutation_policy(retry_policy, true, 'write_schema')
        raise InvalidArgumentError, 'write_schema: expected String' unless schema.is_a?(String)

        operation(call_options) do
          @schema = schema.dup.freeze
          next_revision
        end
      end

      def reflect_schema(consistency:, filters: [], timeout: nil, call_options: nil, retry_policy: nil)
        begin_call(:reflect_schema, nil, binding)
        validate_policy(retry_policy)
        operation(call_options) do
          reflection = @reflection || ReflectSchemaResult.new(definitions: [], caveats: [], revision: nil)
          reflection.with(revision: current_revision)
        end
      end

      def check(check, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, call_options: nil, retry_policy: nil)
        begin_call(:check, check, binding)
        raise InvalidArgumentError, 'check: expected PermissionCheck' unless check.is_a?(PermissionCheck)

        validate_policy(retry_policy)
        operation(call_options) { lookup_check(check) }
      end

      def check_bulk(checks, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, item_errors: BulkItemErrors::RAISED,
                     call_options: nil, retry_policy: nil)
        begin_call(:check_bulk, checks, binding)
        BulkItemErrors.validate(item_errors)
        validate_policy(retry_policy)
        checks = checks.to_a
        checks.each { |item| raise InvalidArgumentError, 'check: expected PermissionCheck' unless item.is_a?(PermissionCheck) }
        operation(call_options) { checks.map { |item| lookup_check(item) } }.freeze
      end

      # Does nothing: the store holds no transport.
      def close = nil

      # nil: the store has no protocol client.
      def proto_client = nil

      # nil: the store never retries.
      def retry_policy = nil

      # Empty: the store never retries.
      def operation_retry_policies = {}.freeze

      # The remaining operations have no in-memory semantics: each is
      # recorded, consults the fault hook, validates its retry policy as the
      # client does, and raises UNIMPLEMENTED (streams when iterated).

      def lookup_resources(resource_type:, permission:, subject:, consistency:, context: nil, limit: nil, # rubocop:disable Metrics/ParameterLists
                           page_size: 1000, timeout: nil, permissionship_filter: LookupPermissionshipFilter::ALL, cursor: nil,
                           call_options: nil, retry_policy: nil, paging: Paging::AUTOMATIC, &)
        unimplemented_stream(:lookup_resources, nil, binding, retry_policy, &)
      end

      def lookup_subjects(resource:, permission:, subject_type:, consistency:, subject_relation: nil,
                          context: nil, timeout: nil, limit: nil, call_options: nil, retry_policy: nil, &)
        unimplemented_stream(:lookup_subjects, nil, binding, retry_policy, &)
      end

      def watch(object_types: [], start_revision: nil, content: [WatchContent::RELATIONSHIPS], filters: [], timeout: nil,
                call_options: nil, retry_policy: nil, &)
        unimplemented_stream(:watch, nil, binding, retry_policy, &)
      end

      def computable_permissions(definition_name:, relation_name:, consistency:, definition_name_filter: nil, timeout: nil, call_options: nil,
                                 retry_policy: nil)
        unimplemented(:computable_permissions, nil, binding, retry_policy)
      end

      def dependent_relations(definition_name:, permission_name:, consistency:, timeout: nil, call_options: nil, retry_policy: nil)
        unimplemented(:dependent_relations, nil, binding, retry_policy)
      end

      def diff_schema(comparison_schema, consistency:, timeout: nil, call_options: nil, retry_policy: nil)
        unimplemented(:diff_schema, comparison_schema, binding, retry_policy)
      end

      def expand_permission_tree(resource:, permission:, consistency:, timeout: nil, call_options: nil, retry_policy: nil)
        unimplemented(:expand_permission_tree, nil, binding, retry_policy)
      end

      def experimental_register_relationship_counter(name, filter, timeout: nil, call_options: nil, retry_policy: nil)
        unimplemented(:experimental_register_relationship_counter, name, binding, retry_policy)
      end

      def experimental_count_relationships(name, timeout: nil, call_options: nil, retry_policy: nil)
        unimplemented(:experimental_count_relationships, name, binding, retry_policy)
      end

      def experimental_unregister_relationship_counter(name, timeout: nil, call_options: nil, retry_policy: nil)
        unimplemented(:experimental_unregister_relationship_counter, name, binding, retry_policy)
      end

      def experimental_roaring_lookup_resources(resource_type:, permission:, subject:, consistency:, timeout: nil, call_options: nil,
                                                retry_policy: nil)
        unimplemented(:experimental_roaring_lookup_resources, nil, binding, retry_policy)
      end

      def experimental_count_relationships_by_filter(filter, timeout: nil, call_options: nil, retry_policy: nil)
        unimplemented(:experimental_count_relationships_by_filter, filter, binding, retry_policy)
      end

      def experimental_watch_permission_sets(start_revision: nil, timeout: nil, call_options: nil, retry_policy: nil, &)
        unimplemented_stream(:experimental_watch_permission_sets, nil, binding, retry_policy, &)
      end

      def experimental_lookup_permission_sets(limit: 1000, at_revision: nil, cursor: nil, timeout: nil, call_options: nil, retry_policy: nil, &)
        unimplemented_stream(:experimental_lookup_permission_sets, nil, binding, retry_policy, &)
      end

      def experimental_download_permission_sets(at_revision: nil, timeout: nil, call_options: nil, retry_policy: nil)
        unimplemented(:experimental_download_permission_sets, nil, binding, retry_policy)
      end

      private

      # Records +operation+, consults the fault hook and validates
      # +retry_policy+, then raises UNIMPLEMENTED.
      def unimplemented(operation, request, bound, retry_policy)
        begin_unimplemented(operation, request, bound, retry_policy)
        raise unimplemented_error(operation)
      end

      # As #unimplemented, but UNIMPLEMENTED is raised when the returned
      # stream is iterated (at once when a block is given), as the client's
      # streams surface server errors.
      def unimplemented_stream(operation, request, bound, retry_policy, &)
        begin_unimplemented(operation, request, bound, retry_policy)
        stream = Enumerator.new { raise unimplemented_error(operation) }
        block_given? ? stream.each(&) : stream
      end

      def begin_unimplemented(operation, request, bound, retry_policy)
        begin_call(operation, request, bound)
        if Client::NEVER_IDEMPOTENT_OPERATIONS.include?(operation)
          Retrying.mutation_policy(retry_policy, false, operation.to_s)
        else
          validate_policy(retry_policy)
        end
      end

      def unimplemented_error(operation)
        server_error(GRPC::Core::StatusCodes::UNIMPLEMENTED, "#{operation} is not implemented by SpiceDB::Testing::Store")
      end

      # Records a call of +operation+ with its +request+ and the keyword
      # options bound in +bound+ (the operation's binding), then consults the
      # fault hook at FaultStage::CALL.
      def begin_call(operation, request, bound)
        names = self.class.instance_method(operation).parameters.filter_map { |type, name| name if %i[key keyreq].include?(type) }
        options = names.to_h { |name| [name, bound.local_variable_get(name)] }.freeze
        call = @mutex.synchronize do
          index = @counts[operation]
          @counts[operation] += 1
          Call.new(operation: operation, index: index, request: request, options: options).tap { |recorded| @calls << recorded }
        end
        fault(call, FaultStage::CALL)
        call
      end

      # Consults the fault hook at one point of +call+ and raises what it returns.
      def fault(call, stage, item = nil, batch = nil)
        hook = @mutex.synchronize { @fault_hook }
        error = hook&.call(Fault.new(call: call, stage: stage, item: item, batch: batch))
        raise error if error
      end

      # A re-iterable Enumerator over +items+ that consults the fault hook
      # before each one.
      def item_stream(call, items)
        Enumerator.new do |out|
          items.each_with_index do |item, index|
            fault(call, FaultStage::ITEM, index)
            out << item
          end
        end
      end

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
        items = matching.each_with_index.map { |relationship, index| [relationship, Store.cursor_after(index + 1)] }.drop(start)
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

      def server_error(...) = Store.server_error(...)

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
