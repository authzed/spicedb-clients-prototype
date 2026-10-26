package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.errors.InvalidArgumentException;
import java.util.*;
import org.junit.jupiter.api.Test;

class PermissionCheckBuilderTest {
  @Test
  void buildsIndependentSnapshots() {
    var nested = new ArrayList<Object>(List.of("original"));
    var context = new HashMap<String, Object>();
    context.put("values", nested);
    var resource = new ObjectRef("document", "readme");
    var subject = SubjectRef.direct(new ObjectRef("user", "alice"));
    var builder =
        PermissionCheck.builder()
            .resource(resource)
            .permission("view")
            .subject(subject)
            .consistency(Consistency.full())
            .context(context);
    nested.add("changed");
    var first = builder.build();
    var second = builder.permission("edit").context(null).build();
    assertEquals(resource, first.resource());
    assertEquals(subject, first.subject());
    assertEquals("view", first.permission());
    assertEquals(List.of("original"), first.context().get("values"));
    assertEquals("edit", second.permission());
    assertNull(second.context());
  }

  @Test
  void rejectsMissingRequiredFields() {
    var builder = PermissionCheck.builder();
    assertThrows(InvalidArgumentException.class, builder::build);
    builder.resource(new ObjectRef("document", "readme"));
    assertThrows(InvalidArgumentException.class, builder::build);
    builder.permission("view");
    assertThrows(InvalidArgumentException.class, builder::build);
    builder.subject(SubjectRef.direct(new ObjectRef("user", "alice")));
    assertThrows(InvalidArgumentException.class, builder::build);
    builder.consistency(Consistency.full());
    assertDoesNotThrow(builder::build);
    assertThrows(InvalidArgumentException.class, () -> builder.permission("").build());
  }
}
