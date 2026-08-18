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
  # A relationship mutation observed via #watch. `operation` is one of
  # :create, :touch, :delete, or :unspecified. :unspecified means the server
  # sent an operation this client does not recognize — either
  # OPERATION_UNSPECIFIED on the wire, or a future operation value added after
  # this client shipped. Never treat it as a write: a cache or index mirror
  # that upserts on an unrecognized operation could turn a delete it doesn't
  # understand into a silent write.
  Update = Data.define(:operation, :relationship)

  # A single event from #updates, corresponding to one WatchResponse from the
  # server. `changes_through` is always populated -- it's the ZedToken
  # (String) this event is current through; pass it as `start_revision` to a
  # later `#updates` call to resume after a dropped stream, instead of
  # restarting from the original `start_revision` (reprocessing everything
  # since, possibly past the GC window) or from head (silently losing every
  # change in the gap). `is_checkpoint` is true for a checkpoint event, which
  # carries no `updates` -- it exists only to advertise a fresh
  # `changes_through` and, behind a proxy that aborts idle connections, to
  # keep the stream alive. Checkpoints are only sent when
  # `include_checkpoints: true` is passed to `#updates`.
  WatchEvent = Data.define(:updates, :changes_through, :is_checkpoint)

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
    # check_context_to_struct, caveat_context_to_struct, check_context_value,
    # caveat_context_value_from_proto, and struct_to_caveat_context all come
    # from here — the caveat-context codec shared by the check and write
    # surfaces. module_function on CaveatContext keeps them private instance
    # methods here, matching their visibility before the extraction.
    include SpiceDB::CaveatContext

    # with_retry, call_once, backoff_delay, MAX_RETRIES, and
    # BASE_RETRY_DELAY all come from here -- see {SpiceDB::Retrying}'s
    # module doc for the retry-safety rule they implement.
    include SpiceDB::Retrying

    # watch_request, watch_event_from_proto, and watch_update_from_proto
    # (the #updates/#call_watch request-building and response-mapping
    # helpers) come from here -- see {SpiceDB::WatchMapping}'s module doc.
    include SpiceDB::WatchMapping

    # Default page sizes for transparent cursor pagination.
    DEFAULT_READ_PAGE_SIZE   = 512
    DEFAULT_LOOKUP_PAGE_SIZE = 512
    DEFAULT_EXPORT_PAGE_SIZE = 512
    DEFAULT_DELETE_PAGE_SIZE = 1_000
    DEFAULT_IMPORT_BATCH_SIZE = 1_000
    DEFAULT_CHECK_BATCH_SIZE = 1_000

    # Applied to every unary call that does not pass its own `timeout:`.
    #
    # Mirrors authzed-node's `DEFAULT_DEADLINE_MS = 30_000` (its comment cites
    # grpc/grpc-node#541, a known gRPC failure mode where a channel that
    # accepts a connection but never answers produces no error at all).
    # Without a finite default, a wedged SpiceDB hangs every caller that
    # didn't opt in to a timeout -- in practice, most callers -- forever: the
    # connection looks fine at the transport level, so nothing ever times out
    # and nothing is ever produced to retry. See root DESIGN.md, "RULE: A
    # unary call must have a deadline".
    #
    # Deliberately NOT applied to streaming calls (#read_relationships,
    # #lookup_resources, #lookup_subjects, #updates, #export_relationships)
    # -- those are long-lived by design, and applying this default to them
    # would make the stream itself the outage (DESIGN.md, "Streaming calls
    # MUST NOT inherit the unary default").
    DEFAULT_TIMEOUT_SECONDS = 30

    # @return [Object] the underlying proto client for advanced use cases
    attr_reader :proto_client

    # Creates a client with an insecure (plaintext) connection.
    # Use this for testing only — the lack of TLS is made obvious by the name.
    #
    # If a block is given, the client is yielded and cleanup is ensured.
    #
    # @param endpoint [String] the SpiceDB server address (e.g., "localhost:50051")
    # @param token [String] the preshared key for authentication
    # @param default_timeout [Numeric] seconds applied to every unary call that
    #   does not pass its own `timeout:` (see {DEFAULT_TIMEOUT_SECONDS}). NOT
    #   applied to streaming calls.
    # @yield [client] optionally yields the client for block-scoped usage
    # @return [Client]
    def self.new_plaintext(endpoint, token, default_timeout: DEFAULT_TIMEOUT_SECONDS, &block)
      client = new(endpoint: endpoint, token: token, insecure: true, default_timeout: default_timeout)
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
    # @param default_timeout [Numeric] seconds applied to every unary call that
    #   does not pass its own `timeout:` (see {DEFAULT_TIMEOUT_SECONDS}). NOT
    #   applied to streaming calls.
    # @yield [client] optionally yields the client for block-scoped usage
    # @return [Client]
    def self.new_system_tls(endpoint, token, default_timeout: DEFAULT_TIMEOUT_SECONDS, &block)
      client = new(endpoint: endpoint, token: token, insecure: false, default_timeout: default_timeout)
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
    def initialize(endpoint:, token:, insecure: false, default_timeout: DEFAULT_TIMEOUT_SECONDS)
      @endpoint = endpoint
      @token = token
      @insecure = insecure
      @default_timeout = default_timeout

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
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [SpiceDB::CheckResult]
    def check_permission(consistency, permission, relationship, context: nil, timeout: nil)
      check_permissions(consistency, permission, relationship, context: context, timeout: timeout).first
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
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [Array<SpiceDB::CheckResult>]
    def check_permissions(consistency, permission, *relationships, context: nil, timeout: nil)
      relationships = relationships.flatten
      return [] if relationships.empty?

      with_retry do
        # Delegate to proto client BulkCheckPermissions
        # Each relationship becomes a CheckBulkPermissionsRequestItem
        items = relationships.map { |r| check_item_from_rel(r, permission) }

        call_bulk_check(consistency, items, context, effective_timeout(timeout))
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
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [Boolean]
    def check_any(consistency, permission, *relationships, context: nil, timeout: nil)
      check_permissions(consistency, permission, *relationships, context: context, timeout: timeout).any?(&:has_permission?)
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
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [Boolean]
    #
    # Returns false, not the vacuous true Enumerable#all? yields on an empty
    # collection, when +relationships+ is empty -- "no checks to run" is not
    # "all checks passed".
    def check_all(consistency, permission, *relationships, context: nil, timeout: nil)
      relationships = relationships.flatten
      return false if relationships.empty?

      check_permissions(consistency, permission, *relationships, context: context, timeout: timeout).all?(&:has_permission?)
    end

    # --- Relationships ---

    # Commits a transaction of relationship mutations to SpiceDB.
    #
    # @param transaction [SpiceDB::Transaction]
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [String] the revision at which the write occurred
    def write(transaction, timeout: nil)
      call_once do
        call_write_relationships(transaction, effective_timeout(timeout))
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
    # @param timeout [Numeric, nil] seconds bounding EACH page's call,
    #   overriding the client's default_timeout
    # @return [String] the revision of the final deletion
    def delete_relationships(filter, must_match: [], must_not_match: [], limit: nil, timeout: nil)
      preconditions = must_match.map { |f| { operation: :must_match, filter: f } } +
                      must_not_match.map { |f| { operation: :must_not_match, filter: f } }
      page_size = limit || DEFAULT_DELETE_PAGE_SIZE
      t = effective_timeout(timeout)

      revision = nil
      loop do
        rev, complete = call_once do
          call_delete_relationships(filter, page_size, preconditions, t)
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
      require_proto_client!
      Enumerator.new do |yielder|
        call_lookup_subjects(consistency, resource_type, resource_id, permission, subject_type) do |lookup_subject|
          yielder << lookup_subject
        end
      rescue StandardError => e
        # We intentionally do NOT retry LookupSubjects here (mirrors #updates
        # below) -- the proto marks its cursor unimplemented, so unlike
        # #lookup_resources/#export_relationships there is no resume
        # mechanism, and retrying a mid-stream failure would re-yield
        # results the caller has already seen. Mapping the error to a
        # native SpiceDB::* type (instead of leaking a raw GRPC::BadStatus)
        # is the correctness fix; retry is left to the caller.
        raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

        raise
      end
    end

    # --- Schema ---

    # Returns the current SpiceDB schema.
    #
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [Array(String, String)] the schema text and revision
    def read_schema(timeout: nil)
      with_retry { call_read_schema(effective_timeout(timeout)) }
    end

    # Writes a new schema to SpiceDB.
    #
    # @param schema [String] the schema text
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [String] the revision
    def write_schema(schema, timeout: nil)
      call_once { call_write_schema(schema, effective_timeout(timeout)) }
    end

    # Returns the definitions and caveats in the current schema.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [SpiceDB::ReflectSchemaResult]
    def reflect_schema(consistency, timeout: nil)
      with_retry { call_reflect_schema(consistency, effective_timeout(timeout)) }
    end

    # Returns the permissions that are computable for the given relation.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param definition_name [String]
    # @param relation_name [String]
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [Array(Array<SpiceDB::RelationReference>, String)] references and revision
    def computable_permissions(consistency, definition_name, relation_name, timeout: nil)
      with_retry { call_computable_permissions(consistency, definition_name, relation_name, effective_timeout(timeout)) }
    end

    # Returns the relations that the given permission depends on.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param definition_name [String]
    # @param permission_name [String]
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [Array(Array<SpiceDB::RelationReference>, String)] references and revision
    def dependent_relations(consistency, definition_name, permission_name, timeout: nil)
      with_retry { call_dependent_relations(consistency, definition_name, permission_name, effective_timeout(timeout)) }
    end

    # Compares the current schema against the given comparison schema.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param comparison_schema [String]
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [Array(Array<SpiceDB::SchemaDiff>, String)] diffs and revision
    def diff_schema(consistency, comparison_schema, timeout: nil)
      with_retry { call_diff_schema(consistency, comparison_schema, effective_timeout(timeout)) }
    end

    # --- Expand ---

    # Expands the permission tree for the given resource and permission.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param resource_type [String]
    # @param resource_id [String]
    # @param permission [String]
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [SpiceDB::ExpandResult]
    def expand_permission_tree(consistency, resource_type, resource_id, permission, timeout: nil)
      with_retry { call_expand_permission_tree(consistency, resource_type, resource_id, permission, effective_timeout(timeout)) }
    end

    # --- Bulk ---

    # Streams relationships to SpiceDB for bulk import. Relationships are
    # automatically batched into chunks of 1,000.
    #
    # `import_bulk_relationships` is client-streaming, not unary: its
    # duration scales with the size of `relationships`, not with server
    # latency, so it does NOT inherit `default_timeout` (root DESIGN.md,
    # "RULE: A unary call must have a deadline", clause 3). Passing no
    # `timeout` here means this call is unbounded; pass `timeout` to bound
    # it explicitly.
    #
    # @param relationships [Enumerable<SpiceDB::Relationship>]
    # @param timeout [Numeric, nil] seconds bounding this call (no default)
    # @return [Integer] the number of relationships loaded
    def import_relationships(relationships, timeout: nil)
      call_once { call_import_relationships(relationships, timeout) }
    end

    # Returns an Enumerator over all relationships matching the optional filter,
    # streamed from SpiceDB in bulk. Cursors are handled transparently.
    #
    # Unlike every other paginated call on this client (whose page-size field
    # bounds the WHOLE server stream for that call, ending it once that many
    # results have been returned), ExportBulkRelationships' page-size field
    # (`optional_limit`) bounds only the number of relationships the server
    # puts in a SINGLE response message -- the server keeps streaming further
    # messages on the SAME call until the whole dataset has been sent (or the
    # client stops consuming). The outer per-page loop/cursor shape below is
    # unchanged from -- and correct for -- every other paginated method on
    # this client; what was wrong was `call_export_relationships` collecting
    # a full page into an Array and returning it only once fully drained,
    # which -- given the semantics above -- meant draining the ENTIRE export
    # into memory before this Enumerator yielded a single relationship. An
    # OOM risk for the one API most likely to face the largest dataset in the
    # system. `call_export_relationships` now yields each relationship
    # directly off the wire as its response message arrives, so the first
    # relationship is available immediately regardless of how large the
    # export is or whether the server has finished sending it.
    #
    # Establishment retry applies ONLY while zero items have been yielded
    # from the current page's open stream -- retrying after any item has
    # been yielded would replay/duplicate it, since a retry re-runs the
    # whole block (including its `yielder <<` side effects) rather than
    # resuming a partially consumed stream.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param filter [SpiceDB::Filter, nil] optional filter
    # @return [Enumerator<SpiceDB::Relationship>]
    def export_relationships(consistency, filter = nil)
      Enumerator.new do |yielder|
        cursor = nil
        loop do
          count = 0
          attempt = 0
          begin
            call_export_relationships(consistency, filter, cursor, DEFAULT_EXPORT_PAGE_SIZE) do |rel, new_cursor|
              yielder << rel
              count += 1
              cursor = new_cursor
            end
          rescue StandardError => e
            if count.zero? && should_retry_establishment?(attempt, e)
              attempt += 1
              retry
            end
            raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

            raise
          end

          break if count < DEFAULT_EXPORT_PAGE_SIZE
        end
      end
    end

    # --- Watch ---

    # Returns an Enumerator over watch events from SpiceDB's watch API. Each
    # yielded SpiceDB::WatchEvent corresponds to one server response: zero or
    # more relationship updates, all current through
    # SpiceDB::WatchEvent#changes_through.
    #
    # @param object_types [Array<String>] object types to watch
    # @param start_revision [String, nil] optional revision to start from
    # @param include_checkpoints [Boolean] also request periodic checkpoint
    #   events (SpiceDB::WatchEvent#is_checkpoint, no updates). Recommended
    #   if this SpiceDB instance is running behind a proxy that aborts idle
    #   connections, since a checkpoint keeps the stream alive even when
    #   there are no changes.
    # @return [Enumerator<SpiceDB::WatchEvent>]
    def updates(object_types, start_revision: nil, include_checkpoints: false)
      Enumerator.new do |yielder|
        call_watch(object_types, start_revision, include_checkpoints) do |event|
          yielder << event
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
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [nil]
    def experimental_register_relationship_counter(name, filter, timeout: nil)
      call_once { call_register_relationship_counter(name, filter, effective_timeout(timeout)) }
      nil
    end

    # Reads the value of a previously registered relationship counter.
    #
    # @note Experimental: wraps SpiceDB's ExperimentalService and may change without following the backwards-compatibility mandate.
    #
    # @param name [String] counter name
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [SpiceDB::CountResult]
    def experimental_count_relationships(name, timeout: nil)
      with_retry { call_count_relationships(name, effective_timeout(timeout)) }
    end

    # Removes a previously registered relationship counter.
    #
    # @note Experimental: wraps SpiceDB's ExperimentalService and may change without following the backwards-compatibility mandate.
    #
    # @param name [String] counter name
    # @param timeout [Numeric, nil] seconds bounding this call, overriding
    #   the client's default_timeout
    # @return [nil]
    def experimental_unregister_relationship_counter(name, timeout: nil)
      call_once { call_unregister_relationship_counter(name, effective_timeout(timeout)) }
      nil
    end

    private

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
    #
    # @raise [SpiceDB::InvalidArgumentError] if +subject_id+ or +subject_relation+ is set without
    #   +subject_type+. The wire's SubjectFilter.subject_type is a required field, so there is no
    #   way to express a subject ID/relation constraint without it, which makes silently dropping
    #   the constraint the one unsafe resolution: a caller who wrote
    #   +Filter.new(resource_type: 'document', subject_id: 'alice')+, expecting to narrow to
    #   alice's relationships, would instead match every subject on every document -- e.g.
    #   +delete_relationships+ would delete every relationship on every document, not just
    #   alice's. See root DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail",
    #   clause 1.
    def filter_to_proto(filter)
      args = { resource_type: filter.resource_type }
      args[:optional_resource_id] = filter.resource_id if filter.resource_id
      args[:optional_resource_id_prefix] = filter.resource_id_prefix if filter.resource_id_prefix
      args[:optional_relation] = filter.relation if filter.relation

      if filter.subject_type
        subject_filter = { subject_type: filter.subject_type }
        subject_filter[:optional_subject_id] = filter.subject_id if filter.subject_id
        if filter.subject_relation
          subject_filter[:optional_relation] = Authzed::Api::V1::SubjectFilter::RelationFilter.new(
            relation: filter.subject_relation
          )
        end
        args[:optional_subject_filter] = Authzed::Api::V1::SubjectFilter.new(**subject_filter)
      elsif filter.subject_id || filter.subject_relation
        missing = filter.subject_id ? 'subject_id' : 'subject_relation'
        raise SpiceDB::InvalidArgumentError, "Filter has #{missing} set without subject_type -- call with_subject_type before with_#{missing}."
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
        caveat_args[:context] = caveat_context_to_struct(rel.caveat_context) if rel.caveat_context
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
    def call_bulk_check(consistency, items, call_level_context, timeout_seconds)
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
        ),
        deadline: deadline_for(timeout_seconds)
      )

      # The proto guarantees pairs are returned in request order but says
      # nothing about count. A short response would otherwise silently
      # desync results[i] from relationships[i] for every item after the
      # gap — one resource's answer attributed to another. Fail loudly
      # instead of returning a misaligned-but-"successful" Array.
      if resp.pairs.length != proto_items.length
        raise SpiceDB::Error,
              "check_bulk_permissions returned #{resp.pairs.length} pair(s) for " \
              "#{proto_items.length} request item(s)"
      end

      # CheckBulkPermissionsResponse#checked_at is a single response-level
      # ZedToken (not per-pair) — propagate it to every CheckResult below.
      # Message-typed proto fields are nil when unset (unlike scalar
      # fields), so guard with `&.` — ZedTokens are documented as opaque,
      # never-nil Strings, so fall back to ''.
      checked_at = resp.checked_at&.token || ''

      resp.pairs.each_with_index.map do |pair, i|
        raise SpiceDB.to_spicedb_error(pair.error) if pair.respond_to?(:error) && pair.error && pair.error.respond_to?(:message) && !pair.error.message.empty?

        # `response` is the oneof group name — nil means neither `item` nor
        # `error` was set. The proto schema guarantees a well-behaved server
        # never sends this, but nothing on the wire prevents it. Mirrors
        # spicedb-rust's malformed-oneof guard: fail loudly instead of
        # dereferencing `pair.item`, which is nil in this case.
        if pair.response.nil?
          raise SpiceDB::Error,
                "check item #{i}: malformed CheckBulkPermissionsPair (neither item nor error set)"
        end

        CheckResult.new(
          permissionship: check_permissionship_from_proto(pair.item.permissionship),
          missing_context: missing_context_from_proto(pair.item.partial_caveat_info),
          checked_at: checked_at
        )
      end
    end

    def call_write_relationships(transaction, timeout_seconds)
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
        Authzed::Api::V1::WriteRelationshipsRequest.new(**req_args),
        deadline: deadline_for(timeout_seconds)
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

    def call_delete_relationships(filter, page_size, preconditions, timeout_seconds)
      resp = @proto_client.permissions.delete_relationships(
        Authzed::Api::V1::DeleteRelationshipsRequest.new(
          relationship_filter: filter_to_proto(filter),
          optional_preconditions: build_preconditions(preconditions),
          optional_limit: page_size,
          optional_allow_partial_deletions: true
        ),
        deadline: deadline_for(timeout_seconds)
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

    def call_read_schema(timeout_seconds)
      resp = @proto_client.schema.read_schema(
        Authzed::Api::V1::ReadSchemaRequest.new,
        deadline: deadline_for(timeout_seconds)
      )
      [resp.schema_text, resp.read_at.token]
    end

    def call_write_schema(schema, timeout_seconds)
      resp = @proto_client.schema.write_schema(
        Authzed::Api::V1::WriteSchemaRequest.new(schema: schema),
        deadline: deadline_for(timeout_seconds)
      )
      resp.written_at.token
    end

    def call_reflect_schema(consistency, timeout_seconds)
      resp = @proto_client.schema.reflect_schema(
        Authzed::Api::V1::ReflectSchemaRequest.new(
          consistency: build_consistency(consistency)
        ),
        deadline: deadline_for(timeout_seconds)
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

    def call_computable_permissions(consistency, definition_name, relation_name, timeout_seconds)
      resp = @proto_client.schema.computable_permissions(
        Authzed::Api::V1::ComputablePermissionsRequest.new(
          consistency: build_consistency(consistency),
          definition_name: definition_name,
          relation_name: relation_name
        ),
        deadline: deadline_for(timeout_seconds)
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

    def call_dependent_relations(consistency, definition_name, permission_name, timeout_seconds)
      resp = @proto_client.schema.dependent_relations(
        Authzed::Api::V1::DependentRelationsRequest.new(
          consistency: build_consistency(consistency),
          definition_name: definition_name,
          permission_name: permission_name
        ),
        deadline: deadline_for(timeout_seconds)
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

    def call_diff_schema(consistency, comparison_schema, timeout_seconds)
      resp = @proto_client.schema.diff_schema(
        Authzed::Api::V1::DiffSchemaRequest.new(
          consistency: build_consistency(consistency),
          comparison_schema: comparison_schema
        ),
        deadline: deadline_for(timeout_seconds)
      )

      diffs = resp.diffs.map { |d| schema_diff_from_proto(d) }
      [diffs, resp.read_at.token]
    end

    def call_expand_permission_tree(consistency, resource_type, resource_id, permission, timeout_seconds)
      resp = @proto_client.permissions.expand_permission_tree(
        Authzed::Api::V1::ExpandPermissionTreeRequest.new(
          consistency: build_consistency(consistency),
          resource: Authzed::Api::V1::ObjectReference.new(
            object_type: resource_type,
            object_id: resource_id
          ),
          permission: permission
        ),
        deadline: deadline_for(timeout_seconds)
      )

      ExpandResult.new(
        tree: permission_tree_from_proto(resp.tree_root),
        revision: resp.expanded_at.token
      )
    end

    def call_import_relationships(relationships, timeout_seconds)
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

      resp = @proto_client.permissions.import_bulk_relationships(requests, deadline: deadline_for(timeout_seconds))
      resp.num_loaded
    end

    # Yields each matching relationship (and the cursor to resume after it)
    # directly off the wire as response messages arrive -- see the Yardoc on
    # #export_relationships for why this must NOT collect a full response
    # into an Array before returning.
    def call_export_relationships(consistency, filter, cursor, page_size)
      require_proto_client!
      req_args = {
        consistency: build_consistency(consistency),
        optional_limit: page_size
      }
      req_args[:optional_relationship_filter] = filter_to_proto(filter) if filter
      req_args[:optional_cursor] = Authzed::Api::V1::Cursor.new(token: cursor) if cursor

      @proto_client.permissions.export_bulk_relationships(
        Authzed::Api::V1::ExportBulkRelationshipsRequest.new(**req_args)
      ).each do |resp|
        new_cursor = resp.after_result_cursor.token
        resp.relationships.each do |proto_rel|
          yield relationship_from_proto(proto_rel), new_cursor
        end
      end
    end

    def call_watch(object_types, start_revision, include_checkpoints)
      require_proto_client!
      request = watch_request(object_types, start_revision, include_checkpoints)

      @proto_client.watch.watch(request).each do |resp|
        yield watch_event_from_proto(resp)
      end
    end

    def call_register_relationship_counter(name, filter, timeout_seconds)
      @proto_client.experimental.experimental_register_relationship_counter(
        Authzed::Api::V1::ExperimentalRegisterRelationshipCounterRequest.new(
          name: name,
          relationship_filter: filter_to_proto(filter)
        ),
        deadline: deadline_for(timeout_seconds)
      )
    end

    def call_count_relationships(name, timeout_seconds)
      resp = @proto_client.experimental.experimental_count_relationships(
        Authzed::Api::V1::ExperimentalCountRelationshipsRequest.new(
          name: name
        ),
        deadline: deadline_for(timeout_seconds)
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

    def call_unregister_relationship_counter(name, timeout_seconds)
      @proto_client.experimental.experimental_unregister_relationship_counter(
        Authzed::Api::V1::ExperimentalUnregisterRelationshipCounterRequest.new(
          name: name
        ),
        deadline: deadline_for(timeout_seconds)
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
