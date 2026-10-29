package com.authzed.spicedb.gen.test;

import static com.authzed.spicedb.gen.test.Permissions.*;
import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.ClientOperation;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.LookupOptions;
import com.authzed.spicedb.LookupPermissionshipFilter;
import com.authzed.spicedb.LookupResult.LookupResource;
import com.authzed.spicedb.ObjectRef;
import com.authzed.spicedb.Permissionship;
import com.authzed.spicedb.SpiceDBApi;
import com.authzed.spicedb.SubjectRef;
import com.authzed.spicedb.errors.SpiceDBException;
import com.authzed.spicedb.testing.InMemorySpiceDB;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;
import org.junit.jupiter.api.Test;

/**
 * Runtime-name access, name constants and the schema text, plus the typed client over the
 * in-memory double. None of these tests needs a server.
 */
class RuntimeNamesTest {

  // --- The typed client wraps any SpiceDBApi, including the double ---

  @Test
  void typedClientWrapsTheInMemoryDouble() {
    var db = new InMemorySpiceDB();
    try (var typed = new TypedClient(db)) {
      SpiceDBApi api = typed.client();
      assertSame(db, api);

      typed.touch(List.of(Document("readme").editor(User("alice"))));
      assertEquals(1, db.relationships().size());
      try (var rows = typed.readRelationships(Consistency.full(), Document("readme").filter())) {
        assertEquals("alice", rows.findFirst().orElseThrow().relationship().subjectId());
      }

      db.setCheckResult(
          new ObjectRef("document", "readme"),
          "edit",
          SubjectRef.direct(new ObjectRef("user", "alice")),
          Permissionship.HAS_PERMISSION);
      assertTrue(
          typed.check(Consistency.full(), Document("readme").edit(), User("alice")).hasPermission());
      assertFalse(
          typed.check(Consistency.full(), Document("readme").edit(), User("bob")).hasPermission());
    }
  }

  // --- definitionByName ---

  @Test
  void definitionByNameResolvesEveryDefinition() {
    for (var name : Definitions.names()) {
      var def = definitionByName(name);
      assertEquals(name, def.name());
      assertEquals(name, def.ref("x").definitionName());
      assertEquals("x", def.ref("x").id());
      assertEquals(name, def.toString());
      assertEquals(definitionByName(name), def);
    }
    var document = definitionByName(Definitions.DOCUMENT);
    assertInstanceOf(DocumentRef.class, document.ref("readme"));
    assertEquals(Document("readme"), document.ref("readme"));
    assertEquals(DocumentRelations.names(), document.relationNames());
    assertEquals(DocumentPermissions.names(), document.permissionNames());
    assertEquals(DocumentFilter(), document.filter());
    assertEquals(Document("readme").view(), document.permissionByName("readme", "view"));
    assertEquals(Document("readme").ownerRelation(), document.relationByName("readme", "owner"));
    assertEquals(Document_View, document.permissionRef("view"));
  }

  @Test
  void definitionByNameRejectsNamesTheSchemaLacks() {
    var e = assertThrows(NotInSchemaException.class, () -> definitionByName("folder"));
    assertTrue(e.getMessage().contains("folder"), e.getMessage());
    assertThrows(
        NotInSchemaException.class,
        () -> definitionByName("document").permissionByName("readme", "share"));
    assertThrows(
        NotInSchemaException.class, () -> definitionByName("document").permissionRef("viewer"));
  }

  @Test
  void definitionRefsAreDynamicSubjects() {
    // A subject type named at run time is a DefinitionRef, so forDynamicSubject accepts it.
    DefinitionRef user = definitionByName("user").ref("alice");
    var check = definitionByName("document").permissionByName("readme", "view");
    assertEquals("document:readme#view@user:alice", check.forDynamicSubject(user).toString());
    var relationship = definitionByName("document").ref("readme").relationshipByName("owner", user);
    assertEquals(Document("readme").owner(User("alice")), relationship);
  }

  // --- permissionRefByName ---

  @Test
  void permissionRefByNameNeedsNoResource() {
    assertEquals(Document_View, permissionRefByName("document", "view"));
    assertEquals(Organization_CanAdmin, permissionRefByName("organization", "can_admin"));
    var ref = permissionRefByName("document", "delete");
    assertEquals("document#delete", ref.toString());
    assertTrue(ref.admits(User("alice")));
    assertFalse(ref.admits(Team("eng").member()));
    assertEquals(Document("x").delete().ref(), ref);

    assertThrows(NotInSchemaException.class, () -> permissionRefByName("folder", "view"));
    // A relation is not a permission.
    var e =
        assertThrows(NotInSchemaException.class, () -> permissionRefByName("document", "viewer"));
    assertTrue(e.getMessage().contains("document has no permission viewer"), e.getMessage());
  }

  // --- lookupResources by runtime names ---

