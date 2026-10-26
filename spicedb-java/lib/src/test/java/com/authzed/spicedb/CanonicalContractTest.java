package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.errors.InvalidArgumentException;
import java.util.*;
import org.junit.jupiter.api.Test;

class CanonicalContractTest {
  @Test
  void snapshotsNestedNullContext() {
    var list = new ArrayList<Object>();
    list.add(null);
    list.add("before");
    var context = new HashMap<String, Object>();
    context.put("values", list);
    var request =
        new PermissionCheck(
            new ObjectRef("document", "one"),
            "view",
            SubjectRef.direct(new ObjectRef("user", "alice")),
            Consistency.full(),
            context);
    list.set(1, "after");
    assertEquals(Arrays.asList(null, "before"), request.context().get("values"));
    assertThrows(UnsupportedOperationException.class, () -> request.context().put("x", 1));
  }

  @Test
  void rejectsInvalidContextAndSubject() {
    var cycle = new HashMap<String, Object>();
    cycle.put("self", cycle);
    assertThrows(
        InvalidArgumentException.class, () -> CheckOptions.builder().context(cycle).build());
    assertThrows(
        InvalidArgumentException.class, () -> SubjectRef.named(new ObjectRef("user", "a"), "..."));
  }

  @Test
  void semanticConsistencyAndExactFilter() {
    assertEquals(
        Consistency.atLeast(new ZedToken("opaque")), Consistency.atLeast(new ZedToken("opaque")));
    var relationship =
        Relationship.of(
            new ObjectRef("document", "one"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
    assertEquals(SubjectRelationFilter.direct(), relationship.toFilter().subjectRelation());
  }
}
