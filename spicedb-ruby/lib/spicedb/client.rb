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
  ExpandResult = Data.define(:tree_root, :revision)
  CountResult = Data.define(:relationship_count, :revision, :still_calculating)
  Update = Data.define(:operation, :relationship)

  # The idiomatic SpiceDB client for Ruby.
  #
  # Use {.new_plaintext} or {.new_system_tls} to create a client.
  # All read operations require an explicit {SpiceDB::Consistency::Strategy}.
  class Client
    # Default page sizes for transparent cursor pagination.
    DEFAULT_READ_PAGE_SIZE   = 512
    DEFAULT_LOOKUP_PAGE_SIZE = 512
    DEFAULT_EXPORT_PAGE_SIZE = 512
    DEFAULT_DELETE_PAGE_SIZE = 10_000
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
      @proto_client = nil # Will be initialized when proto gem is available
    end

    # Closes the client connection.
    def close
      # Close the underlying gRPC channel when proto client is available
    end

    # --- Checks (all via BulkCheckPermissions) ---

    # Checks a single permission and returns true if granted.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param permission [String] the permission to check
    # @param relationship [SpiceDB::Relationship]
    # @return [Boolean]
    def check_permission(consistency, permission, relationship)
      check_permissions(consistency, permission, relationship).first
    end

    # Performs a bulk permission check on the given relationships and returns
    # a boolean for each relationship indicating whether permission is granted.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param permission [String] the permission to check
    # @param relationships [Array<SpiceDB::Relationship>]
    # @return [Array<Boolean>]
    def check_permissions(consistency, permission, *relationships)
      relationships = relationships.flatten
      return [] if relationships.empty?

      with_retry do
        # Delegate to proto client BulkCheckPermissions
        # Each relationship becomes a CheckBulkPermissionsRequestItem
        items = relationships.map { |r| check_item_from_rel(r, permission) }

        resp = call_bulk_check(consistency, items)
        resp.map { |pair| pair[:has_permission] }
      end
    end

    # Returns true if any of the given relationships have the permission.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param permission [String]
    # @param relationships [Array<SpiceDB::Relationship>]
    # @return [Boolean]
    def check_any(consistency, permission, *relationships)
      check_permissions(consistency, permission, *relationships).any?
    end

    # Returns true if all of the given relationships have the permission.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param permission [String]
    # @param relationships [Array<SpiceDB::Relationship>]
    # @return [Boolean]
    def check_all(consistency, permission, *relationships)
      results = check_permissions(consistency, permission, *relationships)
      results.all?
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
    # are automatically paged in batches of 10,000.
    #
    # @param filter [SpiceDB::Filter]
    # @return [String] the revision of the final deletion
    def delete_relationships(filter)
      revision = nil
      loop do
        rev, complete = with_retry do
          call_delete_relationships(filter, DEFAULT_DELETE_PAGE_SIZE)
        end
        revision = rev
        break if complete
      end
      revision
    end

    # --- Lookups ---

    # Returns an Enumerator over resource IDs of the given type that the
    # subject has the specified permission on. Cursors are handled transparently.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param resource_type [String]
    # @param permission [String]
    # @param subject_type [String]
    # @param subject_id [String]
    # @return [Enumerator<String>]
    def lookup_resources(consistency, resource_type, permission, subject_type, subject_id)
      Enumerator.new do |yielder|
        cursor = nil
        loop do
          ids, new_cursor, count = with_retry do
            call_lookup_resources(consistency, resource_type, permission, subject_type, subject_id, cursor, DEFAULT_LOOKUP_PAGE_SIZE)
          end

          ids.each { |id| yielder << id }

          break if count < DEFAULT_LOOKUP_PAGE_SIZE

          cursor = new_cursor
        end
      end
    end

    # Returns an Enumerator over subject IDs of the given type that have the
    # specified permission on the resource. Unlike lookup_resources, this does
    # not support cursor-based pagination and streams all results.
    #
    # @param consistency [SpiceDB::Consistency::Strategy]
    # @param resource_type [String]
    # @param resource_id [String]
    # @param permission [String]
    # @param subject_type [String]
    # @return [Enumerator<String>]
    def lookup_subjects(consistency, resource_type, resource_id, permission, subject_type)
      Enumerator.new do |yielder|
        with_retry do
          call_lookup_subjects(consistency, resource_type, resource_id, permission, subject_type) do |subject_id|
            yielder << subject_id
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
      end
    end

    # --- Experimental ---

    # Registers a named counter that tracks relationships matching the given filter.
    # The counter is computed asynchronously by SpiceDB.
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
    # @param name [String] counter name
    # @return [SpiceDB::CountResult]
    def experimental_count_relationships(name)
      with_retry { call_count_relationships(name) }
    end

    # Removes a previously registered relationship counter.
    #
    # @param name [String] counter name
    # @return [nil]
    def experimental_unregister_relationship_counter(name)
      with_retry { call_unregister_relationship_counter(name) }
      nil
    end

    private

    # Retries the block with exponential backoff for transient gRPC errors.
    def with_retry(max_retries: MAX_RETRIES, &block)
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
    def check_item_from_rel(relationship, permission)
      {
        resource_type: relationship.resource_type,
        resource_id: relationship.resource_id,
        permission: permission,
        subject_type: relationship.subject_type,
        subject_id: relationship.subject_id,
        subject_relation: relationship.subject_relation
      }
    end

    # --- Proto client call stubs ---
    # These methods will delegate to the proto client once the spicedb-proto
    # gem is generated by buf. They are structured to make the wiring
    # straightforward.

    def call_bulk_check(consistency, items)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_write_relationships(transaction)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_read_relationships(consistency, filter, cursor, page_size)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_delete_relationships(filter, page_size)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_lookup_resources(consistency, resource_type, permission, subject_type, subject_id, cursor, page_size)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_lookup_subjects(consistency, resource_type, resource_id, permission, subject_type, &block)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_read_schema
      raise NotImplementedError, "proto client not yet available"
    end

    def call_write_schema(schema)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_reflect_schema(consistency)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_computable_permissions(consistency, definition_name, relation_name)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_dependent_relations(consistency, definition_name, permission_name)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_diff_schema(consistency, comparison_schema)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_expand_permission_tree(consistency, resource_type, resource_id, permission)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_import_relationships(relationships)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_export_relationships(consistency, filter, cursor, page_size)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_watch(object_types, start_revision, &block)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_register_relationship_counter(name, filter)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_count_relationships(name)
      raise NotImplementedError, "proto client not yet available"
    end

    def call_unregister_relationship_counter(name)
      raise NotImplementedError, "proto client not yet available"
    end
  end
end