  @Test
  void lookupResourcesByNamesForwardsTheValidatedRequest() {
    var db = new InMemorySpiceDB();
    var options =
        LookupOptions.builder()
            .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
            .build();
    try (var typed = new TypedClient(db)) {
      drain(() -> typed.lookupResourcesByName(Consistency.full(), "document", "view", User("alice"), options));
    }
    var call = db.callsOf(ClientOperation.LOOKUP_RESOURCES).get(0);
    assertEquals(
        List.of(
            Consistency.full(),
            "document",
            "view",
            SubjectRef.direct(new ObjectRef("user", "alice"))),
        call.arguments());
    assertSame(options, call.options());
  }

  @Test
  void lookupResourcesByNamesRejectsBeforeCalling() {
    var db = new InMemorySpiceDB();
    try (var typed = new TypedClient(db)) {
      assertThrows(
          NotInSchemaException.class,
          () -> typed.lookupResourcesByName(Consistency.full(), "folder", "view", User("alice")));
      assertThrows(
          NotInSchemaException.class,
          () -> typed.lookupResourcesByName(Consistency.full(), "document", "share", User("alice")));
      // A wildcard of a type that reaches the permission is not a lookup subject.
      assertThrows(
          SubjectNotAllowedException.class,
          () -> typed.lookupResourcesByName(Consistency.full(), "organization", "view", UserWildcard()));
      // document#delete is reached by users only, so team members find nothing, without a call.
      try (var none =
          typed.lookupResourcesByName(Consistency.full(), "document", "delete", Team("eng").member())) {
        assertEquals(0, none.count());
      }
      assertThrows(
          NullPointerException.class,
          () -> typed.lookupResourcesByName(Consistency.full(), "document", "view", null));
    }
    assertTrue(db.callsOf(ClientOperation.LOOKUP_RESOURCES).isEmpty());
  }

  /**
   * A policy layer that receives (type, id, permission) strings, such as a Spring Security
   * permission evaluator, needs no switch over definition names, no placeholder resource ID and no
   * native lookup call.
   */
  @Test
  void policyLayerUsesOnlyRuntimeNames() {
    var db = new InMemorySpiceDB();
    db.setCheckResult(
        new ObjectRef("organization", "acme"),
        "view",
        SubjectRef.direct(new ObjectRef("user", "alice")),
        Permissionship.HAS_PERMISSION);
    try (var typed = new TypedClient(db)) {
      var policy = new RuntimeNamePolicy(typed);
      assertTrue(policy.hasPermission("organization", "acme", "view", "alice"));
      assertFalse(policy.hasPermission("organization", "acme", "view", "bob"));
      assertThrows(
          NotInSchemaException.class, () -> policy.hasPermission("office", "x", "view", "alice"));
      drain(() -> policy.accessibleResourceIds("organization", "view", "alice").stream());
      assertThrows(
          NotInSchemaException.class,
          () -> policy.accessibleResourceIds("organization", "owner", "alice"));
      // Relations are looked up as well as permissions, as SpiceDB allows.
      drain(() -> policy.accessibleResourceIds("organization", "admin", "alice").stream());
    }
    assertEquals(2, db.callsOf(ClientOperation.LOOKUP_RESOURCES).size());
  }

  private record RuntimeNamePolicy(TypedClient spiceDb) {
    boolean hasPermission(String type, String id, String permission, String userId) {
      var check = definitionByName(type).permissionByName(id, permission);
      return spiceDb
          .check(Consistency.full(), check.forDynamicSubject(User(userId)))
          .hasPermission();
    }

    Set<String> accessibleResourceIds(String type, String permission, String userId) {
      var options =
          LookupOptions.builder()
              .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
              .build();
      try (var found =
          spiceDb.lookupResourcesByName(Consistency.full(), type, permission, User(userId), options)) {
        return found.map(LookupResource::resourceId).collect(Collectors.toSet());
      }
    }
  }

  // --- Constants ---

  @Test
  void definitionNameConstants() {
    assertEquals("user", Definitions.USER);
    assertEquals("document", Definitions.DOCUMENT);
    assertEquals("invitation", Definitions.INVITATION);
    // Schema order.
    assertEquals(
        List.of("user", "team", "document", "organization", "invitation"), Definitions.names());
  }

  @Test
  void schemaTextIsTheSourceItWasGeneratedFrom() throws Exception {
    assertEquals(Files.readString(Path.of("../sample.zed")), SCHEMA);
  }

  /**
   * Consumes a lookup stream. The double may serve the lookup or report it unimplemented; either
   * way the request has been recorded, which is what these tests assert.
   */
  private static void drain(java.util.function.Supplier<? extends java.util.stream.Stream<?>> s) {
    try (var stream = s.get()) {
      stream.forEach(x -> {});
    } catch (SpiceDBException ignored) {
      // An unimplemented lookup in the double; the request was still recorded.
    }
  }
}
