package com.authzed.spicedb.gen.test;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.LookupResult;
import com.authzed.spicedb.SpiceDBClient;

import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.stream.Collectors;

import static com.authzed.spicedb.Consistency.*;
import static com.authzed.spicedb.gen.test.Permissions.*;
import static org.assertj.core.api.Assertions.*;

/**
 * Integration tests for generated typed permissions API.
 *
 * <p>Requires a running SpiceDB instance on localhost:50051 with
 * preshared key "somerandomkeyhere".
 */
class PermissionsTest {

    private static final String SCHEMA = """
        caveat ip_range(allowed_cidr string) {
            allowed_cidr == "0.0.0.0/0"
        }

        caveat time_window(start string, end string) {
            start != "" && end != ""
        }

        definition user {}

        definition team {
            relation member: user | team#member
        }

        definition document {
            relation viewer: user | user with ip_range | user with time_window | team#member
            relation editor: user
            relation owner: user
            permission view = viewer + editor + owner
            permission edit = editor + owner
            permission delete = owner
        }
        """;

    private static SpiceDBClient rawClient;
    private static TypedClient tc;

    @BeforeAll
    static void setUpClient() {
        rawClient = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");
        rawClient.writeSchema(SCHEMA);
        tc = new TypedClient(rawClient);
    }

    @AfterAll
    static void tearDownClient() {
        if (rawClient != null) {
            rawClient.close();
        }
    }

    @BeforeEach
    void cleanUp() {
        rawClient.deleteRelationships(Filter.of("document"));
        rawClient.deleteRelationships(Filter.of("team"));
    }

    // --- Touch + Check (base) ---

    @Test
    void user_viewer_can_view() {
        tc.touch(Document("readme").viewer(User("alice")));

        boolean allowed = tc.check(full(), Document("readme").view(), User("alice")).hasPermission();
        assertThat(allowed).isTrue();
    }

    @Test
    void user_viewer_cannot_edit() {
        tc.touch(Document("readme").viewer(User("alice")));

        boolean allowed = tc.check(full(), Document("readme").edit(), User("alice")).hasPermission();
        assertThat(allowed).isFalse();
    }

    @Test
    void user_editor_can_edit_and_view() {
        tc.touch(Document("readme").editor(User("bob")));

        assertThat(tc.check(full(), Document("readme").edit(), User("bob")).hasPermission()).isTrue();
        assertThat(tc.check(full(), Document("readme").view(), User("bob")).hasPermission()).isTrue();
    }

    @Test
    void user_owner_can_delete() {
        tc.touch(Document("readme").owner(User("charlie")));

        assertThat(tc.check(full(), Document("readme").delete(), User("charlie")).hasPermission()).isTrue();
        assertThat(tc.check(full(), Document("readme").edit(), User("charlie")).hasPermission()).isTrue();
        assertThat(tc.check(full(), Document("readme").view(), User("charlie")).hasPermission()).isTrue();
    }

    // --- Touch + Check (caveated) ---

    @Test
    void caveated_viewer_with_ip_range() {
        tc.touch(Document("readme").viewer(
            User("alice").withIpRange(new IpRangeContext().withAllowedCidr("0.0.0.0/0"))
        ));

        // Caveated permission check — SpiceDB evaluates the caveat.
        boolean allowed = tc.check(full(), Document("readme").view(), User("alice")).hasPermission();
        assertThat(allowed).isTrue();
    }

    @Test
    void caveated_viewer_missing_context_is_conditional_not_denied() {
        // No context supplied at check time, so the caveat cannot be evaluated.
        // RULE (root DESIGN.md, "Only an unconditional grant is true"): this must
        // surface as CONDITIONAL_PERMISSION, not silently collapse to a bare denial
        // (or, worse, a grant). hasPermission() must be false either way, but the
        // *reason* must be observable via permissionship()/missingContext() — that
        // observability is exactly what a boolean-returning check() would destroy.
        tc.touch(Document("readme").viewer(
            User("frank").withIpRange(new IpRangeContext())
        ));

        var result = tc.check(full(), Document("readme").view(), User("frank"));

        assertThat(result.hasPermission()).isFalse();
        assertThat(result.permissionship()).isEqualTo(LookupResult.Permissionship.CONDITIONAL_PERMISSION);
        assertThat(result.missingContext()).contains("allowed_cidr");
        assertThat(result.checkedAt()).isNotBlank();
    }

    // --- Touch + Check (sub-ref) ---

    @Test
    void team_member_can_view_via_subref() {
        tc.touch(
            Team("eng").forMember(User("alice")),
            Document("readme").viewer(Team("eng").member())
        );

        assertThat(tc.check(full(), Document("readme").view(), User("alice")).hasPermission()).isTrue();
    }

    // --- LookupResources ---

    @Test
    void lookup_resources_finds_viewable_docs() {
        tc.touch(
            Document("doc1").viewer(User("alice")),
            Document("doc2").editor(User("alice")),
            Document("doc3").owner(User("bob"))
        );

        List<String> resources = tc.lookupResources(full(), Document_View, User("alice"))
            .map(LookupResult.LookupResource::resourceId)
            .collect(Collectors.toList());

        assertThat(resources).containsExactlyInAnyOrder("doc1", "doc2");
    }

    @Test
    void lookup_resources_finds_editable_docs() {
        tc.touch(
            Document("doc1").viewer(User("alice")),
            Document("doc2").editor(User("alice")),
            Document("doc3").owner(User("alice"))
        );

        List<String> resources = tc.lookupResources(full(), Document_Edit, User("alice"))
            .map(LookupResult.LookupResource::resourceId)
            .collect(Collectors.toList());

        assertThat(resources).containsExactlyInAnyOrder("doc2", "doc3");
    }

    // --- LookupSubjects ---

    @Test
    void lookup_subjects_finds_viewers() {
        tc.touch(
            Document("readme").viewer(User("alice")),
            Document("readme").editor(User("bob")),
            Document("readme").owner(User("charlie"))
        );

        List<String> subjects = tc.lookupSubjects(full(), Document("readme").view(), UserType)
            .map(ls -> ls.subject().subjectId())
            .collect(Collectors.toList());

        assertThat(subjects).containsExactlyInAnyOrder("alice", "bob", "charlie");
    }

    @Test
    void lookup_subjects_finds_only_editors() {
        tc.touch(
            Document("readme").viewer(User("alice")),
            Document("readme").editor(User("bob")),
            Document("readme").owner(User("charlie"))
        );

        List<String> subjects = tc.lookupSubjects(full(), Document("readme").edit(), UserType)
            .map(ls -> ls.subject().subjectId())
            .collect(Collectors.toList());

        assertThat(subjects).containsExactlyInAnyOrder("bob", "charlie");
    }
}
