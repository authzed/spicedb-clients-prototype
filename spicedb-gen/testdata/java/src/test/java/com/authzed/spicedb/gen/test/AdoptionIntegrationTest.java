package com.authzed.spicedb.gen.test;

import static com.authzed.spicedb.gen.test.Permissions.*;
import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.errors.FailedPreconditionException;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import org.junit.jupiter.api.Test;

/**
 * The adoption capabilities against a real SpiceDB (reads SPICEDB_ENDPOINT, default
 * localhost:50051): permission usersets and wildcards write and check, relation checks, typed bulk
 * checks, preconditioned typed transactions, read-back deletes and identity-only key deletes.
 */
class AdoptionIntegrationTest {
  private static final String SCHEMA =
      """
      use expiration

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

      definition organization {
          relation admin: user
          relation member: user
          relation creator: user:*
          relation guest: user | user:*
          permission can_admin = admin
          permission view = admin + member + guest
      }

      definition invitation {
          relation org: organization
          relation grant: organization#can_admin
          relation timed_grant: organization#can_admin with expiration
          relation inviter: user with expiration
          permission accept = grant + timed_grant
      }
      """;

  /**
   * Other suites (PermissionsTest) rewrite the schema without organization/invitation, which
   * SpiceDB refuses while relationships exist under them: leave none behind.
   */
  @org.junit.jupiter.api.AfterEach
  void removeAdoptionRelationships() {
    var endpoint = System.getenv().getOrDefault("SPICEDB_ENDPOINT", "localhost:50051");
    try (var raw = SpiceDBClient.createPlaintext(endpoint, "somerandomkeyhere")) {
      raw.deleteAllRelationshipsNonAtomic(InvitationFilter());
      raw.deleteAllRelationshipsNonAtomic(OrganizationFilter());
    }
  }

  /**
   * A policy layer passing type and permission names at run time: the generated schema text is
   * written as-is, and lookups and checks by name reach the server with no native call.
   */
  @Test
  void runtimeNamesAgainstServer() {
    var endpoint = System.getenv().getOrDefault("SPICEDB_ENDPOINT", "localhost:50051");
    try (var raw = SpiceDBClient.createPlaintext(endpoint, "somerandomkeyhere");
        var tc = new TypedClient(raw)) {
      assertEquals(SCHEMA, Permissions.SCHEMA);
      tc.writeSchema(Permissions.SCHEMA);
      var org = definitionByName(Definitions.ORGANIZATION);
      tc.touch(
          java.util.List.of(
              org.ref("acme-runtime").relationshipByName("member", User("carol")),
              org.ref("beta-runtime").relationshipByName("admin", User("carol"))));
      var options =
          com.authzed.spicedb.LookupOptions.builder()
              .permissionshipFilter(
                  com.authzed.spicedb.LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
              .build();
      try (var found =
          tc.lookupResources(Consistency.full(), "organization", "view", User("carol"), options)) {
        assertEquals(
            java.util.Set.of("acme-runtime", "beta-runtime"),
            found
                .map(com.authzed.spicedb.LookupResult.LookupResource::resourceId)
                .collect(java.util.stream.Collectors.toSet()));
      }
      var check = org.permissionByName("beta-runtime", "can_admin").forDynamicSubject(User("carol"));
      assertTrue(tc.check(Consistency.full(), check).hasPermission());
    }
  }

  @Test
  void adoptionAgainstServer() {
    var endpoint = System.getenv().getOrDefault("SPICEDB_ENDPOINT", "localhost:50051");
    try (var raw = SpiceDBClient.createPlaintext(endpoint, "somerandomkeyhere");
        var tc = new TypedClient(raw)) {
      tc.writeSchema(SCHEMA);
      var cs = Consistency.full();
      var acme = Organization("acme-adoption");
      var inv = Invitation("inv-adoption");
      tc.deleteAllRelationshipsNonAtomic(acme.filter());
      tc.deleteAllRelationshipsNonAtomic(inv.filter());

      // G1 + T6: permission userset and wildcard subjects write and check.
      var inAnHour = Instant.now().plus(Duration.ofHours(1));
      tc.writeTxn(
          new Txn()
              .touch(acme.admin(User("alice")))
              .touch(acme.member(User("bob")))
              .touch(acme.creator(UserWildcard()))
              .touch(acme.guest(UserWildcard()))
              .touch(inv.grant(acme.canAdminSubject()))
              .touch(inv.timedGrant(acme.canAdminSubject().withExpiration(inAnHour)))
              .touch(inv.inviter(User("alice").withExpiration(inAnHour))));

      assertTrue(
          tc.check(cs, inv.accept(), User("alice")).hasPermission(),
          "alice can accept through organization#can_admin");
      assertTrue(
          tc.check(cs, acme.view(), User("anyone")).hasPermission(),
          "user:* guest grants view to anyone");

      // T8: relation checks.
      assertTrue(tc.check(cs, acme.memberRelation(), User("bob")).hasPermission());
      assertFalse(
          tc.check(cs, acme.memberRelation(), User("alice")).hasPermission(),
          "admin is not the member relation");

      // T4: typed bulk check, input order preserved.
      var results =
          tc.checkBulk(
              cs, checksFor(User("bob"), acme.canAdmin(), acme.view(), acme.memberRelation()));
      assertEquals(3, results.size());
      assertFalse(results.get(0).hasPermission());
      assertTrue(results.get(1).hasPermission());
      assertTrue(results.get(2).hasPermission());

      // T9: a runtime-chosen permission name.
      assertTrue(
          tc.check(cs, acme.permissionByName("view").forDynamicSubject(User("bob")))
              .hasPermission());

      // T2: a precondition guards a mixed delete+touch; the failing guard writes nothing.
      tc.writeTxn(
          new Txn()
              .mustNotMatch(withSubject(acme.adminFilter(), User("bob")))
              .delete(acme.member(User("bob")))
              .touch(acme.admin(User("bob"))));
      assertThrows(
          FailedPreconditionException.class,
          () ->
              tc.writeTxn(
                  new Txn()
                      .mustNotMatch(withSubject(acme.adminFilter(), User("bob")))
                      .touch(acme.member(User("carol")))));
      assertFalse(tc.check(cs, acme.memberRelation(), User("carol")).hasPermission());

      // T3 + T5: read back with a typed filter and delete what was read.
      var admins = new ArrayList<String>();
      var txn = new Txn();
      try (var stream = tc.readRelationships(cs, acme.adminFilter())) {
        stream.forEach(
            r -> {
              admins.add(r.relationship().subjectId());
              txn.delete(TypedRelationship.fromRelationship(r.relationship()));
            });
      }
      admins.sort(null);
      assertEquals(java.util.List.of("alice", "bob"), admins);
      tc.writeTxn(txn);
      try (var stream = tc.readRelationships(cs, acme.adminFilter())) {
        assertEquals(0, stream.count());
      }

      // T7: delete an expiration-only relationship by identity alone.
      tc.writeTxn(new Txn().delete(inv.inviterKey(User("alice"))));
      try (var stream = tc.readRelationships(cs, inv.inviterFilter())) {
        assertEquals(0, stream.count(), "inviter relationship should be deleted");
      }
    }
  }
}
