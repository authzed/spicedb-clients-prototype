package com.authzed.spicedb.gen.test;

import static com.authzed.spicedb.gen.test.Permissions.*;
import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.ObjectRef;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SubjectRef;
import com.authzed.spicedb.SubjectRelationFilter;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.errors.InvalidArgumentException;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Typed-client capabilities found missing while migrating real services: permission usersets,
 * wildcard refs, identity-only keys, relation checks, typed transactions, filters, bulk checks,
 * native round trips and by-name lookups. None of these tests needs a server; the live half is
 * AdoptionIntegrationTest.
 */
class AdoptionTest {

  @Test
  void permissionsAreObservable() {
    var p = Organization("acme").canAdmin();
    assertEquals("organization", p.resourceType());
    assertEquals("acme", p.resourceId());
    assertEquals("can_admin", p.name());
    assertEquals("organization:acme#can_admin", p.toString());
    assertEquals("organization#can_admin", p.ref().toString());
    assertEquals("document#view", Document_View.toString());
    assertEquals("view", Document_View.name());
    assertEquals("organization:acme#member", Organization("acme").memberRelation().toString());
  }

  @Test
  void permissionUsersetIsASubject() {
    // G1: organization#can_admin is a permission; its subject accessor is canAdminSubject() so
    // canAdmin() stays the permission accessor.
    var typed = Invitation("inv").grant(Organization("acme").canAdminSubject());
    assertEquals("invitation:inv#grant@organization:acme#can_admin", typed.toString());
    assertEquals("can_admin", typed.subjectRelation());

    var expires = Instant.parse("2030-01-01T00:00:00Z");
    var timed =
        Invitation("inv").timedGrant(Organization("acme").canAdminSubject().withExpiration(expires));
    assertEquals(expires, timed.toRelationship().expiration());
    assertEquals("can_admin", timed.toRelationship().subject().relation());
  }

  @Test
  void typedRelationshipsRoundTripWithNative() {
    var typed = Invitation("inv").grant(Organization("acme").canAdminSubject());
    Relationship nativeRel = typed.toRelationship();
    assertEquals(typed, TypedRelationship.fromRelationship(nativeRel));
    assertEquals(typed.toString(), TypedRelationship.fromRelationship(nativeRel).toString());

    var broken = TypedRelationship.ofError(new InvalidArgumentException("no such user"));
    assertTrue(broken.toString().contains("no such user"));
    assertTrue(broken.error().isPresent());
    assertThrows(InvalidArgumentException.class, broken::toRelationship);
    assertTrue(TypedRelationship.fromRelationship(null).error().isPresent());
  }

  @Test
  void writerConstructionErrorsAreDeferred() {
    var bad = Organization("").admin(User("alice"));
    assertTrue(bad.error().isPresent());
    assertThrows(InvalidArgumentException.class, bad::toRelationship);
  }

  @Test
  void expiringSubjectsCarryTheirExpiration() {
    var expires = Instant.parse("2030-01-01T00:00:00Z");
    var r = Invitation("inv").inviter(User("alice").withExpiration(expires));
    assertEquals(expires, r.expiration());
    assertEquals(expires, r.toRelationship().expiration());
  }

  @Test
  void txnDefersFirstErrorAndBuildsNativeTransaction() {
    var txn =
        new Txn()
            .touch(Organization("").admin(User("alice"))) // empty ID: deferred error
            .touch(Organization("acme").admin(User("bob")));
    assertThrows(InvalidArgumentException.class, txn::build);
    assertTrue(txn.error().isPresent());
    assertTrue(txn.updates().isEmpty());

    var ok =
        new Txn()
            .mustNotMatch(Organization("acme").adminFilter())
            .delete(Organization("acme").member(User("alice")))
            .touch(Organization("acme").admin(User("alice")));
    assertEquals(2, ok.size());
    assertFalse(ok.isEmpty());
    assertTrue(ok.error().isEmpty());
    assertEquals(2, ok.updates().size());
    assertEquals(Transaction.Operation.TOUCH, ok.updates().get(1).operation());
    assertEquals(1, ok.preconditions().size());
    Transaction nativeTxn = ok.build();
    assertEquals(2, nativeTxn.mutations().size());
    assertEquals(1, nativeTxn.preconditions().size());
    assertEquals(Transaction.Operation.DELETE, nativeTxn.mutations().get(0).operation());
    assertEquals(
        "organization:acme#member@user:alice",
        nativeTxn.mutations().get(0).relationship().toString());
    assertEquals(
        Transaction.PreconditionOperation.MUST_NOT_MATCH,
        nativeTxn.preconditions().get(0).operation());
    assertTrue(ok.toString().contains("TOUCH(organization:acme#admin@user:alice)"), ok.toString());
    // build() returns a fresh native transaction each time.
    assertNotSame(nativeTxn, ok.build());
  }

