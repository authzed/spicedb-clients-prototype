# frozen_string_literal: true

module SpiceDB
  # Request-building and response-mapping for SpiceDB::Client#updates
  # (the watch API), extracted out of Client the same way CaveatContext was
  # (see caveat_context.rb) to keep Client under the Metrics/ClassLength
  # ceiling .rubocop.yml deliberately does not raise.
  #
  # Included into Client using a `private` module body (mirroring
  # {SpiceDB::Retrying}) rather than `module_function` like CaveatContext,
  # because #watch_event_from_proto and #watch_update_from_proto call back
  # into Client's own #relationship_from_proto -- they are not
  # standalone-testable pure functions the way CaveatContext's converters
  # are, so there is no `SpiceDB::WatchMapping.foo(...)` call form to
  # support.
  module WatchMapping
    private

    # Builds a WatchRequest for #call_watch. optional_update_kinds is
    # empty-means-default (relationship updates only, for backwards
    # compatibility) -- a non-empty list is the exact set requested, so
    # asking for checkpoints must also spell out relationship updates or the
    # server would stop sending them.
    def watch_request(object_types, start_revision, content, filters: [])
      raise LocalValidationError, 'watch object_types and filters are mutually exclusive' unless object_types.empty? || filters.empty?
      raise LocalValidationError, 'watch content must be a nonempty array' unless content.is_a?(Array) && !content.empty?

      kinds = { WatchContent::RELATIONSHIPS => :WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES,
                WatchContent::CHECKPOINTS => :WATCH_KIND_INCLUDE_CHECKPOINTS,
                WatchContent::SCHEMA => :WATCH_KIND_INCLUDE_SCHEMA_UPDATES }
      Authzed::Api::V1::WatchRequest.new(
        optional_object_types: object_types.map { |type| Values.name(type, 'object_types') },
        optional_relationship_filters: filters.map { |filter| filter_to_proto(filter) },
        optional_start_cursor: token_to_proto(start_revision),
        optional_update_kinds: content.map { |choice| kinds.fetch(WatchContent.validate(choice)) }.uniq
      )
    end

    # Maps a proto WatchResponse to a native WatchEvent: the response-level
    # changes_through/is_checkpoint fields, plus each update mapped via
    # #watch_update_from_proto.
    def watch_event_from_proto(resp)
      WatchEvent.new(
        updates: resp.updates.map { |u| watch_update_from_proto(u) },
        # Message-typed proto fields are nil when unset (unlike scalar
        # fields), so preserve absence as nil.
        changes_through: ZedToken.from_proto(resp.changes_through),
        is_checkpoint: resp.is_checkpoint, schema_updated: resp.schema_updated,
        metadata: resp.optional_transaction_metadata && struct_to_caveat_context(resp.optional_transaction_metadata),
        full_revision_metadata: resp.full_revision_metadata.map { |metadata| struct_to_caveat_context(metadata) }
      )
    end

    # Server-supplied data: an unrecognized operation (OPERATION_UNSPECIFIED, or a future wire
    # value added after this client shipped) must not map to a write, and must not raise. Root
    # DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail", clause 2.
    # :unspecified is the same safe symbol this client already uses for an unrecognized
    # permissionship, rather than a name unique to this one mapper.
    def watch_update_from_proto(update)
      op = case update.operation
           when :OPERATION_CREATE then :create
           when :OPERATION_TOUCH then :touch
           when :OPERATION_DELETE then :delete
           else :unspecified
           end
      Update.new(operation: op, relationship: relationship_from_proto(update.relationship, 'watch update relationship'))
    end
  end
end
