package com.authzed.spicedb.gen.test;

import static com.authzed.spicedb.Consistency.*;
import static com.authzed.spicedb.gen.test.Permissions.*;

import com.authzed.spicedb.*;
import java.time.Instant;
import java.util.List;

/**
 * Every line marked "should not compile" must fail on its own: the Magefile compiles this file with
 * every diagnostic reported and requires an error on each marked line, no error on any unmarked
 * line, and no "cannot find symbol" error (which would mean the API was renamed, not rejected).
 */
class TypeErrors {
  void subjects(TypedClient tc) {
    // Team members cannot have delete or edit permission on documents.
    tc.check(full(), Document("readme").delete(), Team("eng").member()); // should not compile
    tc.check(full(), Document("readme").edit(), Team("eng").member()); // should not compile
    // editor only accepts users, not team members or caveated users.
    Document("readme").editor(Team("eng").member()); // should not compile
    Document("x").editor(User("x").withIpRange(new IpRangeContext())); // should not compile
    // Type sentinels name a subject type for lookups; they are not writable subjects.
    Document("readme").editor(UserType); // should not compile
  }

  void permissionUsersets() {
    // G1: grant admits only organization#can_admin, a permission userset.
    Invitation("x").grant(User("alice")); // should not compile
    Invitation("x").grant(Organization("acme")); // should not compile
    // timed_grant requires the expiring form of the userset.
    Invitation("x").timedGrant(Organization("acme").canAdminSubject()); // should not compile
    // canAdmin() stays the permission accessor; the subject accessor is canAdminSubject().
    Invitation("x").grant(Organization("acme").canAdmin()); // should not compile
  }

  void wildcards() {
    // T6: creator admits only user:*, never a concrete user.
    Organization("x").creator(User("alice")); // should not compile
    // A wildcard is not a concrete member.
    Organization("x").member(UserWildcard()); // should not compile
  }

  void keysAndExpiration() {
    // T7: inviter admits only expiring users; its key admits only the base user.
    Invitation("x").inviter(User("alice")); // should not compile
    Invitation("x").inviterKey(User("alice").withExpiration(Instant.EPOCH)); // should not compile
    // A key identifies a relationship for deletes only; it cannot be written.
    new Txn().touch(Invitation("x").inviterKey(User("alice"))); // should not compile
  }

  void checks(TypedClient tc) {
    // T8: relation checks keep the relation's subject constraint.
    tc.check(full(), Organization("x").memberRelation(), Team("eng").member()); // should not compile
    // T4: typed bulk checks reject a permission the subject is not admitted to.
    checksFor(Team("eng").member(), Document("x").delete()); // should not compile
  }

  void runtimeNames(TypedClient tc) {
    // Runtime-name lookups take a generated Subject, never a native SubjectRef.
    tc.lookupResources(full(), "document", "view", SubjectRef.direct(new ObjectRef("user", "a"))); // should not compile
    // A runtime-resolved ref is PermissionRef<?>: the static typed lookup cannot take it.
    tc.lookupResources(full(), permissionRefByName("document", "view"), User("a")); // should not compile
    // The static typed lookup still rejects a subject its permission does not admit.
    tc.lookupResources(full(), Document_Delete, Team("eng").member()); // should not compile
    // A definition resolved by name yields a DefinitionRef, not a specific ref type.
    DocumentRef doc = definitionByName("document").ref("x"); // should not compile
    // Definition-name constants and the schema text are constants, not settable.
    Definitions.DOCUMENT = "doc"; // should not compile
    SCHEMA = ""; // should not compile
    // Definition is built only by definitionByName.
    new Definition("document", DocumentRef::new, List.of(), List.of()); // should not compile
  }

  void runtimeEntryPoints(TypedClient tc) {
    // By-name results are the shared types, not a specific generated record.
    DocumentRef doc = parseObject("document:readme"); // should not compile
    TeamMemberRef set = subjectSetByName("team", "eng", "member"); // should not compile
    // A subject set is a Subject, never an object of a definition.
    DefinitionRef notAnObject = Team("eng").member(); // should not compile
    // relationshipFromTriple takes tuple text, not typed refs.
    relationshipFromTriple(Document("x"), "viewer", User("alice")); // should not compile
    // checkByName takes a generated subject, not tuple text.
    tc.checkByName(full(), "document", "x", "view", "user:alice"); // should not compile
    // NamedCheck needs every name and a generated subject.
    new NamedCheck("document", "x", "view", "user:alice"); // should not compile
  }

  void txns(TypedClient tc) {
    // writeTxn never shadows the native write.
    tc.write(new Txn()); // should not compile
  }

  void writesReturnResults(TypedClient tc, SpiceDBClient client) {
    ZedToken bare = tc.touch(List.of()); // should not compile
    Consistency.atLeast(tc.create(List.of())); // should not compile
    ZedToken nativeBare = client.write(new Transaction()); // should not compile
  }

  void modesAndRevisionsAreTyped() {
    CheckOptions.builder().tracing(true); // should not compile
    LookupOptions.builder().debug("enabled"); // should not compile
    LookupOptions.builder().permissionshipFilter("has_permission_only"); // should not compile
    WatchOptions.builder().content(true); // should not compile
    ClientOptions.builder().remotePlaintextCredentials(true); // should not compile
    Consistency.atLeast("revision"); // should not compile
  }
}
