# frozen_string_literal: true

module SpiceDB
  # Request-building and response-mapping for
  # SpiceDB::Client#experimental_roaring_lookup_resources, extracted out of
  # Client the same way WatchMapping was (see watch_mapping.rb) to keep
  # Client under the Metrics/ClassLength ceiling .rubocop.yml deliberately
  # does not raise.
  module RoaringLookup
    private

    # Issues ExperimentalRoaringLookupResources and maps the response to a
    # native RoaringLookupResourcesResult. build_consistency and
    # deadline_for are Client's own helpers (Client#initialize/Retrying), not
    # this module's -- available here because Client includes this module
    # into its own instance method namespace, the same way WatchMapping's
    # #watch_event_from_proto reaches Client#relationship_from_proto.
    def call_roaring_lookup_resources(consistency, resource_type, permission, subject_type, subject_id,
                                      timeout_seconds)
      resp = @proto_client.materialize.experimental_roaring_lookup_resources(
        Authzed::Api::Materialize::V0::ExperimentalRoaringLookupResourcesRequest.new(
          consistency: build_consistency(consistency),
          resource_object_type: resource_type,
          permission: permission,
          subject: Authzed::Api::V1::SubjectReference.new(
            object: Authzed::Api::V1::ObjectReference.new(
              object_type: subject_type,
              object_id: subject_id
            )
          )
        ),
        deadline: deadline_for(timeout_seconds)
      )

      RoaringLookupResourcesResult.new(
        bitmap: resp.bitmap,
        cardinality: resp.cardinality,
        at_revision: resp.at_revision.token
      )
    end
  end
end
