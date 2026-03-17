package com.authzed.spicedb;

import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Unit tests for SpiceDBClient helper methods (proto conversion, etc.).
 * Integration tests require a running SpiceDB instance and belong in
 * the examples directory.
 */
class SpiceDBClientTest {

    @Test
    void toProtoRelationshipBasic() {
        Relationship r = Relationship.of("document", "doc1", "viewer", "user", "alice");
        var proto = SpiceDBClient.toProtoRelationship(r);
        assertEquals("document", proto.getResource().getObjectType());
        assertEquals("doc1", proto.getResource().getObjectId());
        assertEquals("viewer", proto.getRelation());
        assertEquals("user", proto.getSubject().getObject().getObjectType());
        assertEquals("alice", proto.getSubject().getObject().getObjectId());
    }

    @Test
    void toProtoRelationshipWithSubjectRelation() {
        Relationship r = Relationship.of("document", "doc1", "viewer", "group", "eng", "member");
        var proto = SpiceDBClient.toProtoRelationship(r);
        assertEquals("member", proto.getSubject().getOptionalRelation());
    }

    @Test
    void toProtoRelationshipWithCaveat() {
        Relationship r = Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCaveat("is_allowed", Map.of("allowed", true));
        var proto = SpiceDBClient.toProtoRelationship(r);
        assertTrue(proto.hasOptionalCaveat());
        assertEquals("is_allowed", proto.getOptionalCaveat().getCaveatName());
    }

    @Test
    void toProtoRelationshipWithExpiration() {
        Instant exp = Instant.parse("2026-12-31T23:59:59Z");
        Relationship r = Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withExpiration(exp);
        var proto = SpiceDBClient.toProtoRelationship(r);
        assertTrue(proto.hasOptionalExpiresAt());
        assertEquals(exp.getEpochSecond(), proto.getOptionalExpiresAt().getSeconds());
    }

    @Test
    void fromProtoRelationshipRoundTrip() {
        Relationship original = Relationship.of(
            "document", "doc1", "viewer", "user", "alice", "member");
        var proto = SpiceDBClient.toProtoRelationship(original);
        Relationship restored = SpiceDBClient.fromProtoRelationship(proto);
        assertEquals(original.resourceType(), restored.resourceType());
        assertEquals(original.resourceID(), restored.resourceID());
        assertEquals(original.resourceRelation(), restored.resourceRelation());
        assertEquals(original.subjectType(), restored.subjectType());
        assertEquals(original.subjectID(), restored.subjectID());
        assertEquals(original.subjectRelation(), restored.subjectRelation());
    }

    @Test
    void fromProtoRelationshipWithCaveatRoundTrip() {
        Relationship original = Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCaveat("test_caveat", Map.of("key", "value"));
        var proto = SpiceDBClient.toProtoRelationship(original);
        Relationship restored = SpiceDBClient.fromProtoRelationship(proto);
        assertEquals("test_caveat", restored.caveatName());
        assertEquals("value", restored.caveatContext().get("key"));
    }

    @Test
    void fromProtoRelationshipWithExpirationRoundTrip() {
        Instant exp = Instant.parse("2026-06-15T12:00:00Z");
        Relationship original = Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withExpiration(exp);
        var proto = SpiceDBClient.toProtoRelationship(original);
        Relationship restored = SpiceDBClient.fromProtoRelationship(proto);
        assertEquals(exp, restored.expiration());
    }
}
