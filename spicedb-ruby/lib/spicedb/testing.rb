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
    # the import commits. ITEM: before a read, export or lookup yields item
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
    # #check, #check_bulk, #lookup_resources and #lookup_subjects return the
    # same native values as the client's; #close does nothing. The other
    # operations (watch, expand, schema introspection and the experimental
    # operations) have no in-memory semantics: they are recorded and consult
    # the fault hook like any call, then raise the client's error for an
    # UNIMPLEMENTED status (a SpiceDB::Error whose +code+ is UNIMPLEMENTED);
    # streaming ones raise it when iterated.
    #
    # It applies SpiceDB's relationship semantics -- Filter#matches?,
    # preconditions, CREATE conflicts, atomic transactions and imports -- and
    # raises errors with the same classes, codes and message shape as the
    # client: a server failure's +message+ and +server_message+ are the
    # server's message (for example "could not CREATE relationship ..."), and
    # local validation raises InvalidArgumentError (whose server_message is its message).
    # Empty writes check their preconditions and return the current revision,
    # as SpiceDB does.
    #
    # It does not evaluate the schema. A permission such as
    # +view = reader + writer + parent_folder->read+ is granted only by a
    # stored relationship whose relation is literally +view+, or by the check
    # resolver: storing +document:1#reader@user:alice+ does not grant +view+.
    # Tests of real permission logic should model it in the check resolver,
    # or run SpiceDB itself (+spicedb serve-testing+ or a container) for
    # schema-accurate results.
    #
    # A check resolves in this order, the same in every SpiceDB client's
    # double:
    #
    # 1. a result registered for that exact check with #set_check_result;
    # 2. the check resolver (#check_resolver=, or +check_resolver:+ on
    #    construction): a callable receiving the PermissionCheck, with any
    #    call-level context merged in. It returns a CheckResult, a
    #    permissionship Symbol, true or false, or nil to defer to the next
    #    step. +store.check_resolver = Store::GRANT_ALL+ grants everything;
    # 3. the stored relationships: one on the check's resource whose relation
    #    is the permission's name and whose subject matches exactly, or by
    #    wildcard (+user:*+ matches every +user+), grants. A caveated match
    #    grants conditionally (caveats are not evaluated), and an expired one
    #    does not grant;
    # 4. anything else is :no_permission.
    #
    # #lookup_resources and #lookup_subjects enumerate the objects of the
    # requested type that appear in the stored relationships (as resource or
    # subject, in insertion order) and return those check resolution grants,
    # conditional ones as :conditional_permission. They honor
    # +permissionship_filter:+, +limit:+ and +cursor:+ (Store.cursor_after)
    # and report no excluded subjects. #reflect_schema returns the result registered with
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
        raise LocalValidationError, 'cursor_after: expected a non-negative Integer' unless count.is_a?(Integer) && !count.negative?

        count.to_s
      end

      # A check resolver that grants every check; see #check_resolver=.
      GRANT_ALL = ->(_check) { :has_permission }

      # @param relationships [Array<Relationship>] the initial relationships
      # @param check_resolver [#call, nil] see #check_resolver=
      def initialize(relationships = [], check_resolver: nil)
        @mutex = Mutex.new
        @relationships = {}
        @checks = {}
        self.check_resolver = check_resolver
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
        raise LocalValidationError, 'fault_hook: expected a callable or nil' unless hook.nil? || hook.respond_to?(:call)

        @mutex.synchronize { @fault_hook = hook }
      end

      # Makes the next call of +operation+ (one of OPERATIONS) raise +error+
      # before doing anything; later calls succeed. Replaces any fault hook.
      def fail_next(operation, error)
        raise LocalValidationError, "fail_next: unknown operation #{operation.inspect}" unless OPERATIONS.include?(operation)

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
        raise LocalValidationError, 'set_check_result: expected CheckResult or permissionship Symbol' unless result.is_a?(CheckResult)

        answered!(result, 'set_check_result')
        @mutex.synchronize { @checks[check_key(check)] = result }
        self
      end

      # Installs the check resolver (anything responding to +call+, or nil to
      # remove it), consulted for every check without a registered result,
      # including the checks lookups make. It receives the PermissionCheck and
      # returns a CheckResult, a permissionship Symbol (:has_permission,
      # :no_permission, :conditional_permission), true or false, or nil to
      # decline (fall through to the stored relationships). It answers checks
      # and does not fail them: inject failures with #fault_hook= or
      # #fail_next (an exception it raises anyway propagates). Tests over
      # generated IDs use it instead of registering each check, and GRANT_ALL
      # grants everything. It may run on several threads at once, and must
      # not call the store.
      def check_resolver=(resolver)
        raise LocalValidationError, 'check_resolver: expected a callable or nil' unless resolver.nil? || resolver.respond_to?(:call)

        @mutex.synchronize { @check_resolver = resolver }
      end

      # Registers the result #reflect_schema returns (stamped with the
      # store's current revision). Without one it returns no definitions and
      # no caveats, as SpiceDB does for an empty schema.
      def set_reflect_schema_result(result) # rubocop:disable Naming/AccessorMethodName -- pairs with set_check_result
        raise LocalValidationError, 'set_reflect_schema_result: expected ReflectSchemaResult' unless result.is_a?(ReflectSchemaResult)

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
        raise LocalValidationError, 'write: expected Transaction' unless transaction.is_a?(Transaction)

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
        raise LocalValidationError, 'expected Filter' unless filter.is_a?(Filter)

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
        raise LocalValidationError, 'expected Filter' unless filter.nil? || filter.is_a?(Filter)

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
        batch.each { |relationship| raise LocalValidationError, 'import: expected Relationship' unless relationship.is_a?(Relationship) }
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
      # +limit+ raise LocalValidationError, as SpiceDB does.
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
        raise LocalValidationError, 'on_progress must respond to call' unless on_progress.nil? || on_progress.respond_to?(:call)
        raise LocalValidationError, 'expected Filter' unless filter.is_a?(Filter)

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
        raise LocalValidationError, 'write_schema: expected String' unless schema.is_a?(String)

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
        raise LocalValidationError, 'check: expected PermissionCheck' unless check.is_a?(PermissionCheck)

        validate_policy(retry_policy)
        resolve_check(merge_context(check, context), operation(call_options) { snapshot })
      end

      def check_bulk(checks, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, item_errors: BulkItemErrors::RAISED,
                     call_options: nil, retry_policy: nil)
        begin_call(:check_bulk, checks, binding)
        BulkItemErrors.validate(item_errors)
        validate_policy(retry_policy)
        checks = checks.to_a
        checks.each { |item| raise LocalValidationError, 'check: expected PermissionCheck' unless item.is_a?(PermissionCheck) }
        state = operation(call_options) { snapshot }
        checks.map { |item| resolve_check(merge_context(item, context), state) }.freeze
      end

      # The objects of +resource_type+ in the stored relationships that check
      # resolution grants +permission+ to +subject+ (see the class
      # documentation). The n-th granted object carries the cursor
      # Store.cursor_after(n); results skipped by +permissionship_filter:
      # HAS_PERMISSION_ONLY+ still advance it, and +limit:+ counts yielded
      # results.
      def lookup_resources(resource_type:, permission:, subject:, consistency:, context: nil, limit: nil, # rubocop:disable Metrics/ParameterLists
                           page_size: 1000, timeout: nil, permissionship_filter: LookupPermissionshipFilter::ALL, cursor: nil,
                           call_options: nil, retry_policy: nil, paging: Paging::AUTOMATIC, &)
        call = begin_call(:lookup_resources, nil, binding)
        resource_type = Values.name(resource_type, 'resource_type')
        permission = Values.name(permission, 'permission')
        raise LocalValidationError, "subject: expected SubjectRef, got #{Values.received(subject)}" unless subject.is_a?(SubjectRef)

        LookupPermissionshipFilter.validate(permissionship_filter)
        Paging.validate(paging)
        validate_paging(page_size, limit, retry_policy)
        state = operation(call_options) { snapshot.merge(start: cursor_start(cursor)) }
        granted = candidates(state, resource_type).filter_map do |id|
          check = PermissionCheck.new(resource: ObjectRef.new(object_type: resource_type, object_id: id), permission: permission,
                                      subject: subject, context: context)
          result = resolve_check(check, state)
          [id, result] if GRANTING.include?(result.permissionship)
        end
        items = granted.each_with_index.map do |(id, result), index|
          LookupResource.new(resource_id: id, permissionship: result.permissionship, partial_caveat: partial_caveat(result),
                             looked_up_at: state[:revision], cursor: Store.cursor_after(index + 1))
        end.drop(state[:start])
        items = items.select { |item| item.permissionship == :has_permission } if permissionship_filter == LookupPermissionshipFilter::HAS_PERMISSION_ONLY
        items = items.first(limit) if limit
        stream = item_stream(call, items)
        block_given? ? stream.each(&) : stream
      end

      # The objects of +subject_type+ (with +subject_relation+; nil means
      # direct subjects) in the stored relationships that check resolution
      # grants +permission+ on +resource+. A stored wildcard subject
      # (+user:*+) is returned as subject ID "*". No subject is excluded.
      def lookup_subjects(resource:, permission:, subject_type:, consistency:, subject_relation: nil,
                          context: nil, timeout: nil, limit: nil, call_options: nil, retry_policy: nil, &)
        call = begin_call(:lookup_subjects, nil, binding)
        raise LocalValidationError, "resource: expected ObjectRef, got #{Values.received(resource)}" unless resource.is_a?(ObjectRef)

        permission = Values.name(permission, 'permission')
        subject_type = Values.name(subject_type, 'subject_type')
        validate_policy(retry_policy)
        positive(limit, 'limit') if limit
        state = operation(call_options) { snapshot }
        items = candidates(state, subject_type).filter_map do |id|
          subject = SubjectRef.new(object: ObjectRef.new(object_type: subject_type, object_id: id), relation: subject_relation)
          result = resolve_check(PermissionCheck.new(resource: resource, permission: permission, subject: subject, context: context), state)
          next unless GRANTING.include?(result.permissionship)

          LookupSubject.new(subject: ResolvedSubject.new(subject_id: id, permissionship: result.permissionship, partial_caveat: partial_caveat(result)),
                            excluded_subjects: [], looked_up_at: state[:revision])
        end
        items = items.first(limit) if limit
        stream = item_stream(call, items)
        block_given? ? stream.each(&) : stream
      end

      # Does nothing: the store holds no transport.
      def close = nil

      # Does nothing and returns the store: there is no connection to wait for.
      def connect!(timeout: Client::DEFAULT_CONNECT_TIMEOUT_SECONDS) = self # rubocop:disable Lint/UnusedMethodArgument -- the client's signature

      # nil: the store has no protocol client.
      def proto_client = nil

      # nil: the store never retries.
      def retry_policy = nil

      # Empty: the store never retries.
      def operation_retry_policies = {}.freeze

      # The remaining operations have no in-memory semantics: each is
      # recorded, consults the fault hook, validates its retry policy as the
      # client does, and raises UNIMPLEMENTED (streams when iterated).

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
        raise LocalValidationError, 'call_options: expected SpiceDB::CallOptions' unless call_options.nil? || call_options.is_a?(CallOptions)

        @mutex.synchronize do
          call_options&.response_metadata&.record(header: @header, trailer: @trailer)
          yield
        end
      end

      # The relationships matching +block+ after +cursor+ (a 1-based position
      # in matching order), up to +limit+, each paired with its own cursor.
      def page(cursor, limit, &)
        start = cursor_start(cursor)
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
        raise LocalValidationError, 'retry_policy: expected SpiceDB::RetryPolicy' unless policy.nil? || policy.is_a?(RetryPolicy)
      end

      def positive(value, name)
        raise LocalValidationError, "#{name} must be a positive integer" unless value.is_a?(Integer) && value.positive?
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

      # The number of results +cursor+ (Store.cursor_after) skips.
      def cursor_start(cursor)
        return 0 if cursor.nil? || cursor.empty?
        raise server_error(GRPC::Core::StatusCodes::INVALID_ARGUMENT, "error decoding cursor: #{cursor}") unless cursor.match?(/\A\d+\z/)

        cursor.to_i
      end

      # Permissionships a lookup returns.
      GRANTING = %i[has_permission conditional_permission].freeze
      private_constant :GRANTING

      # Permissionships a check answers with. SpiceDB never answers
      # UNSPECIFIED, and the client raises a malformed-response error for
      # it, so the double never returns :unspecified except on an errored
      # bulk item. (The client also maps a permissionship newer than itself
      # to :unspecified, which never grants; the double has no newer ones.)
      ANSWERS = %i[no_permission has_permission conditional_permission].freeze
      private_constant :ANSWERS

      def answered!(result, what)
        return result if result.error || ANSWERS.include?(result.permissionship)

        raise LocalValidationError, "#{what}: expected permissionship :has_permission, :no_permission or :conditional_permission, " \
                                    "got #{result.permissionship.inspect} (SpiceDB never answers a check without one)"
      end

      # What check resolution reads, taken under the lock so the resolver
      # runs outside it.
      def snapshot
        { checks: @checks.dup, resolver: @check_resolver, relationships: @relationships.values, revision: current_revision }
      end

      # Call-level +context+ merged under the check's own (item keys win).
      def merge_context(check, context)
        return check if context.nil?

        check.with(context: context.merge(check.context || {}))
      end

      # Resolves +check+ against +state+ (a #snapshot) in the documented order.
      def resolve_check(check, state)
        registered = state[:checks][check_key(check)]
        return registered if registered

        resolved = state[:resolver]&.call(check)
        resolved = resolver_result(resolved, state[:revision]) unless resolved.nil?
        resolved || stored_result(check, state)
      end

      def resolver_result(value, revision)
        case value
        when CheckResult then answered!(value, 'check_resolver')
        when true then CheckResult.new(permissionship: :has_permission, missing_context: [], checked_at: revision)
        when false then CheckResult.new(permissionship: :no_permission, missing_context: [], checked_at: revision)
        when Symbol then answered!(CheckResult.new(permissionship: value, missing_context: [], checked_at: revision), 'check_resolver')
        else raise LocalValidationError, "check_resolver: expected CheckResult, permissionship Symbol, true, false or nil, got #{Values.received(value)}"
        end
      end

      # A stored relationship on the resource, named like the permission,
      # whose subject matches exactly or by wildcard, grants.
      def stored_result(check, state)
        now = Time.now
        matches = state[:relationships].select do |relationship|
          relationship.resource == check.resource && relationship.relation == check.permission &&
            subject_matches?(relationship.subject, check.subject) && (relationship.expiration.nil? || relationship.expiration > now)
        end
        permissionship = if matches.any? { |relationship| relationship.caveat.nil? } then :has_permission
                         elsif matches.empty? then :no_permission
                         else :conditional_permission
                         end
        CheckResult.new(permissionship: permissionship, missing_context: [], checked_at: state[:revision])
      end

      def subject_matches?(stored, wanted)
        stored.relation == wanted.relation &&
          (stored.object == wanted.object || stored.object == ObjectRef.new(object_type: wanted.object.object_type, object_id: '*'))
      end

      # The IDs of +object_type+ objects in the stored relationships, as
      # resource or subject, in insertion order.
      def candidates(state, object_type)
        state[:relationships].flat_map do |relationship|
          [relationship.resource, relationship.subject.object].select { |object| object.object_type == object_type }.map(&:object_id)
        end.uniq
      end

      def partial_caveat(result)
        return nil unless result.permissionship == :conditional_permission

        PartialCaveatInfo.new(missing_required_context: result.missing_context)
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