  @Test
  void typedFilters() {
    var type = OrganizationFilter();
    assertEquals("organization", type.resourceType());
    assertEquals("", type.resourceID());

    var exact = Organization("acme").filter();
    assertEquals("acme", exact.resourceID());
    assertEquals("", exact.relation());

    var admins = Organization("acme").adminFilter();
    assertEquals("admin", admins.relation());

    var bySubject = withSubject(Organization("acme").adminFilter(), User("alice"));
    assertEquals("user", bySubject.subjectType());
    assertEquals("alice", bySubject.subjectID());
    assertEquals(SubjectRelationFilter.direct(), bySubject.subjectRelation());

    var byType = withSubject(InvitationFilter(), OrganizationCanAdminType);
    assertEquals("organization", byType.subjectType());
    assertEquals("", byType.subjectID());
    assertEquals(SubjectRelationFilter.named("can_admin"), byType.subjectRelation());

    // withSubject pins the subject relation; withSubjectAnyRelation does not.
    var any = withSubjectAnyRelation(DocumentFilter(), Team("eng").member());
    assertEquals(SubjectRelationFilter.any(), any.subjectRelation());
    assertTrue(any.matches(Relationship.fromTuple("document:d#viewer@team:eng#member")));
    assertTrue(any.matches(Relationship.fromTuple("document:d#viewer@team:eng")));
    assertFalse(
        withSubject(DocumentFilter(), Team("eng").member())
            .matches(Relationship.fromTuple("document:d#viewer@team:eng")));
    assertFalse(
        withSubject(DocumentFilter(), User("x"))
            .matches(Relationship.fromTuple("document:d#viewer@user:x#self")));

    assertEquals("admin", OrganizationRelations.ADMIN);
    assertEquals("can_admin", OrganizationPermissions.CAN_ADMIN);

    // An empty ID must not widen to the whole type.
    assertThrows(InvalidArgumentException.class, () -> Organization("").filter());
    assertThrows(InvalidArgumentException.class, () -> Organization("").adminFilter());

    var keyFilter = Invitation("inv").inviterKey(User("alice")).toFilter();
    assertEquals("inv", keyFilter.resourceID());
    assertEquals("inviter", keyFilter.relation());
    assertEquals("alice", keyFilter.subjectID());
    assertEquals(SubjectRelationFilter.direct(), keyFilter.subjectRelation());
  }

  @Test
  void wildcardRefs() {
    // T6: user:* is a distinct ref; creator admits only it, guest admits both.
    var creator = Organization("acme").creator(UserWildcard());
    assertEquals("organization:acme#creator@user:*", creator.toString());
    assertEquals("*", creator.subjectId());
    var guest = Organization("acme").guest(User("alice"));
    assertEquals("alice", guest.subjectId());
    assertEquals("*", Organization("acme").guest(UserWildcard()).subjectId());
  }

  @Test
  void keysIdentifyExpiringRelationshipsWithoutAnExpiration() {
    // T7: deleting invitation#inviter@user needs no time.
    var key = Invitation("inv").inviterKey(User("alice"));
    assertNull(key.toRelationship().expiration());
    assertEquals("invitation:inv#inviter@user:alice", key.toString());
    var txn = new Txn().delete(key);
    assertEquals(1, txn.size());
    assertEquals(Transaction.Operation.DELETE, txn.build().mutations().get(0).operation());
  }

  @Test
  void relationChecksAndBulkChecks() {
    // T8 + T4: relation checks build native checks; typed bulk checks preserve order.
    var check =
        Organization("acme").memberRelation().permissionCheck(User("bob"), Consistency.full());
    assertEquals("member", check.permission());
    assertEquals(new ObjectRef("organization", "acme"), check.resource());
    assertEquals(SubjectRef.direct(new ObjectRef("user", "bob")), check.subject());

    var checks =
        checksFor(
            User("bob"),
            Organization("acme").canAdmin(),
            Organization("acme").view(),
            Organization("acme").memberRelation(),
            Invitation("inv").accept());
    assertEquals(
        List.of("can_admin", "view", "member", "accept"),
        checks.stream().map(c -> c.permission().name()).toList());
    var first = checks.get(0).toPermissionCheck(Consistency.full());
    assertEquals(new ObjectRef("organization", "acme"), first.resource());
    assertEquals("can_admin", first.permission());
  }

  @Test
  void byNameLookups() {
    var org = Organization("acme");
    assertEquals(org.canAdmin(), org.permissionByName("can_admin"));
    assertThrows(NotInSchemaException.class, () -> org.permissionByName("admin"));

    assertEquals(org.memberRelation(), org.relationByName("member"));
    assertThrows(NotInSchemaException.class, () -> org.relationByName("can_admin"));

    var typed = org.relationshipByName("creator", UserWildcard());
    assertEquals("organization:acme#creator@user:*", typed.toString());
    assertThrows(
        SubjectNotAllowedException.class, () -> org.relationshipByName("creator", User("alice")));
    assertThrows(NotInSchemaException.class, () -> org.relationshipByName("nope", User("alice")));
    assertEquals(
        "invitation:inv#grant@organization:acme#can_admin",
        Invitation("inv").relationshipByName("grant", org.canAdminSubject()).toString());

    // Runtime-chosen permission with runtime-validated subject.
    var dynamic = org.permissionByName("view").forDynamicSubject(User("alice"));
    assertEquals("view", dynamic.permission().name());
    assertThrows(
        SubjectNotAllowedException.class,
        () -> org.permissionByName("view").forDynamicSubject(Team("eng").member()));

    assertEquals(List.of("can_admin", "view"), OrganizationPermissions.names());
    assertEquals(List.of("admin", "member", "creator", "guest"), OrganizationRelations.names());
    assertEquals(List.of(), UserRelations.names());
  }

  @Test
  void caveatedSubjectsStillCarryContext() {
    var r =
        Document("readme")
            .viewer(User("alice").withIpRange(new IpRangeContext().withAllowedCidr("10.0.0.0/8")));
    assertEquals("ip_range", r.caveatName());
    assertEquals(Map.of("allowed_cidr", "10.0.0.0/8"), r.caveatContext());
  }
}
