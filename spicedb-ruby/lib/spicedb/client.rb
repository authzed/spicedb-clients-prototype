# frozen_string_literal: true

module SpiceDB
  # Schema reflection result types
  SchemaDefinition = Data.define(:name, :comment, :relations, :permissions)
  SchemaRelation = Data.define(:name, :comment, :parent_definition_name)
  SchemaPermission = Data.define(:name, :comment, :parent_definition_name)
  SchemaCaveat = Data.define(:name, :comment, :expression, :parameters)
  SchemaCaveatParameter = Data.define(:name, :type, :parent_caveat_name)
  ReflectSchemaResult = Data.define(:definitions, :caveats, :revision)
  RelationReference = Data.define(:definition_name, :relation_name, :is_permission)
  SchemaDiff = Data.define(:kind, :definition_name, :relation_name, :permission_name, :caveat_name) do
    def initialize(kind:, definition_name: nil, relation_name: nil, permission_name: nil, caveat_name: nil)
      super
    end
  end
  # rubocop:disable Lint/DataDefineOverride -- :object_id mirrors the wire field and Go's ObjectRef.ObjectID; overriding Kernel#object_id is safe here since Data uses field-based ==/hash
  ObjectRef = Data.define(:object_type, :object_id)
  # rubocop:enable Lint/DataDefineOverride
  SubjectRef = Data.define(:subject_type, :subject_id, :optional_relation)
  IntermediateNode = Data.define(:operation, :children)
  LeafNode = Data.define(:subjects)
  PermissionTree = Data.define(:expanded_object, :expanded_relation, :intermediate, :leaf)
  ExpandResult = Data.define(:tree, :revision)
  CountResult = Data.define(:relationship_count, :revision, :still_calculating)
  Update = Data.define(:operation, :relationship)

  # Lookup result types — mirror spicedb-go's client/lookup_types.go.
  # `permissionship` is one of :unspecified, :no_permission, :has_permission,
  # :conditional_permission — the same native symbol set CheckResult uses
  # below. Lookup surfaces never produce :no_permission (a resource/subject
  # without the permission simply doesn't appear in lookup results — SpiceDB's
  # LookupPermissionship proto enum has no NO_PERMISSION value at all).
  # Callers MUST check `permissionship` before treating a result as a full
  # grant — a :conditional_permission result may resolve to false once the
  # missing caveat context (see `partial_caveat`) is supplied.
  PartialCaveatInfo = Data.define(:missing_required_context)
  # `looked_up_at` is the ZedToken (String) the lookup was evaluated
  # against — pass it to a later `Consistency.at_least` for read-your-writes
  # against this result.
  LookupResource = Data.define(:resource_id, :permissionship, :partial_caveat, :looked_up_at)
  ResolvedSubject = Data.define(:subject_id, :permissionship, :partial_caveat)
  # When `subject.subject_id` is the wildcard "*", `excluded_subjects` lists
  # subjects excluded from that wildcard grant — callers MUST treat those
  # subjects as NOT having the permission, even though the wildcard would
  # otherwise suggest they do. `looked_up_at` is the ZedToken (String) the
  # lookup was evaluated against.
  LookupSubject = Data.define(:subject, :excluded_subjects, :looked_up_at)

  # Check result types.
  #
  # `permissionship` is one of :unspecified, :no_permission, :has_permission,
  # :conditional_permission — CheckPermissionResponse's four-valued
  # Permissionship (unlike the lookup surfaces above, checks can
  # affirmatively report :no_permission). `missing_context` carries the
  # caveat parameter names SpiceDB could not evaluate because context wasn't
  # supplied at check time; it is `[]` unless `permissionship` is
  # :conditional_permission. `checked_at` is the ZedToken (String) the check
  # was evaluated against.
  #
  # `has_permission?` is true ONLY for :has_permission. A
  # :conditional_permission result is NOT a grant — it means SpiceDB could
  # not evaluate the caveat because context was missing, not that the caveat
  # evaluated to false. Ruby has no `__bool__`-style hook: every CheckResult
  # is truthy in a bare `if result`/`result ? ... : ...` regardless of
  # permissionship, so callers MUST call `result.has_permission?` explicitly
  # — testing the result itself is unconditionally true and will silently
  # treat a conditional (unevaluated) grant as allowed.
  CheckResult = Data.define(:permissionship, :missing_context, :checked_at) do
    # @return [Boolean] true only when permissionship is :has_permission
    def has_permission?
      permissionship == :has_permission
    end
  end

  # The idiomatic SpiceDB client for Ruby.
  #
  # Use {.new_plaintext} or {.new_system_tls} to create a client.
  # All read operations require an explicit {SpiceDB::Consistency::Strategy}.
  class Client
    # Default page sizes for transparent cursor pagination.
    DEFAULT_READ_PAGE_SIZE   = 512
    DEFAULT_LOOKUP_PAGE_SIZE = 512
    DEFAULT_EXPORT_PAGE_SIZE = 512
    DEFAULT_DELETE_PAGE_SIZE = 1_000
    DEFAULT_IMPORT_BATCH_SIZE = 1_000
    DEFAULT_CHECK_BATCH_SIZE = 1_000

    # Retry configuration for transient gRPC errors.
    MAX_RETRIES = 3
    BASE_RETRY_DELAY = 0.1 # seconds

    # @return [Object] the underlying proto client for advanced use cases
    attr_reader :proto_client

    # Creates a client with an insecure (plaintext) connection.
    # Use this for testing only — the lack of TLS is made obvious by the name.
    #
    # If a block is given, the client is yielded and cleanup is ensured.
    #
    # @param endpoint [String] the SpiceDB server address (e.g., "localhost:50051")
    # @param token [String] the preshared key for authentication
    # @yield [client] optionally yields the client for block-scoped usage
    # @return [Client]
    def self.new_plaintext(endpoint, token, &block)
      client = new(endpoint: endpoint, token: token, insecure: true)
      if block
        begin
          yield client
        ensure
          client.close
        end
      else
        client
      end
    end

    # Creates a client using the system's TLS certificate pool.
    # Use this for production connections.
    #
    # If a block is given, the client is yielded and cleanup is ensured.
    #
    # @param endpoint [String] the SpiceDB server address (e.g., "grpc.example.com:443")
    # @param token [String] the preshared key for authentication
    # @yield [client] optionally yields the client for block-scoped usage
    # @return [Client]
    def self.new_system_tls(endpoint, token, &block)
      client = new(endpoint: endpoint, token: token, insecure: false)
      if block
        begin
          yield client
        ensure
          client.close
        end
      else
        client
      end
    end

    # @api private
    # Use {.new_plaintext} or {.new_system_tls} instead.
    def initialize(endpoint:, token:, insecure: false)
      @endpoint = endpoint
      @token = token
      @insecure = insecure

      begin
        require 'spicedb_proto'
        @proto_client = SpiceDBProto::Client.new(endpoint, token, insecure: insecure)
      rescue LoadError
        # Proto gem not yet available (e.g. buf hasn't generated stubs).
        # Client object can still be created but gRPC calls will fail.
        @proto_client = nil
      end
    end

    # Closes the client connection.
    def close
      @proto_client&.close
    end

    # --- Checks (all via BulkCheckPermissions) ---

    # Checks a single permission.
    #
    # Returns a {SpiceDB::CheckResult}, not a Boolean — a caveated
    # relationship whose context wasn't supplied at check time comes back
    # with `permissionship == :conditional_permission`, which is neither a
    # grant nor a denial. Callers MUST use `result.has_permission?` (true
    # ONLY for :has_permission) rather than testing the result itself: Ruby
    # has no way to make an object falsy, so `if result` is unconditionally
    # true even for a conditional result.
    #
    # `context:` supplies caveat context for this check. `relationship` can
    # also carry its own `Relationship#check_context`, which overrides
    # `context:` for this one check — see {#check_permissions} for the
    # merge rule.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param permission [String] the permission to check
    # @param relationship [SpiceDB::Relationship]
    # @param context [Hash, nil] call-level caveat context for this check
    # @return [SpiceDB::CheckResult]
    def check_permission(consistency, permission, relationship, context: nil)
      check_permissions(consistency, permission, relationship, context: context).first
    end

    # Performs a bulk permission check on the given relationships.
    #
    # Returns a {SpiceDB::CheckResult} for each relationship — see
    # {#check_permission} for why this isn't a Boolean.
    #
    # `context:` is a call-level default applied to every relationship's
    # check. Each relationship can instead (or in addition) carry its own
    # `Relationship#check_context` (e.g. `rel.with_check_context({...})`),
    # which overrides `context:` for that one item — merged key-by-key, NOT
    # a wholesale replacement: the item's keys win on conflict, but
    # call-level keys the item doesn't mention are retained. An item with no
    # `check_context` inherits `context:` unchanged. `check_context` is
    # check-time-only and distinct from `Relationship#caveat_context` (which
    # is written to SpiceDB at write time) — see {SpiceDB::Relationship}.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param permission [String] the permission to check
    # @param relationships [Array<SpiceDB::Relationship>]
    # @param context [Hash, nil] call-level caveat context, applied to every item
    # @return [Array<SpiceDB::CheckResult>]
    def check_permissions(consistency, permission, *relationships, context: nil)
      relationships = relationships.flatten
      return [] if relationships.empty?

      with_retry do
        # Delegate to proto client BulkCheckPermissions
        # Each relationship becomes a CheckBulkPermissionsRequestItem
        items = relationships.map { |r| check_item_from_rel(r, permission) }

        call_bulk_check(consistency, items, context)
      end
    end

    # Returns true if any of the given relationships have the permission.
    #
    # Counts ONLY :has_permission results — a :conditional_permission result
    # does NOT count as a grant, since SpiceDB couldn't evaluate the caveat
    # without the missing context. This is deliberately fail-closed.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param permission [String]
    # @param relationships [Array<SpiceDB::Relationship>]
    # @param context [Hash, nil] call-level caveat context, applied to every item — see {#check_permissions}
    # @return [Boolean]
    def check_any(consistency, permission, *relationships, context: nil)
      check_permissions(consistency, permission, *relationships, context: context).any?(&:has_permission?)
    end

    # Returns true if all of the given relationships have the permission.
    #
    # Requires EVERY result to be :has_permission — a single
    # :conditional_permission result fails the check, since SpiceDB couldn't
    # evaluate that caveat without the missing context. This is deliberately
    # fail-closed.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param permission [String]
    # @param relationships [Array<SpiceDB::Relationship>]
    # @param context [Hash, nil] call-level caveat context, applied to every item — see {#check_permissions}
    # @return [Boolean]
    def check_all(consistency, permission, *relationships, context: nil)
      check_permissions(consistency, permission, *relationships, context: context).all?(&:has_permission?)
    end

    # --- Relationships ---

    # Commits a transaction of relationship mutations to SpiceDB.
    #
    # @param transaction [SpiceDB::Transaction]
    # @return [String] the revision at which the write occurred
    def write(transaction)
      with_retry do
        call_write_relationships(transaction)
      end
    end

    # Returns an Enumerator over relationships matching the given filter.
    # Cursors are handled transparently — the client automatically re-fetches
    # pages of 512 relationships.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param filter [SpiceDB::Filter]
    # @return [Enumerator<SpiceDB::Relationship>]
    def read_relationships(consistency, filter)
      Enumerator.new do |yielder|
        cursor = nil
        loop do
          page, new_cursor, count = with_retry do
            call_read_relationships(consistency, filter, cursor, DEFAULT_READ_PAGE_SIZE)
          end

          page.each { |rel| yielder << rel }

          break if count < DEFAULT_READ_PAGE_SIZE

          cursor = new_cursor
        end
      end
    end

    # Deletes all relationships matching the given filter. Large result sets
    # are automatically paged in batches of 1,000 (override with `limit:`).
    #
    # `must_match:`/`must_not_match:` add preconditions that guard the
    # delete: if a precondition fails, the server rejects that call and
    # deletes nothing for it. Preconditions are a per-request proto field,
    # so when a delete spans multiple pages (i.e. more matches than the
    # limit), they are re-evaluated by the server on every page — there is
    # no "check-once, apply-to-all-pages" semantics. This means a delete
    # that starts successfully can still fail partway through if the
    # guarded state changes between pages, after earlier pages have
    # already been deleted. For a single-shot, all-or-nothing guarded
    # delete, pair the precondition with a `limit:` large enough to cover
    # every matching relationship in one call.
    #
    # @param filter [SpiceDB::Filter]
    # @param must_match [Array<SpiceDB::Filter>] preconditions that must each match at least one relationship
    # @param must_not_match [Array<SpiceDB::Filter>] preconditions that must each match no relationships
    # @param limit [Integer, nil] overrides the default per-request page size (1,000)
    # @return [String] the revision of the final deletion
    def delete_relationships(filter, must_match: [], must_not_match: [], limit: nil)
      preconditions = must_match.map { |f| { operation: :must_match, filter: f } } +
                      must_not_match.map { |f| { operation: :must_not_match, filter: f } }
      page_size = limit || DEFAULT_DELETE_PAGE_SIZE

      revision = nil
      loop do
        rev, complete = with_retry do
          call_delete_relationships(filter, page_size, preconditions)
        end
        revision = rev
        break if complete
      end
      revision
    end

    # --- Lookups ---

    # Returns an Enumerator over resources of the given type that the subject
    # has the specified permission on. Each result carries the permissionship
    # (full grant vs conditional on caveat context) and, for conditional
    # results, which caveat context was missing. Cursors are handled
    # transparently.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param resource_type [String]
    # @param permission [String]
    # @param subject_type [String]
    # @param subject_id [String]
    # @return [Enumerator<SpiceDB::LookupResource>]
    def lookup_resources(consistency, resource_type, permission, subject_type, subject_id)
      Enumerator.new do |yielder|
        cursor = nil
        loop do
          resources, new_cursor, count = with_retry do
            call_lookup_resources(consistency, resource_type, permission, subject_type, subject_id, cursor,
                                  DEFAULT_LOOKUP_PAGE_SIZE)
          end

          resources.each { |resource| yielder << resource }

          break if count < DEFAULT_LOOKUP_PAGE_SIZE

          cursor = new_cursor
        end
      end
    end

    # Returns an Enumerator over subjects of the given type that have the
    # specified permission on the resource. Unlike lookup_resources, this does
    # not support cursor-based pagination and streams all results.
    #
    # When a yielded LookupSubject#subject is the wildcard "*", the server
    # has granted the permission to every subject of subject_type EXCEPT
    # those listed in LookupSubject#excluded_subjects. Callers MUST check
    # excluded_subjects before treating a wildcard match as a blanket grant,
    # or they risk granting access to subjects the server explicitly excluded.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param resource_type [String]
    # @param resource_id [String]
    # @param permission [String]
    # @param subject_type [String]
    # @return [Enumerator<SpiceDB::LookupSubject>]
    def lookup_subjects(consistency, resource_type, resource_id, permission, subject_type)
      Enumerator.new do |yielder|
        with_retry do
          call_lookup_subjects(consistency, resource_type, resource_id, permission, subject_type) do |lookup_subject|
            yielder << lookup_subject
          end
        end
      end
    end

    # --- Schema ---

    # Returns the current SpiceDB schema.
    #
    # @return [Array(String, String)] the schema text and revision
    def read_schema
      with_retry { call_read_schema }
    end

    # Writes a new schema to SpiceDB.
    #
    # @param schema [String] the schema text
    # @return [String] the revision
    def write_schema(schema)
      with_retry { call_write_schema(schema) }
    end

    # Returns the definitions and caveats in the current schema.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @return [SpiceDB::ReflectSchemaResult]
    def reflect_schema(consistency)
      with_retry { call_reflect_schema(consistency) }
    end

    # Returns the permissions that are computable for the given relation.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param definition_name [String]
    # @param relation_name [String]
    # @return [Array(Array<SpiceDB::RelationReference>, String)] references and revision
    def computable_permissions(consistency, definition_name, relation_name)
      with_retry { call_computable_permissions(consistency, definition_name, relation_name) }
    end

    # Returns the relations that the given permission depends on.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param definition_name [String]
    # @param permission_name [String]
    # @return [Array(Array<SpiceDB::RelationReference>, String)] references and revision
    def dependent_relations(consistency, definition_name, permission_name)
      with_retry { call_dependent_relations(consistency, definition_name, permission_name) }
    end

    # Compares the current schema against the given comparison schema.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param comparison_schema [String]
    # @return [Array(Array<SpiceDB::SchemaDiff>, String)] diffs and revision
    def diff_schema(consistency, comparison_schema)
      with_retry { call_diff_schema(consistency, comparison_schema) }
    end

    # --- Expand ---

    # Expands the permission tree for the given resource and permission.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param resource_type [String]
    # @param resource_id [String]
    # @param permission [String]
    # @return [SpiceDB::ExpandResult]
    def expand_permission_tree(consistency, resource_type, resource_id, permission)
      with_retry { call_expand_permission_tree(consistency, resource_type, resource_id, permission) }
    end

    # --- Bulk ---

    # Streams relationships to SpiceDB for bulk import. Relationships are
    # automatically batched into chunks of 1,000.
    #
    # @param relationships [Enumerable<SpiceDB::Relationship>]
    # @return [Integer] the number of relationships loaded
    def import_relationships(relationships)
      with_retry { call_import_relationships(relationships) }
    end

    # Returns an Enumerator over all relationships matching the optional filter,
    # streamed from SpiceDB in bulk. Cursors are handled transparently.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param filter [SpiceDB::Filter, nil] optional filter
    # @return [Enumerator<SpiceDB::Relationship>]
    def export_relationships(consistency, filter = nil)
      Enumerator.new do |yielder|
        cursor = nil
        loop do
          page, new_cursor, count = with_retry do
            call_export_relationships(consistency, filter, cursor, DEFAULT_EXPORT_PAGE_SIZE)
          end

          page.each { |rel| yielder << rel }

          break if count < DEFAULT_EXPORT_PAGE_SIZE

          cursor = new_cursor
        end
      end
    end

    # --- Watch ---

    # Returns an Enumerator over relationship changes from SpiceDB's watch API.
    #
    # @param object_types [Array<String>] object types to watch
    # @param start_revision [String, nil] optional revision to start from
    # @return [Enumerator<SpiceDB::Update>]
    def updates(object_types, start_revision: nil)
      Enumerator.new do |yielder|
        call_watch(object_types, start_revision) do |update|
          yielder << update
        end
      rescue StandardError => e
        # We intentionally do NOT retry the streaming watch here (unlike
        # with_retry for unary/paginated calls) — retrying a live
        # server-stream mid-flight risks re-emitting/replaying updates the
        # caller has already seen. Mapping the error to a native SpiceDB::*
        # type (instead of leaking a raw GRPC::BadStatus) is the correctness
        # fix; retry is left to the caller.
        raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

        raise
      end
    end

    # --- Experimental ---

    # Registers a named counter that tracks relationships matching the given filter.
    # The counter is computed asynchronously by SpiceDB.
    #
    # @note Experimental: wraps SpiceDB's ExperimentalService and may change without following the backwards-compatibility mandate.
    #
    # @param name [String] counter name
    # @param filter [SpiceDB::Filter]
    # @return [nil]
    def experimental_register_relationship_counter(name, filter)
      with_retry { call_register_relationship_counter(name, filter) }
      nil
    end

    # Reads the value of a previously registered relationship counter.
    #
    # @note Experimental: wraps SpiceDB's ExperimentalService and may change without following the backwards-compatibility mandate.
    #
    # @param name [String] counter name
    # @return [SpiceDB::CountResult]
    def experimental_count_relationships(name)
      with_retry { call_count_relationships(name) }
    end

    # Removes a previously registered relationship counter.
    #
    # @note Experimental: wraps SpiceDB's ExperimentalService and may change without following the backwards-compatibility mandate.
    #
    # @param name [String] counter name
    # @return [nil]
    def experimental_unregister_relationship_counter(name)
      with_retry { call_unregister_relationship_counter(name) }
      nil
    end

    private

    # Retries the block with exponential backoff for transient gRPC errors.
    def with_retry(max_retries: MAX_RETRIES)
      require_proto_client!
      attempts = 0
      begin
        yield
      rescue StandardError => e
        attempts += 1
        if SpiceDB.transient?(e) && attempts <= max_retries
          sleep(BASE_RETRY_DELAY * (2**(attempts - 1)))
          retry
        end
        raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

        raise
      end
    end

    # Builds a check item hash from a relationship and permission.
    # `check_context` carries the relationship's check-time-only per-item
    # caveat context (SpiceDB::Relationship#check_context) — distinct from
    # the relationship's write-time `caveat_context` — through to
    # `call_bulk_check`, where it's merged with the call-level context.
    def check_item_from_rel(relationship, permission)
      {
        resource_type: relationship.resource_type,
        resource_id: relationship.resource_id,
        permission: permission,
        subject_type: relationship.subject_type,
        subject_id: relationship.subject_id,
        subject_relation: relationship.subject_relation,
        check_context: relationship.check_context
      }
    end

    # Merges call-level and per-item caveat context for one check item.
    #
    # Key-level, item wins: `(call_level || {}).merge(item_level || {})`.
    # Item keys override call-level keys on conflict; call-level keys the
    # item doesn't mention are RETAINED — this is NOT wholesale replacement.
    # An item that supplies only one key must not silently drop every other
    # call-level key, or a caveat would fail for context the caller believed
    # it had already supplied (spec D3b), landing the caller right back in
    # the confusing CONDITIONAL_PERMISSION state this feature exists to
    # resolve.
    #
    # Returns nil — so the wire request sets no `context` field at all,
    # rather than an empty Struct — only when both inputs are nil (neither
    # call-level nor per-item context was supplied).
    def merge_check_context(call_level, item_level)
      return nil if call_level.nil? && item_level.nil?

      (call_level || {}).merge(item_level || {})
    end

    # Builds a Google::Protobuf::Struct from a merged caveat-context Hash,
    # or nil if context is nil. Struct requires String keys, so Symbol (or
    # other) keys are stringified. Values are dispatched by Ruby class onto
    # google.protobuf.Value's `kind` oneof (see #check_context_value) so
    # types are preserved on the wire — unlike a naive #to_s, this keeps
    # e.g. an Integer caveat parameter evaluable by CEL as a number rather
    # than turning it into the string "42".
    def check_context_to_struct(context)
      return nil if context.nil?

      struct = Google::Protobuf::Struct.new
      context.each { |k, v| struct.fields[k.to_s] = check_context_value(v) }
      struct
    end

    # Converts one Ruby value into a Google::Protobuf::Value, dispatched by
    # class onto the proto's `kind` oneof. Hash/Array recurse so nested
    # caveat context (e.g. a list or map parameter) round-trips correctly,
    # not just flat scalars.
    def check_context_value(value)
      case value
      when nil
        Google::Protobuf::Value.new(null_value: :NULL_VALUE)
      when true, false
        Google::Protobuf::Value.new(bool_value: value)
      when Numeric
        Google::Protobuf::Value.new(number_value: value)
      when String
        Google::Protobuf::Value.new(string_value: value)
      when Hash
        Google::Protobuf::Value.new(struct_value: check_context_to_struct(value))
      when Array
        list = Google::Protobuf::ListValue.new(values: value.map { |v| check_context_value(v) })
        Google::Protobuf::Value.new(list_value: list)
      else
        raise SpiceDB::InvalidArgumentError, "unsupported caveat context value type: #{value.class}"
      end
    end

    # Converts a Google::Protobuf::Value back into a Ruby value by dispatching on the
    # `kind` oneof. Hash/Array recurse so nested caveat context is fully converted.
    #
    # This shares a google.protobuf.Value codec with check_context_value, but is not
    # its inverse on any data path: check_context_value serves only the check surface
    # (check_context_to_struct, called from the check path), while this serves only the
    # relationship read path (struct_to_caveat_context). Check-time context and
    # write-time caveat context are different wire fields with different lifetimes and
    # must never be conflated — see DESIGN.md.
    #
    # Dispatching on `kind` is required for correctness, not tidiness: reading a
    # non-string Value via #string_value returns "" rather than raising, which would
    # silently destroy every numeric, boolean, list and nested value read back from
    # SpiceDB. An unset kind yields nil.
    def caveat_context_value_from_proto(value)
      case value.kind
      when :null_value   then nil
      when :bool_value   then value.bool_value
      when :number_value then value.number_value
      when :string_value then value.string_value
      when :struct_value then struct_to_caveat_context(value.struct_value)
      when :list_value   then value.list_value.values.map { |v| caveat_context_value_from_proto(v) }
      end
    end

    # Converts a Google::Protobuf::Struct into a plain string-keyed Ruby Hash.
    #
    # Struct#fields is a Google::Protobuf::Map, not a Hash, so Hash-only methods such
    # as transform_values raise NoMethodError on it directly. Map#to_h is not a safe
    # substitute either: for message-valued maps it recursively converts each Value via
    # the generic protobuf-to-hash conversion (e.g. {number_value: 7.0}) rather than
    # leaving it as a Value we can dispatch on, which would break
    # caveat_context_value_from_proto's `kind` dispatch. Map includes Enumerable, so we
    # iterate its raw entries directly instead of going through to_h at all.
    def struct_to_caveat_context(struct)
      struct.fields.each_with_object({}) { |(k, v), acc| acc[k] = caveat_context_value_from_proto(v) }
    end

    # --- Proto helpers ---

    # Raises if the proto client is not available (e.g. buf hasn't generated stubs).
    def require_proto_client!
      return if @proto_client

      raise SpiceDB::Error,
            'proto client not available — ensure the spicedb_proto gem is installed and buf-generated stubs exist'
    end

    # Builds an Authzed::Api::V1::Consistency proto from a Strategy.
    def build_consistency(strategy)
      Authzed::Api::V1::Consistency.new(**strategy.to_proto)
    end

    # Builds an Authzed::Api::V1::RelationshipFilter from a SpiceDB::Filter.
    def filter_to_proto(filter)
      args = { resource_type: filter.resource_type }
      args[:optional_resource_id] = filter.resource_id if filter.resource_id
      args[:optional_resource_id_prefix] = filter.resource_id_prefix if filter.resource_id_prefix
      args[:optional_relation] = filter.relation if filter.relation

      if filter.subject_type
        subject_filter = {
          subject_type: filter.subject_type
        }
        subject_filter[:optional_subject_id] = filter.subject_id if filter.subject_id
        if filter.subject_relation
          subject_filter[:optional_relation] = Authzed::Api::V1::SubjectFilter::RelationFilter.new(
            relation: filter.subject_relation
          )
        end
        args[:optional_subject_filter] = Authzed::Api::V1::SubjectFilter.new(**subject_filter)
      end

      Authzed::Api::V1::RelationshipFilter.new(**args)
    end

    # Converts a SpiceDB::Relationship to an Authzed::Api::V1::Relationship proto.
    def relationship_to_proto(rel)
      subject = Authzed::Api::V1::SubjectReference.new(
        object: Authzed::Api::V1::ObjectReference.new(
          object_type: rel.subject_type,
          object_id: rel.subject_id
        ),
        optional_relation: rel.subject_relation || ''
      )

      args = {
        resource: Authzed::Api::V1::ObjectReference.new(
          object_type: rel.resource_type,
          object_id: rel.resource_id
        ),
        relation: rel.resource_relation,
        subject: subject
      }

      if rel.caveat_name
        caveat_args = { caveat_name: rel.caveat_name }
        if rel.caveat_context
          context_struct = Google::Protobuf::Struct.new
          rel.caveat_context.each { |k, v| context_struct.fields[k.to_s] = Google::Protobuf::Value.new(string_value: v.to_s) }
          caveat_args[:context] = context_struct
        end
        args[:optional_caveat] = Authzed::Api::V1::ContextualizedCaveat.new(**caveat_args)
      end

      if rel.expiration
        args[:optional_expires_at] = Google::Protobuf::Timestamp.new(
          seconds: rel.expiration.to_i,
          nanos: rel.expiration.nsec
        )
      end

      Authzed::Api::V1::Relationship.new(**args)
    end

    # Converts an Authzed::Api::V1::Relationship proto to a SpiceDB::Relationship.
    def relationship_from_proto(proto_rel)
      caveat_name = nil
      caveat_context = nil
      if proto_rel.optional_caveat&.caveat_name && !proto_rel.optional_caveat.caveat_name.empty?
        caveat_name = proto_rel.optional_caveat.caveat_name
        caveat_context = struct_to_caveat_context(proto_rel.optional_caveat.context) if proto_rel.optional_caveat.context
      end

      expiration = nil
      if proto_rel.optional_expires_at&.seconds&.positive?
        expiration = Time.at(proto_rel.optional_expires_at.seconds,
                             proto_rel.optional_expires_at.nanos, :nsec)
      end

      SpiceDB::Relationship.new(
        resource_type: proto_rel.resource.object_type,
        resource_id: proto_rel.resource['object_id'],
        resource_relation: proto_rel.relation,
        subject_type: proto_rel.subject.object.object_type,
        subject_id: proto_rel.subject.object['object_id'],
        subject_relation: proto_rel.subject.optional_relation,
        caveat_name: caveat_name,
        caveat_context: caveat_context,
        expiration: expiration
      )
    end

    # Maps transaction operation symbols to proto enum values.
    OPERATION_MAP = {
      create: :OPERATION_CREATE,
      touch: :OPERATION_TOUCH,
      delete: :OPERATION_DELETE
    }.freeze

    # Maps precondition operation symbols to proto enum values.
    PRECONDITION_MAP = {
      must_match: :OPERATION_MUST_MATCH,
      must_not_match: :OPERATION_MUST_NOT_MATCH
    }.freeze

    # Builds Authzed::Api::V1::Precondition protos from an array of
    # {operation:, filter:} hashes (the shape used by both
    # SpiceDB::Transaction#preconditions and delete_relationships'
    # must_match:/must_not_match: kwargs).
    def build_preconditions(preconditions)
      preconditions.map do |pc|
        op = Authzed::Api::V1::Precondition::Operation.const_get(PRECONDITION_MAP[pc[:operation]])
        Authzed::Api::V1::Precondition.new(
          operation: op,
          filter: filter_to_proto(pc[:filter])
        )
      end
    end

    # --- Proto client call implementations ---

    # `call_level_context` is the call-level default (from
    # check_permissions' `context:` keyword); each item's own
    # `check_context` (if any) is merged on top per `merge_check_context` —
    # CheckBulkPermissionsRequest has no context field of its own (proto
    # ground truth: only CheckBulkPermissionsRequestItem#context, field 4,
    # carries context on the wire), so the merged context must be built and
    # attached per item here.
    def call_bulk_check(consistency, items, call_level_context = nil)
      proto_items = items.map do |item|
        item_args = {
          resource: Authzed::Api::V1::ObjectReference.new(
            object_type: item[:resource_type],
            object_id: item[:resource_id]
          ),
          permission: item[:permission],
          subject: Authzed::Api::V1::SubjectReference.new(
            object: Authzed::Api::V1::ObjectReference.new(
              object_type: item[:subject_type],
              object_id: item[:subject_id]
            ),
            optional_relation: item[:subject_relation] || ''
          )
        }

        merged_context = merge_check_context(call_level_context, item[:check_context])
        struct = check_context_to_struct(merged_context)
        item_args[:context] = struct if struct

        Authzed::Api::V1::CheckBulkPermissionsRequestItem.new(**item_args)
      end

      resp = @proto_client.permissions.check_bulk_permissions(
        Authzed::Api::V1::CheckBulkPermissionsRequest.new(
          consistency: build_consistency(consistency),
          items: proto_items
        )
      )

      # CheckBulkPermissionsResponse#checked_at is a single response-level
      # ZedToken (not per-pair) — propagate it to every CheckResult below.
      # Message-typed proto fields are nil when unset (unlike scalar
      # fields), so guard with `&.` — ZedTokens are documented as opaque,
      # never-nil Strings, so fall back to ''.
      checked_at = resp.checked_at&.token || ''

      resp.pairs.map do |pair|
        raise SpiceDB.to_spicedb_error(pair.error) if pair.respond_to?(:error) && pair.error && pair.error.respond_to?(:message) && !pair.error.message.empty?

        CheckResult.new(
          permissionship: check_permissionship_from_proto(pair.item.permissionship),
          missing_context: missing_context_from_proto(pair.item.partial_caveat_info),
          checked_at: checked_at
        )
      end
    end

    def call_write_relationships(transaction)
      updates = transaction.updates.map do |update|
        op = Authzed::Api::V1::RelationshipUpdate::Operation.const_get(OPERATION_MAP[update[:operation]])
        Authzed::Api::V1::RelationshipUpdate.new(
          operation: op,
          relationship: relationship_to_proto(update[:relationship])
        )
      end

      preconditions = build_preconditions(transaction.preconditions)

      req_args = { updates: updates }
      req_args[:optional_preconditions] = preconditions unless preconditions.empty?

      resp = @proto_client.permissions.write_relationships(
        Authzed::Api::V1::WriteRelationshipsRequest.new(**req_args)
      )

      resp.written_at.token
    end

    def call_read_relationships(consistency, filter, cursor, page_size)
      req_args = {
        consistency: build_consistency(consistency),
        relationship_filter: filter_to_proto(filter),
        optional_limit: page_size
      }
      req_args[:optional_cursor] = Authzed::Api::V1::Cursor.new(token: cursor) if cursor

      relationships = []
      new_cursor = nil
      count = 0

      @proto_client.permissions.read_relationships(
        Authzed::Api::V1::ReadRelationshipsRequest.new(**req_args)
      ).each do |resp|
        relationships << relationship_from_proto(resp.relationship)
        new_cursor = resp.after_result_cursor.token
        count += 1
      end

      [relationships, new_cursor, count]
    end

    def call_delete_relationships(filter, page_size, preconditions = [])
      resp = @proto_client.permissions.delete_relationships(
        Authzed::Api::V1::DeleteRelationshipsRequest.new(
          relationship_filter: filter_to_proto(filter),
          optional_preconditions: build_preconditions(preconditions),
          optional_limit: page_size,
          optional_allow_partial_deletions: true
        )
      )

      revision = resp.deleted_at.token
      complete = resp.deletion_progress == :DELETION_PROGRESS_COMPLETE
      [revision, complete]
    end

    def call_lookup_resources(consistency, resource_type, permission, subject_type, subject_id, cursor, page_size)
      req_args = {
        consistency: build_consistency(consistency),
        resource_object_type: resource_type,
        permission: permission,
        subject: Authzed::Api::V1::SubjectReference.new(
          object: Authzed::Api::V1::ObjectReference.new(
            object_type: subject_type,
            object_id: subject_id
          )
        ),
        optional_limit: page_size
      }
      req_args[:optional_cursor] = Authzed::Api::V1::Cursor.new(token: cursor) if cursor

      resources = []
      new_cursor = nil
      count = 0

      @proto_client.permissions.lookup_resources(
        Authzed::Api::V1::LookupResourcesRequest.new(**req_args)
      ).each do |resp|
        resources << lookup_resource_from_proto(resp)
        new_cursor = resp.after_result_cursor.token
        count += 1
      end

      [resources, new_cursor, count]
    end

    def call_lookup_subjects(consistency, resource_type, resource_id, permission, subject_type)
      req = Authzed::Api::V1::LookupSubjectsRequest.new(
        consistency: build_consistency(consistency),
        resource: Authzed::Api::V1::ObjectReference.new(
          object_type: resource_type,
          object_id: resource_id
        ),
        permission: permission,
        subject_object_type: subject_type
      )

      @proto_client.permissions.lookup_subjects(req).each do |resp|
        yield lookup_subject_from_proto(resp)
      end
    end

    def call_read_schema
      resp = @proto_client.schema.read_schema(
        Authzed::Api::V1::ReadSchemaRequest.new
      )
      [resp.schema_text, resp.read_at.token]
    end

    def call_write_schema(schema)
      resp = @proto_client.schema.write_schema(
        Authzed::Api::V1::WriteSchemaRequest.new(schema: schema)
      )
      resp.written_at.token
    end

    def call_reflect_schema(consistency)
      resp = @proto_client.schema.reflect_schema(
        Authzed::Api::V1::ReflectSchemaRequest.new(
          consistency: build_consistency(consistency)
        )
      )

      definitions = resp.definitions.map do |defn|
        relations = defn.relations.map do |rel|
          SchemaRelation.new(
            name: rel.name,
            comment: rel.comment,
            parent_definition_name: rel.parent_definition_name
          )
        end
        permissions = defn.permissions.map do |perm|
          SchemaPermission.new(
            name: perm.name,
            comment: perm.comment,
            parent_definition_name: perm.parent_definition_name
          )
        end
        SchemaDefinition.new(
          name: defn.name,
          comment: defn.comment,
          relations: relations,
          permissions: permissions
        )
      end

      caveats = resp.caveats.map do |cav|
        params = cav.parameters.map do |param|
          SchemaCaveatParameter.new(
            name: param.name,
            type: param.type,
            parent_caveat_name: param.parent_caveat_name
          )
        end
        SchemaCaveat.new(
          name: cav.name,
          comment: cav.comment,
          expression: cav.expression,
          parameters: params
        )
      end

      ReflectSchemaResult.new(
        definitions: definitions,
        caveats: caveats,
        revision: resp.read_at.token
      )
    end

    def call_computable_permissions(consistency, definition_name, relation_name)
      resp = @proto_client.schema.computable_permissions(
        Authzed::Api::V1::ComputablePermissionsRequest.new(
          consistency: build_consistency(consistency),
          definition_name: definition_name,
          relation_name: relation_name
        )
      )

      refs = resp.permissions.map do |perm|
        RelationReference.new(
          definition_name: perm.definition_name,
          relation_name: perm.relation_name,
          is_permission: perm.is_permission
        )
      end

      [refs, resp.read_at.token]
    end

    def call_dependent_relations(consistency, definition_name, permission_name)
      resp = @proto_client.schema.dependent_relations(
        Authzed::Api::V1::DependentRelationsRequest.new(
          consistency: build_consistency(consistency),
          definition_name: definition_name,
          permission_name: permission_name
        )
      )

      refs = resp.relations.map do |rel|
        RelationReference.new(
          definition_name: rel.definition_name,
          relation_name: rel.relation_name,
          is_permission: rel.is_permission
        )
      end

      [refs, resp.read_at.token]
    end

    def call_diff_schema(consistency, comparison_schema)
      resp = @proto_client.schema.diff_schema(
        Authzed::Api::V1::DiffSchemaRequest.new(
          consistency: build_consistency(consistency),
          comparison_schema: comparison_schema
        )
      )

      diffs = resp.diffs.map { |d| schema_diff_from_proto(d) }
      [diffs, resp.read_at.token]
    end

    def call_expand_permission_tree(consistency, resource_type, resource_id, permission)
      resp = @proto_client.permissions.expand_permission_tree(
        Authzed::Api::V1::ExpandPermissionTreeRequest.new(
          consistency: build_consistency(consistency),
          resource: Authzed::Api::V1::ObjectReference.new(
            object_type: resource_type,
            object_id: resource_id
          ),
          permission: permission
        )
      )

      ExpandResult.new(
        tree: permission_tree_from_proto(resp.tree_root),
        revision: resp.expanded_at.token
      )
    end

    def call_import_relationships(relationships)
      batch = []

      # Ruby gRPC client streaming uses an Enumerable of requests
      requests = Enumerator.new do |yielder|
        relationships.each do |rel|
          batch << relationship_to_proto(rel)
          next unless batch.size >= DEFAULT_IMPORT_BATCH_SIZE

          yielder << Authzed::Api::V1::ImportBulkRelationshipsRequest.new(
            relationships: batch
          )
          batch = []
        end
        # Send remaining batch
        unless batch.empty?
          yielder << Authzed::Api::V1::ImportBulkRelationshipsRequest.new(
            relationships: batch
          )
        end
      end

      resp = @proto_client.permissions.import_bulk_relationships(requests)
      resp.num_loaded
    end

    def call_export_relationships(consistency, filter, cursor, page_size)
      req_args = {
        consistency: build_consistency(consistency),
        optional_limit: page_size
      }
      req_args[:optional_relationship_filter] = filter_to_proto(filter) if filter
      req_args[:optional_cursor] = Authzed::Api::V1::Cursor.new(token: cursor) if cursor

      relationships = []
      new_cursor = nil
      count = 0

      @proto_client.permissions.export_bulk_relationships(
        Authzed::Api::V1::ExportBulkRelationshipsRequest.new(**req_args)
      ).each do |resp|
        new_cursor = resp.after_result_cursor.token
        resp.relationships.each do |proto_rel|
          relationships << relationship_from_proto(proto_rel)
          count += 1
        end
      end

      [relationships, new_cursor, count]
    end

    def call_watch(object_types, start_revision)
      require_proto_client!
      req_args = { optional_object_types: object_types }
      req_args[:optional_start_cursor] = Authzed::Api::V1::ZedToken.new(token: start_revision) if start_revision && !start_revision.empty?

      @proto_client.watch.watch(
        Authzed::Api::V1::WatchRequest.new(**req_args)
      ).each do |resp|
        resp.updates.each do |update|
          op = case update.operation
               when :OPERATION_CREATE then :create
               when :OPERATION_TOUCH then :touch
               when :OPERATION_DELETE then :delete
               else :unknown
               end
          yield Update.new(
            operation: op,
            relationship: relationship_from_proto(update.relationship)
          )
        end
      end
    end

    def call_register_relationship_counter(name, filter)
      @proto_client.experimental.experimental_register_relationship_counter(
        Authzed::Api::V1::ExperimentalRegisterRelationshipCounterRequest.new(
          name: name,
          relationship_filter: filter_to_proto(filter)
        )
      )
    end

    def call_count_relationships(name)
      resp = @proto_client.experimental.experimental_count_relationships(
        Authzed::Api::V1::ExperimentalCountRelationshipsRequest.new(
          name: name
        )
      )

      if resp.counter_still_calculating
        return CountResult.new(
          relationship_count: 0,
          revision: '',
          still_calculating: true
        )
      end

      cv = resp.read_counter_value
      CountResult.new(
        relationship_count: cv.relationship_count,
        revision: cv.read_at.token,
        still_calculating: false
      )
    end

    def call_unregister_relationship_counter(name)
      @proto_client.experimental.experimental_unregister_relationship_counter(
        Authzed::Api::V1::ExperimentalUnregisterRelationshipCounterRequest.new(
          name: name
        )
      )
    end

    # Maps the proto AlgebraicSubjectSet operation enum to a native symbol.
    PERMISSION_TREE_OPERATION_MAP = {
      OPERATION_UNION: :union,
      OPERATION_INTERSECTION: :intersection,
      OPERATION_EXCLUSION: :exclusion
    }.freeze

    # Recursively maps a proto Authzed::Api::V1::PermissionRelationshipTree to
    # a native SpiceDB::PermissionTree. Returns nil for a nil input.
    #
    # The proto uses a oneof for intermediate/leaf, which in Ruby becomes a
    # set of methods that return nil if not set.
    def permission_tree_from_proto(t)
      return nil if t.nil?

      # NOTE: use bracket access for object_id — Authzed::Api::V1::ObjectReference#object_id
      # collides with Kernel#object_id, so the dot-accessor returns Ruby's internal object
      # identity instead of the protobuf field (see also relationship_from_proto below).
      expanded_object = ObjectRef.new(
        object_type: t.expanded_object&.object_type,
        object_id: t.expanded_object&.[]('object_id')
      )

      intermediate = nil
      leaf = nil

      if (v = t.intermediate)
        intermediate = IntermediateNode.new(
          operation: PERMISSION_TREE_OPERATION_MAP.fetch(v.operation, :unspecified),
          children: v.children.map { |child| permission_tree_from_proto(child) }
        )
      elsif (v = t.leaf)
        leaf = LeafNode.new(
          subjects: v.subjects.map { |subject| subject_ref_from_proto(subject) }
        )
      end

      PermissionTree.new(
        expanded_object: expanded_object,
        expanded_relation: t.expanded_relation,
        intermediate: intermediate,
        leaf: leaf
      )
    end

    # Maps a proto Authzed::Api::V1::SubjectReference to a SpiceDB::SubjectRef.
    def subject_ref_from_proto(subject)
      SubjectRef.new(
        subject_type: subject.object&.object_type,
        subject_id: subject.object&.[]('object_id'),
        optional_relation: subject.optional_relation
      )
    end

    # Maps the proto LookupPermissionship enum (returned as a Symbol by the
    # Ruby protobuf gem) to a native permissionship symbol. Unrecognized
    # values (including UNSPECIFIED and nil) map to :unspecified.
    LOOKUP_PERMISSIONSHIP_MAP = {
      LOOKUP_PERMISSIONSHIP_HAS_PERMISSION: :has_permission,
      LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION: :conditional_permission
    }.freeze

    # Maps the proto LookupPermissionship enum to its native symbol
    # equivalent. Callers MUST check this before treating a lookup result as
    # a full grant — a :conditional_permission result may resolve to false
    # once the missing caveat context is supplied.
    def permissionship_from_proto(v)
      LOOKUP_PERMISSIONSHIP_MAP.fetch(v, :unspecified)
    end

    # Maps the proto CheckPermissionResponse::Permissionship enum (returned
    # as a Symbol by the Ruby protobuf gem) to the native permissionship
    # symbol used by CheckResult. Unlike LOOKUP_PERMISSIONSHIP_MAP above,
    # this proto enum is four-valued — it can affirmatively report
    # PERMISSIONSHIP_NO_PERMISSION — matching CheckResult#has_permission?'s
    # fail-closed contract (only :has_permission is true). Unrecognized
    # values (including UNSPECIFIED and nil) map to :unspecified.
    CHECK_PERMISSIONSHIP_MAP = {
      PERMISSIONSHIP_NO_PERMISSION: :no_permission,
      PERMISSIONSHIP_HAS_PERMISSION: :has_permission,
      PERMISSIONSHIP_CONDITIONAL_PERMISSION: :conditional_permission
    }.freeze

    # Maps the proto CheckPermissionResponse::Permissionship enum to its
    # native symbol equivalent (see CHECK_PERMISSIONSHIP_MAP above).
    def check_permissionship_from_proto(v)
      CHECK_PERMISSIONSHIP_MAP.fetch(v, :unspecified)
    end

    # Maps a proto PartialCaveatInfo to a native SpiceDB::PartialCaveatInfo.
    # A nil input (the field is unset) maps to nil.
    def partial_caveat_from_proto(v)
      return nil if v.nil?

      PartialCaveatInfo.new(missing_required_context: v.missing_required_context.to_a)
    end

    # Maps a proto PartialCaveatInfo to the flat Array<String>
    # CheckResult#missing_context expects. Unlike partial_caveat_from_proto
    # above (which wraps the value in a PartialCaveatInfo for the lookup
    # surfaces), CheckResult exposes the missing-context list directly,
    # since it's the only caveat detail a check result carries. A nil input
    # (the field is unset — i.e. the check wasn't conditional) maps to [].
    def missing_context_from_proto(v)
      return [] if v.nil?

      v.missing_required_context.to_a
    end

    # Maps a proto ResolvedSubject to a native SpiceDB::ResolvedSubject. A
    # nil input maps to a zero-value ResolvedSubject (nil subject_id), which
    # callers use as the trigger for falling back to deprecated
    # response-level fields.
    def resolved_subject_from_proto(v)
      ResolvedSubject.new(
        subject_id: v&.subject_object_id,
        permissionship: permissionship_from_proto(v&.permissionship),
        partial_caveat: partial_caveat_from_proto(v&.partial_caveat_info)
      )
    end

    # Maps a proto LookupResourcesResponse to a native SpiceDB::LookupResource.
    def lookup_resource_from_proto(resp)
      LookupResource.new(
        resource_id: resp.resource_object_id,
        permissionship: permissionship_from_proto(resp.permissionship),
        partial_caveat: partial_caveat_from_proto(resp.partial_caveat_info),
        looked_up_at: resp.looked_up_at&.token || ''
      )
    end

    # Maps the excluded-subjects list off a proto LookupSubjectsResponse,
    # preferring the non-deprecated `excluded_subjects` (which carries
    # permissionship + caveat info per subject) and falling back to the
    # deprecated `excluded_subject_ids` (IDs only) for servers that don't yet
    # populate the newer field.
    def excluded_subjects_from_proto(resp)
      return resp.excluded_subjects.map { |e| resolved_subject_from_proto(e) } unless resp.excluded_subjects.empty?

      unless resp.excluded_subject_ids.empty?
        return resp.excluded_subject_ids.map do |id|
          ResolvedSubject.new(subject_id: id, permissionship: :unspecified, partial_caveat: nil)
        end
      end

      []
    end

    # Maps a proto LookupSubjectsResponse to a native SpiceDB::LookupSubject.
    # When the result's Subject#subject_id is the wildcard "*",
    # ExcludedSubjects lists subjects excluded from that wildcard grant —
    # callers MUST treat those subjects as NOT having the permission, even
    # though the wildcard would otherwise suggest they do.
    def lookup_subject_from_proto(resp)
      subject = resolved_subject_from_proto(resp.subject)
      if subject.subject_id.nil? || subject.subject_id.empty?
        # Fall back to the deprecated top-level fields for servers that
        # don't yet populate the non-deprecated `subject` field.
        subject = ResolvedSubject.new(
          subject_id: resp.subject_object_id,
          permissionship: permissionship_from_proto(resp.permissionship),
          partial_caveat: partial_caveat_from_proto(resp.partial_caveat_info)
        )
      end

      LookupSubject.new(
        subject: subject,
        excluded_subjects: excluded_subjects_from_proto(resp),
        looked_up_at: resp.looked_up_at&.token || ''
      )
    end

    # Maps a proto ReflectionSchemaDiff to a SpiceDB::SchemaDiff.
    # The proto uses a oneof for the diff type, which in Ruby becomes
    # a set of methods that return nil if not set.
    def schema_diff_from_proto(d)
      # Try each possible diff type in order
      if (v = d.definition_added)
        SchemaDiff.new(kind: 'definition_added', definition_name: v.name)
      elsif (v = d.definition_removed)
        SchemaDiff.new(kind: 'definition_removed', definition_name: v.name)
      elsif (v = d.definition_doc_comment_changed)
        SchemaDiff.new(kind: 'definition_doc_comment_changed', definition_name: v.name)
      elsif (v = d.relation_added)
        SchemaDiff.new(kind: 'relation_added', definition_name: v.parent_definition_name, relation_name: v.name)
      elsif (v = d.relation_removed)
        SchemaDiff.new(kind: 'relation_removed', definition_name: v.parent_definition_name, relation_name: v.name)
      elsif (v = d.relation_doc_comment_changed)
        SchemaDiff.new(kind: 'relation_doc_comment_changed', definition_name: v.parent_definition_name,
                       relation_name: v.name)
      elsif (v = d.relation_subject_type_added)
        SchemaDiff.new(kind: 'relation_subject_type_added', definition_name: v.relation.parent_definition_name,
                       relation_name: v.relation.name)
      elsif (v = d.relation_subject_type_removed)
        SchemaDiff.new(kind: 'relation_subject_type_removed', definition_name: v.relation.parent_definition_name,
                       relation_name: v.relation.name)
      elsif (v = d.permission_added)
        SchemaDiff.new(kind: 'permission_added', definition_name: v.parent_definition_name, permission_name: v.name)
      elsif (v = d.permission_removed)
        SchemaDiff.new(kind: 'permission_removed', definition_name: v.parent_definition_name, permission_name: v.name)
      elsif (v = d.permission_doc_comment_changed)
        SchemaDiff.new(kind: 'permission_doc_comment_changed', definition_name: v.parent_definition_name,
                       permission_name: v.name)
      elsif (v = d.permission_expr_changed)
        SchemaDiff.new(kind: 'permission_expr_changed', definition_name: v.parent_definition_name,
                       permission_name: v.name)
      elsif (v = d.caveat_added)
        SchemaDiff.new(kind: 'caveat_added', caveat_name: v.name)
      elsif (v = d.caveat_removed)
        SchemaDiff.new(kind: 'caveat_removed', caveat_name: v.name)
      elsif (v = d.caveat_doc_comment_changed)
        SchemaDiff.new(kind: 'caveat_doc_comment_changed', caveat_name: v.name)
      elsif (v = d.caveat_expr_changed)
        SchemaDiff.new(kind: 'caveat_expr_changed', caveat_name: v.name)
      elsif (v = d.caveat_parameter_added)
        SchemaDiff.new(kind: 'caveat_parameter_added', caveat_name: v.parent_caveat_name)
      elsif (v = d.caveat_parameter_removed)
        SchemaDiff.new(kind: 'caveat_parameter_removed', caveat_name: v.parent_caveat_name)
      elsif (v = d.caveat_parameter_type_changed)
        SchemaDiff.new(kind: 'caveat_parameter_type_changed', caveat_name: v.parameter.parent_caveat_name)
      else
        SchemaDiff.new(kind: 'unknown')
      end
    end
  end
end
