package com.authzed.spicedb.gen.test;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.SpiceDBClient;
import java.lang.reflect.Modifier;
import java.util.Set;
import org.junit.jupiter.api.Test;

class TypedParityTest {
  // These operations intentionally accept schema-aware subjects/permissions. Native overloads
  // would bypass schema validation. close() intentionally retains owned-versus-borrowed semantics.
  private static final Set<String> SCHEMA_AWARE = Set.of("check", "lookupResources", "lookupSubjects");

  @Test
  void allNativeInstanceCapabilitiesHaveDirectNativeSignaturesExceptSchemaAwareOperations() throws Exception {
    for (var nativeMethod : SpiceDBClient.class.getDeclaredMethods()) {
      if (!Modifier.isPublic(nativeMethod.getModifiers()) || Modifier.isStatic(nativeMethod.getModifiers())) continue;
      if (SCHEMA_AWARE.contains(nativeMethod.getName())) {
        assertThrows(NoSuchMethodException.class, () -> Permissions.TypedClient.class.getMethod(nativeMethod.getName(), nativeMethod.getParameterTypes()));
        continue;
      }
      var forwarded = Permissions.TypedClient.class.getMethod(nativeMethod.getName(), nativeMethod.getParameterTypes());
      assertEquals(nativeMethod.getGenericReturnType().getTypeName(), forwarded.getGenericReturnType().getTypeName(), nativeMethod.toString());
      assertEquals(java.util.Arrays.stream(nativeMethod.getGenericParameterTypes()).map(java.lang.reflect.Type::getTypeName).toList(), java.util.Arrays.stream(forwarded.getGenericParameterTypes()).map(java.lang.reflect.Type::getTypeName).toList(), nativeMethod.toString());
    }
  }
}
