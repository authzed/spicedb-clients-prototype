package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.LookupPermissionship;
import build.buf.gen.authzed.api.v1.LookupResourcesRequest;
import build.buf.gen.authzed.api.v1.LookupResourcesResponse;
import build.buf.gen.authzed.api.v1.LookupSubjectsRequest;
import build.buf.gen.authzed.api.v1.LookupSubjectsResponse;
import build.buf.gen.authzed.api.v1.PartialCaveatInfo;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ResolvedSubject;
import build.buf.gen.authzed.api.v1.ZedToken;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * Proves that {@link SpiceDBClient#lookupResources} and {@link SpiceDBClient#lookupSubjects} yield
 * rich native {@link LookupResult} records (permissionship, partial caveat info, and — critically —
 * excluded subjects for wildcard {@code "*"} matches) instead of bare {@code String}s.
 *
 * <p>Also proves the deprecated response-level fallback fields (used by older SpiceDB servers that
 * don't yet populate the non-deprecated {@code subject}/{@code excluded_subjects} fields) are
 * honored, mirroring {@code spicedb-go}'s {@code lookup.go}.
 */
class LookupResultsTest {

  // ---------------------------------------------------------------------
  // lookupResources — permissionship + partial caveat info
  // ---------------------------------------------------------------------

  @Test
  void lookupResourcesSurfacesHasPermission() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest request,
              StreamObserver<LookupResourcesResponse> responseObserver) {
            responseObserver.onNext(
                LookupResourcesResponse.newBuilder()
                    .setResourceObjectId("firstdoc")
                    .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                    .setLookedUpAt(ZedToken.newBuilder().setToken("lookup-rev-1").build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<LookupResult.LookupResource> results;
      try (Stream<LookupResult.LookupResource> stream =
          client.lookupResources(Consistency.full(), "document", "view", "user", "alice")) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      LookupResult.LookupResource result = results.get(0);
      assertEquals("firstdoc", result.resourceId());
      assertEquals(LookupResult.Permissionship.HAS_PERMISSION, result.permissionship());
      assertNull(result.partialCaveat());
      assertEquals("lookup-rev-1", result.lookedUpAt());
    }
  }

  @Test
  void lookupResourcesWithDebugSetsRequestFlag() throws IOException {
    var captured = new ArrayList<LookupResourcesRequest>();
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest request,
              StreamObserver<LookupResourcesResponse> responseObserver) {
            captured.add(request);
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<LookupResult.LookupResource> stream =
          client.lookupResources(Consistency.full(), "document", "view", "user", "alice", true)) {
        stream.toList();
      }

      assertEquals(1, captured.size());
      assertTrue(captured.get(0).getWithDebug());
    }
  }

  @Test
  void lookupResourcesDefaultsWithDebugToFalse() throws IOException {
    var captured = new ArrayList<LookupResourcesRequest>();
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest request,
              StreamObserver<LookupResourcesResponse> responseObserver) {
            captured.add(request);
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<LookupResult.LookupResource> stream =
          client.lookupResources(Consistency.full(), "document", "view", "user", "alice")) {
        stream.toList();
      }

      assertEquals(1, captured.size());
      assertFalse(captured.get(0).getWithDebug());
    }
  }

  @Test
  void lookupResourcesSurfacesConditionalPermissionWithPartialCaveat() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest request,
              StreamObserver<LookupResourcesResponse> responseObserver) {
            responseObserver.onNext(
                LookupResourcesResponse.newBuilder()
                    .setResourceObjectId("seconddoc")
                    .setPermissionship(
                        LookupPermissionship.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION)
                    .setPartialCaveatInfo(
                        PartialCaveatInfo.newBuilder().addMissingRequiredContext("region").build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<LookupResult.LookupResource> results;
      try (Stream<LookupResult.LookupResource> stream =
          client.lookupResources(Consistency.full(), "document", "view", "user", "alice")) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      LookupResult.LookupResource result = results.get(0);
      assertEquals("seconddoc", result.resourceId());
      assertEquals(LookupResult.Permissionship.CONDITIONAL_PERMISSION, result.permissionship());
      assertNotNull(result.partialCaveat());
      assertEquals(List.of("region"), result.partialCaveat().missingRequiredContext());
    }
  }

  // ---------------------------------------------------------------------
  // lookupSubjects — the critical wildcard-excluded-subjects case
  // ---------------------------------------------------------------------

  @Test
  void lookupSubjectsSurfacesWildcardExcludedSubjects() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupSubjects(
              LookupSubjectsRequest request,
              StreamObserver<LookupSubjectsResponse> responseObserver) {
            responseObserver.onNext(
                LookupSubjectsResponse.newBuilder()
                    .setLookedUpAt(ZedToken.newBuilder().setToken("lookup-rev-wildcard").build())
                    .setSubject(
                        ResolvedSubject.newBuilder()
                            .setSubjectObjectId("*")
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                            .build())
                    .addExcludedSubjects(
                        ResolvedSubject.newBuilder()
                            .setSubjectObjectId("banned-alice")
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                            .build())
                    .addExcludedSubjects(
                        ResolvedSubject.newBuilder()
                            .setSubjectObjectId("banned-bob")
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION)
                            .setPartialCaveatInfo(
                                PartialCaveatInfo.newBuilder()
                                    .addMissingRequiredContext("ip")
                                    .build())
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<LookupResult.LookupSubject> results;
      try (Stream<LookupResult.LookupSubject> stream =
          client.lookupSubjects(Consistency.full(), "document", "doc1", "view", "user")) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      LookupResult.LookupSubject result = results.get(0);
      assertEquals("*", result.subject().subjectId());
      assertEquals(LookupResult.Permissionship.HAS_PERMISSION, result.subject().permissionship());
      assertEquals("lookup-rev-wildcard", result.lookedUpAt());

      // The over-grant risk this task exists to fix: excluded subjects MUST be surfaced so
      // callers can exclude them from a wildcard grant, rather than silently disappearing.
      assertEquals(2, result.excludedSubjects().size());
      List<String> excludedIds =
          result.excludedSubjects().stream().map(LookupResult.ResolvedSubject::subjectId).toList();
      assertTrue(excludedIds.containsAll(List.of("banned-alice", "banned-bob")));

      LookupResult.ResolvedSubject bannedBob =
          result.excludedSubjects().stream()
              .filter(s -> s.subjectId().equals("banned-bob"))
              .findFirst()
              .orElseThrow();
      assertEquals(LookupResult.Permissionship.CONDITIONAL_PERMISSION, bannedBob.permissionship());
      assertEquals(List.of("ip"), bannedBob.partialCaveat().missingRequiredContext());
    }
  }

  @Test
  void lookupSubjectsNonWildcardHasNoExcludedSubjects() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupSubjects(
              LookupSubjectsRequest request,
              StreamObserver<LookupSubjectsResponse> responseObserver) {
            responseObserver.onNext(
                LookupSubjectsResponse.newBuilder()
                    .setSubject(
                        ResolvedSubject.newBuilder()
                            .setSubjectObjectId("alice")
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<LookupResult.LookupSubject> results;
      try (Stream<LookupResult.LookupSubject> stream =
          client.lookupSubjects(Consistency.full(), "document", "doc1", "view", "user")) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      assertEquals("alice", results.get(0).subject().subjectId());
      assertTrue(results.get(0).excludedSubjects().isEmpty());
    }
  }

  // ---------------------------------------------------------------------
  // Deprecated field fallbacks — servers that don't yet populate `subject`/
  // `excluded_subjects` (mirrors spicedb-go's lookup.go fallback handling).
  // ---------------------------------------------------------------------

  @Test
  @SuppressWarnings("deprecation") // intentionally exercises deprecated proto fields
  void lookupSubjectsFallsBackToDeprecatedSubjectFields() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupSubjects(
              LookupSubjectsRequest request,
              StreamObserver<LookupSubjectsResponse> responseObserver) {
            responseObserver.onNext(
                LookupSubjectsResponse.newBuilder()
                    // Only the deprecated top-level fields are set — no `subject`.
                    .setSubjectObjectId("legacy-alice")
                    .setPermissionship(
                        LookupPermissionship.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION)
                    .setPartialCaveatInfo(
                        PartialCaveatInfo.newBuilder().addMissingRequiredContext("time").build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<LookupResult.LookupSubject> results;
      try (Stream<LookupResult.LookupSubject> stream =
          client.lookupSubjects(Consistency.full(), "document", "doc1", "view", "user")) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      LookupResult.ResolvedSubject subject = results.get(0).subject();
      assertEquals("legacy-alice", subject.subjectId());
      assertEquals(LookupResult.Permissionship.CONDITIONAL_PERMISSION, subject.permissionship());
      assertEquals(List.of("time"), subject.partialCaveat().missingRequiredContext());
    }
  }

  @Test
  @SuppressWarnings("deprecation") // intentionally exercises deprecated proto fields
  void lookupSubjectsFallsBackToDeprecatedExcludedSubjectIds() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupSubjects(
              LookupSubjectsRequest request,
              StreamObserver<LookupSubjectsResponse> responseObserver) {
            responseObserver.onNext(
                LookupSubjectsResponse.newBuilder()
                    .setSubject(
                        ResolvedSubject.newBuilder()
                            .setSubjectObjectId("*")
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                            .build())
                    // Only the deprecated ID-only exclusion list is set — no `excluded_subjects`.
                    .addExcludedSubjectIds("legacy-banned-1")
                    .addExcludedSubjectIds("legacy-banned-2")
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<LookupResult.LookupSubject> results;
      try (Stream<LookupResult.LookupSubject> stream =
          client.lookupSubjects(Consistency.full(), "document", "doc1", "view", "user")) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      List<LookupResult.ResolvedSubject> excluded = results.get(0).excludedSubjects();
      assertEquals(2, excluded.size());
      List<String> excludedIds =
          excluded.stream().map(LookupResult.ResolvedSubject::subjectId).toList();
      assertTrue(excludedIds.containsAll(List.of("legacy-banned-1", "legacy-banned-2")));
      // Deprecated excluded_subject_ids carries only IDs — no permissionship/caveat info.
      for (LookupResult.ResolvedSubject s : excluded) {
        assertEquals(LookupResult.Permissionship.UNSPECIFIED, s.permissionship());
        assertNull(s.partialCaveat());
      }
    }
  }
}
