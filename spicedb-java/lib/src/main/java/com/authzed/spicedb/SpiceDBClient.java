package com.authzed.spicedb;

import build.buf.gen.authzed.api.v1.*;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.MetadataUtils;
import io.grpc.stub.StreamObserver;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.TimeUnit;
import java.util.stream.Stream;
import java.util.stream.StreamSupport;

/**
 * Idiomatic Java client for SpiceDB.
 *
 * <p>Implements {@link AutoCloseable} for use with try-with-resources. All streaming methods return
 * {@link Stream} instances that should also be closed when done.
 *
 * <p>Use the static factory methods to create instances:
 *
 * <pre>{@code
 * try (var client = SpiceDBClient.createPlaintext("localhost:50051", "testtoken")) {
 *     boolean allowed = client.checkPermission(
 *         Consistency.full(), "view",
 *         Relationship.of("document", "doc1", "viewer", "user", "alice"));
 * }
 * }</pre>
 */
public final class SpiceDBClient implements AutoCloseable {

  private static final int DEFAULT_READ_PAGE_SIZE = 512;
  private static final int DEFAULT_LOOKUP_PAGE_SIZE = 512;
  private static final int DEFAULT_DELETE_PAGE_SIZE = 1_000;
  private static final int DEFAULT_IMPORT_BATCH_SIZE = 1_000;
  private static final int DEFAULT_EXPORT_PAGE_SIZE = 512;
  private static final int DEFAULT_CHECK_BATCH_SIZE = 1_000;

  private static final int MAX_RETRIES = 3;
  private static final long INITIAL_BACKOFF_MS = 100;

  private final ManagedChannel channel;
  private final PermissionsServiceGrpc.PermissionsServiceBlockingStub permissionsStub;
  private final SchemaServiceGrpc.SchemaServiceBlockingStub schemaStub;
  private final WatchServiceGrpc.WatchServiceBlockingStub watchStub;
  private final ExperimentalServiceGrpc.ExperimentalServiceBlockingStub experimentalStub;
  private final PermissionsServiceGrpc.PermissionsServiceStub permissionsAsyncStub;

  private SpiceDBClient(ManagedChannel channel, Metadata metadata) {
    this.channel = channel;
    this.permissionsStub =
        PermissionsServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.schemaStub =
        SchemaServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.watchStub =
        WatchServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.experimentalStub =
        ExperimentalServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.permissionsAsyncStub =
        PermissionsServiceGrpc.newStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
  }

  /**
   * Creates a client with an insecure (plaintext) connection. Use this for testing only — the lack
   * of TLS is made obvious by the name.
   */
  public static SpiceDBClient createPlaintext(String endpoint, String presharedKey) {
    ManagedChannel channel = ManagedChannelBuilder.forTarget(endpoint).usePlaintext().build();
    return new SpiceDBClient(channel, bearerMetadata(presharedKey));
  }

  /**
   * Creates a client using the system's TLS certificate pool. Use this for production connections.
   */
  public static SpiceDBClient createSystemTls(String endpoint, String presharedKey) {
    ManagedChannel channel =
        ManagedChannelBuilder.forTarget(endpoint).useTransportSecurity().build();
    return new SpiceDBClient(channel, bearerMetadata(presharedKey));
  }

  /**
   * Creates a client with custom options.
   *
   * @param endpoint the SpiceDB endpoint
   * @param presharedKey the bearer token
   * @param options additional configuration options
   */
  public static SpiceDBClient create(
      String endpoint, String presharedKey, ClientOption... options) {
    var builder = ManagedChannelBuilder.forTarget(endpoint);
    for (ClientOption option : options) {
      option.apply(builder);
    }
    return new SpiceDBClient(builder.build(), bearerMetadata(presharedKey));
  }

  /**
   * Test-only factory that wires a client directly to a pre-built {@link ManagedChannel} (e.g. an
   * in-process transport for tests). Package-private: not part of the public API surface.
   */
  static SpiceDBClient forChannel(ManagedChannel channel) {
    return new SpiceDBClient(channel, new Metadata());
  }

  /** Functional option for customizing the client. */
  @FunctionalInterface
  public interface ClientOption {
    void apply(ManagedChannelBuilder<?> builder);
  }

  /** Option to disable TLS (plaintext). Use only for testing. */
  public static ClientOption withInsecure() {
    return ManagedChannelBuilder::usePlaintext;
  }

  // -----------------------------------------------------------------------
  // Checks — all use BulkCheckPermissions under the hood
  // -----------------------------------------------------------------------

  /**
   * Checks a single permission and returns true if granted. Uses BulkCheckPermissions under the
   * hood.
   */
  public boolean checkPermission(Consistency consistency, String permission, Relationship r) {
    List<Boolean> results = checkPermissions(consistency, permission, r);
    return results.get(0);
  }

  /**
   * Checks permissions for multiple relationships, returning a boolean for each. All checks use
   * BulkCheckPermissions under the hood.
   */
  public List<Boolean> checkPermissions(
      Consistency consistency, String permission, Relationship... relationships) {
    if (relationships.length == 0) {
      return List.of();
    }

    var items = new ArrayList<CheckBulkPermissionsRequestItem>(relationships.length);
    for (Relationship r : relationships) {
      items.add(checkItemFromRel(r, permission));
    }

    CheckBulkPermissionsResponse resp =
        withRetry(
            () ->
                permissionsStub.checkBulkPermissions(
                    CheckBulkPermissionsRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .addAllItems(items)
                        .build()));

    var results = new ArrayList<Boolean>(resp.getPairsCount());
    for (int i = 0; i < resp.getPairsCount(); i++) {
      CheckBulkPermissionsPair pair = resp.getPairs(i);
      if (pair.hasError()) {
        throw new SpiceDBException("check item " + i + ": " + pair.getError().getMessage());
      }
      results.add(
          pair.getItem().getPermissionship()
              == CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION);
    }
    return results;
  }

  /** Returns true if any of the given relationships have the permission. */
  public boolean checkAny(
      Consistency consistency, String permission, Relationship... relationships) {
    List<Boolean> results = checkPermissions(consistency, permission, relationships);
    for (boolean r : results) {
      if (r) return true;
    }
    return false;
  }

  /** Returns true if all of the given relationships have the permission. */
  public boolean checkAll(
      Consistency consistency, String permission, Relationship... relationships) {
    List<Boolean> results = checkPermissions(consistency, permission, relationships);
    for (boolean r : results) {
      if (!r) return false;
    }
    return true;
  }

  // -----------------------------------------------------------------------
  // Writes
  // -----------------------------------------------------------------------

  /**
   * Commits a transaction of relationship mutations to SpiceDB, returning the revision at which the
   * write occurred.
   */
  public String write(Transaction txn) {
    var reqBuilder = WriteRelationshipsRequest.newBuilder();

    for (Transaction.Mutation m : txn.mutations()) {
      reqBuilder.addUpdates(toRelationshipUpdate(m));
    }

    for (Transaction.Precondition p : txn.preconditions()) {
      reqBuilder.addOptionalPreconditions(toPrecondition(p));
    }

    WriteRelationshipsResponse resp =
        withRetry(() -> permissionsStub.writeRelationships(reqBuilder.build()));
    return resp.getWrittenAt().getToken();
  }

  // -----------------------------------------------------------------------
  // Read Relationships — cursor-based auto-pagination (512-item pages)
  // -----------------------------------------------------------------------

  /**
   * Returns a stream over relationships matching the given filter. Cursors are handled
   * transparently — the client automatically re-fetches pages of 512 relationships.
   *
   * <p>The returned stream should be closed when done (it is AutoCloseable).
   */
  public Stream<Relationship> readRelationships(Consistency consistency, Filter filter) {
    return paginatedRelationshipStream(consistency, filter, DEFAULT_READ_PAGE_SIZE);
  }

  // -----------------------------------------------------------------------
  // Delete Relationships — auto-paging 10,000-item batches
  // -----------------------------------------------------------------------

  /**
   * Deletes all relationships matching the given filter. Large result sets are automatically paged
   * in batches of 10,000. Returns the revision of the final deletion.
   */
  public String deleteRelationships(Filter filter) {
    String revision = "";
    while (true) {
      DeleteRelationshipsResponse resp =
          withRetry(
              () ->
                  permissionsStub.deleteRelationships(
                      DeleteRelationshipsRequest.newBuilder()
                          .setRelationshipFilter(toRelationshipFilter(filter))
                          .setOptionalLimit(DEFAULT_DELETE_PAGE_SIZE)
                          .setOptionalAllowPartialDeletions(true)
                          .build()));
      revision = resp.getDeletedAt().getToken();
      if (resp.getDeletionProgress()
          == DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_COMPLETE) {
        return revision;
      }
    }
  }

  // -----------------------------------------------------------------------
  // Lookups — cursor-based auto-pagination (512-item pages)
  // -----------------------------------------------------------------------

  /**
   * Returns a stream over resource IDs of the given type that the subject has the specified
   * permission on. Cursors are handled transparently.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<String> lookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      String subjectType,
      String subjectID) {
    Iterator<String> iterator =
        new Iterator<>() {
          private Cursor cursor = null;
          private Iterator<LookupResourcesResponse> currentPage = Collections.emptyIterator();
          private boolean done = false;
          private int pageCount = 0;

          @Override
          public boolean hasNext() {
            if (currentPage.hasNext()) return true;
            if (done) return false;
            fetchNextPage();
            return currentPage.hasNext();
          }

          @Override
          public String next() {
            if (!hasNext()) throw new NoSuchElementException();
            LookupResourcesResponse resp = currentPage.next();
            pageCount++;
            cursor = resp.getAfterResultCursor();
            return resp.getResourceObjectId();
          }

          private void fetchNextPage() {
            var reqBuilder =
                LookupResourcesRequest.newBuilder()
                    .setConsistency(consistency.toProto())
                    .setResourceObjectType(resourceType)
                    .setPermission(permission)
                    .setSubject(
                        SubjectReference.newBuilder()
                            .setObject(
                                ObjectReference.newBuilder()
                                    .setObjectType(subjectType)
                                    .setObjectId(subjectID)
                                    .build())
                            .build())
                    .setOptionalLimit(DEFAULT_LOOKUP_PAGE_SIZE);

            if (cursor != null) {
              reqBuilder.setOptionalCursor(cursor);
            }

            var responses = new ArrayList<LookupResourcesResponse>();
            var serverStream = withRetry(() -> permissionsStub.lookupResources(reqBuilder.build()));
            mapStreamErrors(
                () -> {
                  serverStream.forEachRemaining(responses::add);
                  return null;
                });

            currentPage = responses.iterator();
            if (responses.size() < DEFAULT_LOOKUP_PAGE_SIZE) {
              done = true;
            }
            if (pageCount > 0 && responses.isEmpty()) {
              done = true;
            }
            pageCount = 0;
          }
        };

    return StreamSupport.stream(
        Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false);
  }

  /**
   * Returns a stream over subject IDs of the given type that have the specified permission on the
   * resource. Unlike lookupResources, this does not use cursor-based pagination (not supported in
   * SpiceDB yet) and streams all results in a single call.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<String> lookupSubjects(
      Consistency consistency,
      String resourceType,
      String resourceID,
      String permission,
      String subjectType) {
    var responses = new ArrayList<LookupSubjectsResponse>();
    var serverStream =
        withRetry(
            () ->
                permissionsStub.lookupSubjects(
                    LookupSubjectsRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .setResource(
                            ObjectReference.newBuilder()
                                .setObjectType(resourceType)
                                .setObjectId(resourceID)
                                .build())
                        .setPermission(permission)
                        .setSubjectObjectType(subjectType)
                        .build()));
    mapStreamErrors(
        () -> {
          serverStream.forEachRemaining(responses::add);
          return null;
        });

    return responses.stream()
        .map(
            resp -> {
              String id = resp.getSubject().getSubjectObjectId();
              if (id == null || id.isEmpty()) {
                // Fall back to deprecated field
                id = resp.getSubjectObjectId();
              }
              return id;
            });
  }

  // -----------------------------------------------------------------------
  // Schema
  // -----------------------------------------------------------------------

  /** Result of a {@link #readSchema()} call. */
  public record SchemaResult(String schema, String revision) {}

  /** Returns the current SpiceDB schema. */
  public SchemaResult readSchema() {
    ReadSchemaResponse resp =
        withRetry(() -> schemaStub.readSchema(ReadSchemaRequest.getDefaultInstance()));
    return new SchemaResult(resp.getSchemaText(), resp.getReadAt().getToken());
  }

  /** Writes a new schema to SpiceDB, returning the revision. */
  public String writeSchema(String schema) {
    WriteSchemaResponse resp =
        withRetry(
            () ->
                schemaStub.writeSchema(WriteSchemaRequest.newBuilder().setSchema(schema).build()));
    return resp.getWrittenAt().getToken();
  }

  /** A definition in a SpiceDB schema. */
  public record SchemaDefinition(
      String name,
      String comment,
      List<SchemaRelation> relations,
      List<SchemaPermission> permissions) {}

  /** A relation within a schema definition. */
  public record SchemaRelation(String name, String comment, String parentDefinitionName) {}

  /** A permission within a schema definition. */
  public record SchemaPermission(String name, String comment, String parentDefinitionName) {}

  /** A caveat defined in a SpiceDB schema. */
  public record SchemaCaveat(
      String name, String comment, String expression, List<SchemaCaveatParameter> parameters) {}

  /** A parameter of a caveat. */
  public record SchemaCaveatParameter(String name, String type, String parentCaveatName) {}

  /** Result of a {@link #reflectSchema(Consistency)} call. */
  public record ReflectSchemaResult(
      List<SchemaDefinition> definitions, List<SchemaCaveat> caveats, String revision) {}

  /** Returns the definitions and caveats in the current schema. */
  public ReflectSchemaResult reflectSchema(Consistency consistency) {
    ReflectSchemaResponse resp =
        withRetry(
            () ->
                schemaStub.reflectSchema(
                    ReflectSchemaRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .build()));

    var definitions = new ArrayList<SchemaDefinition>();
    for (var def : resp.getDefinitionsList()) {
      var relations = new ArrayList<SchemaRelation>();
      for (var rel : def.getRelationsList()) {
        relations.add(
            new SchemaRelation(rel.getName(), rel.getComment(), rel.getParentDefinitionName()));
      }
      var permissions = new ArrayList<SchemaPermission>();
      for (var perm : def.getPermissionsList()) {
        permissions.add(
            new SchemaPermission(
                perm.getName(), perm.getComment(), perm.getParentDefinitionName()));
      }
      definitions.add(
          new SchemaDefinition(
              def.getName(), def.getComment(), List.copyOf(relations), List.copyOf(permissions)));
    }

    var caveats = new ArrayList<SchemaCaveat>();
    for (var cav : resp.getCaveatsList()) {
      var params = new ArrayList<SchemaCaveatParameter>();
      for (var param : cav.getParametersList()) {
        params.add(
            new SchemaCaveatParameter(
                param.getName(), param.getType(), param.getParentCaveatName()));
      }
      caveats.add(
          new SchemaCaveat(
              cav.getName(), cav.getComment(), cav.getExpression(), List.copyOf(params)));
    }

    return new ReflectSchemaResult(
        List.copyOf(definitions), List.copyOf(caveats), resp.getReadAt().getToken());
  }

  /** Identifies a relation or permission on a definition. */
  public record RelationReference(
      String definitionName, String relationName, boolean isPermission) {}

  /** Result of a {@link #computablePermissions} call. */
  public record ComputablePermissionsResult(List<RelationReference> permissions, String revision) {}

  /** Returns the permissions that are computable for the given relation. */
  public ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName) {
    ComputablePermissionsResponse resp =
        withRetry(
            () ->
                schemaStub.computablePermissions(
                    ComputablePermissionsRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .setDefinitionName(definitionName)
                        .setRelationName(relationName)
                        .build()));

    var refs = new ArrayList<RelationReference>();
    for (var perm : resp.getPermissionsList()) {
      refs.add(
          new RelationReference(
              perm.getDefinitionName(), perm.getRelationName(), perm.getIsPermission()));
    }
    return new ComputablePermissionsResult(List.copyOf(refs), resp.getReadAt().getToken());
  }

  /** Result of a {@link #dependentRelations} call. */
  public record DependentRelationsResult(List<RelationReference> relations, String revision) {}

  /** Returns the relations that the given permission depends on. */
  public DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName) {
    DependentRelationsResponse resp =
        withRetry(
            () ->
                schemaStub.dependentRelations(
                    DependentRelationsRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .setDefinitionName(definitionName)
                        .setPermissionName(permissionName)
                        .build()));

    var refs = new ArrayList<RelationReference>();
    for (var rel : resp.getRelationsList()) {
      refs.add(
          new RelationReference(
              rel.getDefinitionName(), rel.getRelationName(), rel.getIsPermission()));
    }
    return new DependentRelationsResult(List.copyOf(refs), resp.getReadAt().getToken());
  }

  /** A single difference between two schemas. */
  public record SchemaDiff(
      String kind,
      String definitionName,
      String relationName,
      String permissionName,
      String caveatName) {}

  /** Result of a {@link #diffSchema} call. */
  public record DiffSchemaResult(List<SchemaDiff> diffs, String revision) {}

  /** Compares the current schema against the given comparison schema. */
  public DiffSchemaResult diffSchema(Consistency consistency, String comparisonSchema) {
    DiffSchemaResponse resp =
        withRetry(
            () ->
                schemaStub.diffSchema(
                    DiffSchemaRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .setComparisonSchema(comparisonSchema)
                        .build()));

    var diffs = new ArrayList<SchemaDiff>();
    for (var d : resp.getDiffsList()) {
      diffs.add(schemaDiffFromProto(d));
    }
    return new DiffSchemaResult(List.copyOf(diffs), resp.getReadAt().getToken());
  }

  // -----------------------------------------------------------------------
  // Expand
  // -----------------------------------------------------------------------

  /** Result of an {@link #expandPermissionTree} call. */
  public record ExpandResult(PermissionTree tree, String revision) {}

  /**
   * Expands the permission tree for the given resource and permission, returning the full tree of
   * subjects with access.
   */
  public ExpandResult expandPermissionTree(
      Consistency consistency, String resourceType, String resourceID, String permission) {
    ExpandPermissionTreeResponse resp =
        withRetry(
            () ->
                permissionsStub.expandPermissionTree(
                    ExpandPermissionTreeRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .setResource(
                            ObjectReference.newBuilder()
                                .setObjectType(resourceType)
                                .setObjectId(resourceID)
                                .build())
                        .setPermission(permission)
                        .build()));
    return new ExpandResult(toPermissionTree(resp.getTreeRoot()), resp.getExpandedAt().getToken());
  }

  // -----------------------------------------------------------------------
  // Bulk Import / Export
  // -----------------------------------------------------------------------

  /**
   * Streams relationships to SpiceDB for bulk import, returning the number of relationships loaded.
   * Relationships are automatically batched into chunks of 1,000.
   */
  public long importRelationships(Iterable<Relationship> relationships) {
    var resultHolder = new long[1];
    var errorHolder = new Throwable[1];
    var latch = new java.util.concurrent.CountDownLatch(1);

    StreamObserver<ImportBulkRelationshipsResponse> responseObserver =
        new StreamObserver<>() {
          @Override
          public void onNext(ImportBulkRelationshipsResponse resp) {
            resultHolder[0] = resp.getNumLoaded();
          }

          @Override
          public void onError(Throwable t) {
            errorHolder[0] = t;
            latch.countDown();
          }

          @Override
          public void onCompleted() {
            latch.countDown();
          }
        };

    StreamObserver<ImportBulkRelationshipsRequest> requestObserver =
        permissionsAsyncStub.importBulkRelationships(responseObserver);

    var batch = new ArrayList<build.buf.gen.authzed.api.v1.Relationship>(DEFAULT_IMPORT_BATCH_SIZE);
    for (Relationship r : relationships) {
      batch.add(toProtoRelationship(r));
      if (batch.size() >= DEFAULT_IMPORT_BATCH_SIZE) {
        requestObserver.onNext(
            ImportBulkRelationshipsRequest.newBuilder().addAllRelationships(batch).build());
        batch.clear();
      }
    }

    if (!batch.isEmpty()) {
      requestObserver.onNext(
          ImportBulkRelationshipsRequest.newBuilder().addAllRelationships(batch).build());
    }

    requestObserver.onCompleted();

    try {
      latch.await();
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new SpiceDBException("import interrupted", e);
    }

    if (errorHolder[0] != null) {
      if (errorHolder[0] instanceof StatusRuntimeException sre) {
        throw ErrorMapper.toSpiceDBException(sre);
      }
      throw new SpiceDBException("import failed", errorHolder[0]);
    }

    return resultHolder[0];
  }

  /**
   * Returns a stream over all relationships matching the optional filter, streamed from SpiceDB in
   * bulk. Cursors are handled transparently with 512-item pages.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<Relationship> exportRelationships(Consistency consistency, Filter filter) {
    Iterator<Relationship> iterator =
        new Iterator<>() {
          private Cursor cursor = null;
          private final List<Relationship> buffer = new ArrayList<>();
          private int bufferIndex = 0;
          private boolean done = false;

          @Override
          public boolean hasNext() {
            if (bufferIndex < buffer.size()) return true;
            if (done) return false;
            fetchNextPage();
            return bufferIndex < buffer.size();
          }

          @Override
          public Relationship next() {
            if (!hasNext()) throw new NoSuchElementException();
            return buffer.get(bufferIndex++);
          }

          private void fetchNextPage() {
            buffer.clear();
            bufferIndex = 0;

            var reqBuilder =
                ExportBulkRelationshipsRequest.newBuilder()
                    .setConsistency(consistency.toProto())
                    .setOptionalLimit(DEFAULT_EXPORT_PAGE_SIZE);

            if (filter != null) {
              reqBuilder.setOptionalRelationshipFilter(toRelationshipFilter(filter));
            }
            if (cursor != null) {
              reqBuilder.setOptionalCursor(cursor);
            }

            var serverStream =
                withRetry(() -> permissionsStub.exportBulkRelationships(reqBuilder.build()));

            int pageCount = 0;
            while (mapStreamErrors(serverStream::hasNext)) {
              ExportBulkRelationshipsResponse resp = mapStreamErrors(serverStream::next);
              cursor = resp.getAfterResultCursor();
              for (var r : resp.getRelationshipsList()) {
                buffer.add(fromProtoRelationship(r));
                pageCount++;
              }
            }

            if (pageCount < DEFAULT_EXPORT_PAGE_SIZE) {
              done = true;
            }
          }
        };

    return StreamSupport.stream(
        Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false);
  }

  // -----------------------------------------------------------------------
  // Watch
  // -----------------------------------------------------------------------

  /** A relationship update from the watch API. */
  public record Update(UpdateOperation operation, Relationship relationship) {}

  /** The type of mutation in an Update. */
  public enum UpdateOperation {
    CREATE,
    TOUCH,
    DELETE
  }

  /**
   * Returns a stream over relationship changes from SpiceDB's watch API, starting from the given
   * revision.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<Update> updates(List<String> objectTypes, String startRevision) {
    var reqBuilder = WatchRequest.newBuilder();
    if (objectTypes != null) {
      reqBuilder.addAllOptionalObjectTypes(objectTypes);
    }
    if (startRevision != null && !startRevision.isEmpty()) {
      reqBuilder.setOptionalStartCursor(ZedToken.newBuilder().setToken(startRevision).build());
    }

    var serverStream = withRetry(() -> watchStub.watch(reqBuilder.build()));

    Iterator<Update> iterator =
        new Iterator<>() {
          private final Queue<Update> buffer = new ArrayDeque<>();

          @Override
          public boolean hasNext() {
            if (!buffer.isEmpty()) return true;
            if (!mapStreamErrors(serverStream::hasNext)) return false;
            WatchResponse resp = mapStreamErrors(serverStream::next);
            for (var u : resp.getUpdatesList()) {
              buffer.add(updateFromProto(u));
            }
            return !buffer.isEmpty();
          }

          @Override
          public Update next() {
            if (!hasNext()) throw new NoSuchElementException();
            return buffer.poll();
          }
        };

    return StreamSupport.stream(
        Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false);
  }

  // -----------------------------------------------------------------------
  // Experimental — these APIs may change without following the backwards
  // compatibility mandate
  // -----------------------------------------------------------------------

  /**
   * Registers a named counter that tracks relationships matching the given filter. The counter is
   * computed asynchronously by SpiceDB.
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public void experimentalRegisterRelationshipCounter(String name, Filter filter) {
    withRetry(
        () ->
            experimentalStub.experimentalRegisterRelationshipCounter(
                ExperimentalRegisterRelationshipCounterRequest.newBuilder()
                    .setName(name)
                    .setRelationshipFilter(toRelationshipFilter(filter))
                    .build()));
  }

  /** Result of an {@link #experimentalCountRelationships} call. */
  public record CountResult(long relationshipCount, String revision, boolean stillCalculating) {}

  /**
   * Reads the value of a previously registered relationship counter.
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public CountResult experimentalCountRelationships(String name) {
    ExperimentalCountRelationshipsResponse resp =
        withRetry(
            () ->
                experimentalStub.experimentalCountRelationships(
                    ExperimentalCountRelationshipsRequest.newBuilder().setName(name).build()));

    if (resp.getCounterStillCalculating()) {
      return new CountResult(0, "", true);
    }

    var cv = resp.getReadCounterValue();
    return new CountResult(cv.getRelationshipCount(), cv.getReadAt().getToken(), false);
  }

  /**
   * Removes a previously registered relationship counter.
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public void experimentalUnregisterRelationshipCounter(String name) {
    withRetry(
        () ->
            experimentalStub.experimentalUnregisterRelationshipCounter(
                ExperimentalUnregisterRelationshipCounterRequest.newBuilder()
                    .setName(name)
                    .build()));
  }

  // -----------------------------------------------------------------------
  // AutoCloseable
  // -----------------------------------------------------------------------

  @Override
  public void close() {
    channel.shutdown();
    try {
      if (!channel.awaitTermination(5, TimeUnit.SECONDS)) {
        channel.shutdownNow();
      }
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      channel.shutdownNow();
    }
  }

  // -----------------------------------------------------------------------
  // Internal helpers
  // -----------------------------------------------------------------------

  private static Metadata bearerMetadata(String presharedKey) {
    Metadata metadata = new Metadata();
    metadata.put(
        Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER),
        "Bearer " + presharedKey);
    return metadata;
  }

  /** Retry with exponential backoff for transient gRPC errors. */
  @FunctionalInterface
  private interface RetryableCall<T> {
    T call();
  }

  /**
   * Runs a blocking mid-stream operation (e.g. {@code serverStream.hasNext()}/{@code next()}),
   * mapping any {@link StatusRuntimeException} it throws to a typed {@link SpiceDBException}.
   *
   * <p>Unlike {@link #withRetry}, this does NOT retry — mid-stream errors (including transient
   * ones) are not safely retryable without re-issuing the whole call, since some results may have
   * already been delivered to the consumer.
   */
  private static <T> T mapStreamErrors(java.util.function.Supplier<T> op) {
    try {
      return op.get();
    } catch (StatusRuntimeException e) {
      throw ErrorMapper.toSpiceDBException(e);
    }
  }

  private <T> T withRetry(RetryableCall<T> call) {
    long backoff = INITIAL_BACKOFF_MS;
    for (int attempt = 0; attempt < MAX_RETRIES; attempt++) {
      try {
        return call.call();
      } catch (StatusRuntimeException e) {
        if (!ErrorMapper.isTransient(e) || attempt == MAX_RETRIES - 1) {
          throw ErrorMapper.toSpiceDBException(e);
        }
        try {
          Thread.sleep(backoff);
        } catch (InterruptedException ie) {
          Thread.currentThread().interrupt();
          throw ErrorMapper.toSpiceDBException(e);
        }
        backoff *= 2;
      }
    }
    throw new SpiceDBException("unreachable");
  }

  private Stream<Relationship> paginatedRelationshipStream(
      Consistency consistency, Filter filter, int pageSize) {
    Iterator<Relationship> iterator =
        new Iterator<>() {
          private Cursor cursor = null;
          private final List<Relationship> buffer = new ArrayList<>();
          private int bufferIndex = 0;
          private boolean done = false;

          @Override
          public boolean hasNext() {
            if (bufferIndex < buffer.size()) return true;
            if (done) return false;
            fetchNextPage();
            return bufferIndex < buffer.size();
          }

          @Override
          public Relationship next() {
            if (!hasNext()) throw new NoSuchElementException();
            return buffer.get(bufferIndex++);
          }

          private void fetchNextPage() {
            buffer.clear();
            bufferIndex = 0;

            var reqBuilder =
                ReadRelationshipsRequest.newBuilder()
                    .setConsistency(consistency.toProto())
                    .setRelationshipFilter(toRelationshipFilter(filter))
                    .setOptionalLimit(pageSize);

            if (cursor != null) {
              reqBuilder.setOptionalCursor(cursor);
            }

            var serverStream =
                withRetry(() -> permissionsStub.readRelationships(reqBuilder.build()));

            while (mapStreamErrors(serverStream::hasNext)) {
              ReadRelationshipsResponse resp = mapStreamErrors(serverStream::next);
              cursor = resp.getAfterResultCursor();
              buffer.add(fromProtoRelationship(resp.getRelationship()));
            }

            if (buffer.size() < pageSize) {
              done = true;
            }
          }
        };

    return StreamSupport.stream(
        Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false);
  }

  private static CheckBulkPermissionsRequestItem checkItemFromRel(
      Relationship r, String permission) {
    return CheckBulkPermissionsRequestItem.newBuilder()
        .setResource(
            ObjectReference.newBuilder()
                .setObjectType(r.resourceType())
                .setObjectId(r.resourceID())
                .build())
        .setPermission(permission)
        .setSubject(
            SubjectReference.newBuilder()
                .setObject(
                    ObjectReference.newBuilder()
                        .setObjectType(r.subjectType())
                        .setObjectId(r.subjectID())
                        .build())
                .setOptionalRelation(r.subjectRelation() != null ? r.subjectRelation() : "")
                .build())
        .build();
  }

  private static RelationshipUpdate toRelationshipUpdate(Transaction.Mutation m) {
    RelationshipUpdate.Operation op =
        switch (m.operation()) {
          case CREATE -> RelationshipUpdate.Operation.OPERATION_CREATE;
          case TOUCH -> RelationshipUpdate.Operation.OPERATION_TOUCH;
          case DELETE -> RelationshipUpdate.Operation.OPERATION_DELETE;
        };
    return RelationshipUpdate.newBuilder()
        .setOperation(op)
        .setRelationship(toProtoRelationship(m.relationship()))
        .build();
  }

  private static Precondition toPrecondition(Transaction.Precondition p) {
    Precondition.Operation op =
        switch (p.operation()) {
          case MUST_NOT_MATCH -> Precondition.Operation.OPERATION_MUST_NOT_MATCH;
          case MUST_MATCH -> Precondition.Operation.OPERATION_MUST_MATCH;
        };
    return Precondition.newBuilder()
        .setOperation(op)
        .setFilter(toRelationshipFilter(p.filter()))
        .build();
  }

  static build.buf.gen.authzed.api.v1.Relationship toProtoRelationship(Relationship r) {
    var builder =
        build.buf.gen.authzed.api.v1.Relationship.newBuilder()
            .setResource(
                ObjectReference.newBuilder()
                    .setObjectType(r.resourceType())
                    .setObjectId(r.resourceID())
                    .build())
            .setRelation(r.resourceRelation())
            .setSubject(
                SubjectReference.newBuilder()
                    .setObject(
                        ObjectReference.newBuilder()
                            .setObjectType(r.subjectType())
                            .setObjectId(r.subjectID())
                            .build())
                    .setOptionalRelation(r.subjectRelation() != null ? r.subjectRelation() : "")
                    .build());

    if (r.caveatName() != null && !r.caveatName().isEmpty()) {
      var caveatBuilder = ContextualizedCaveat.newBuilder().setCaveatName(r.caveatName());
      if (r.caveatContext() != null) {
        var structBuilder = com.google.protobuf.Struct.newBuilder();
        for (var entry : r.caveatContext().entrySet()) {
          structBuilder.putFields(entry.getKey(), toProtoValue(entry.getValue()));
        }
        caveatBuilder.setContext(structBuilder.build());
      }
      builder.setOptionalCaveat(caveatBuilder.build());
    }

    if (r.expiration() != null) {
      builder.setOptionalExpiresAt(
          com.google.protobuf.Timestamp.newBuilder()
              .setSeconds(r.expiration().getEpochSecond())
              .setNanos(r.expiration().getNano())
              .build());
    }

    return builder.build();
  }

  static Relationship fromProtoRelationship(build.buf.gen.authzed.api.v1.Relationship pr) {
    String caveatName = null;
    Map<String, Object> caveatContext = null;
    if (pr.hasOptionalCaveat()) {
      caveatName = pr.getOptionalCaveat().getCaveatName();
      if (pr.getOptionalCaveat().hasContext()) {
        caveatContext = new HashMap<>();
        for (var entry : pr.getOptionalCaveat().getContext().getFieldsMap().entrySet()) {
          caveatContext.put(entry.getKey(), fromProtoValue(entry.getValue()));
        }
      }
    }

    Instant expiration = null;
    if (pr.hasOptionalExpiresAt()) {
      expiration =
          Instant.ofEpochSecond(
              pr.getOptionalExpiresAt().getSeconds(), pr.getOptionalExpiresAt().getNanos());
    }

    return new Relationship(
        pr.getResource().getObjectType(),
        pr.getResource().getObjectId(),
        pr.getRelation(),
        pr.getSubject().getObject().getObjectType(),
        pr.getSubject().getObject().getObjectId(),
        pr.getSubject().getOptionalRelation(),
        caveatName,
        caveatContext,
        expiration);
  }

  /**
   * Recursively maps a proto {@code PermissionRelationshipTree} to its native {@link
   * PermissionTree} representation. A null input maps to a zero-value tree.
   */
  static PermissionTree toPermissionTree(PermissionRelationshipTree t) {
    if (t == null) {
      return new PermissionTree(new PermissionTree.ObjectRef("", ""), "", null, null);
    }

    PermissionTree.IntermediateNode intermediate = null;
    if (t.hasIntermediate()) {
      AlgebraicSubjectSet algebraic = t.getIntermediate();
      var children = new ArrayList<PermissionTree>(algebraic.getChildrenCount());
      for (var child : algebraic.getChildrenList()) {
        children.add(toPermissionTree(child));
      }
      intermediate =
          new PermissionTree.IntermediateNode(
              toTreeOperation(algebraic.getOperation()), List.copyOf(children));
    }

    PermissionTree.LeafNode leaf = null;
    if (t.hasLeaf()) {
      DirectSubjectSet direct = t.getLeaf();
      var subjects = new ArrayList<PermissionTree.SubjectRef>(direct.getSubjectsCount());
      for (var subject : direct.getSubjectsList()) {
        subjects.add(
            new PermissionTree.SubjectRef(
                subject.getObject().getObjectType(),
                subject.getObject().getObjectId(),
                subject.getOptionalRelation()));
      }
      leaf = new PermissionTree.LeafNode(List.copyOf(subjects));
    }

    return new PermissionTree(
        new PermissionTree.ObjectRef(
            t.getExpandedObject().getObjectType(), t.getExpandedObject().getObjectId()),
        t.getExpandedRelation(),
        intermediate,
        leaf);
  }

  /** Maps the proto algebraic set operation to its native equivalent. */
  private static PermissionTree.Operation toTreeOperation(AlgebraicSubjectSet.Operation op) {
    return switch (op) {
      case OPERATION_UNION -> PermissionTree.Operation.UNION;
      case OPERATION_INTERSECTION -> PermissionTree.Operation.INTERSECTION;
      case OPERATION_EXCLUSION -> PermissionTree.Operation.EXCLUSION;
      default -> PermissionTree.Operation.UNSPECIFIED;
    };
  }

  private static RelationshipFilter toRelationshipFilter(Filter f) {
    var builder = RelationshipFilter.newBuilder().setResourceType(f.resourceType());

    if (f.resourceID() != null && !f.resourceID().isEmpty()) {
      builder.setOptionalResourceId(f.resourceID());
    }
    if (f.resourceIDPrefix() != null && !f.resourceIDPrefix().isEmpty()) {
      builder.setOptionalResourceIdPrefix(f.resourceIDPrefix());
    }
    if (f.relation() != null && !f.relation().isEmpty()) {
      builder.setOptionalRelation(f.relation());
    }
    if (f.subjectType() != null && !f.subjectType().isEmpty()) {
      var subjectBuilder = SubjectFilter.newBuilder().setSubjectType(f.subjectType());
      if (f.subjectID() != null && !f.subjectID().isEmpty()) {
        subjectBuilder.setOptionalSubjectId(f.subjectID());
      }
      if (f.subjectRelation() != null && !f.subjectRelation().isEmpty()) {
        subjectBuilder.setOptionalRelation(
            SubjectFilter.RelationFilter.newBuilder().setRelation(f.subjectRelation()).build());
      }
      builder.setOptionalSubjectFilter(subjectBuilder.build());
    }
    return builder.build();
  }

  private Update updateFromProto(RelationshipUpdate pu) {
    UpdateOperation op =
        switch (pu.getOperation()) {
          case OPERATION_CREATE -> UpdateOperation.CREATE;
          case OPERATION_TOUCH -> UpdateOperation.TOUCH;
          case OPERATION_DELETE -> UpdateOperation.DELETE;
          default -> UpdateOperation.TOUCH;
        };
    return new Update(op, fromProtoRelationship(pu.getRelationship()));
  }

  private SchemaDiff schemaDiffFromProto(ReflectionSchemaDiff d) {
    // Map each diff case to a descriptive kind string
    if (d.hasDefinitionAdded()) {
      return new SchemaDiff("definition_added", d.getDefinitionAdded().getName(), "", "", "");
    } else if (d.hasDefinitionRemoved()) {
      return new SchemaDiff("definition_removed", d.getDefinitionRemoved().getName(), "", "", "");
    } else if (d.hasDefinitionDocCommentChanged()) {
      return new SchemaDiff(
          "definition_doc_comment_changed",
          d.getDefinitionDocCommentChanged().getName(),
          "",
          "",
          "");
    } else if (d.hasRelationAdded()) {
      return new SchemaDiff(
          "relation_added",
          d.getRelationAdded().getParentDefinitionName(),
          d.getRelationAdded().getName(),
          "",
          "");
    } else if (d.hasRelationRemoved()) {
      return new SchemaDiff(
          "relation_removed",
          d.getRelationRemoved().getParentDefinitionName(),
          d.getRelationRemoved().getName(),
          "",
          "");
    } else if (d.hasRelationDocCommentChanged()) {
      return new SchemaDiff(
          "relation_doc_comment_changed",
          d.getRelationDocCommentChanged().getParentDefinitionName(),
          d.getRelationDocCommentChanged().getName(),
          "",
          "");
    } else if (d.hasRelationSubjectTypeAdded()) {
      return new SchemaDiff(
          "relation_subject_type_added",
          d.getRelationSubjectTypeAdded().getRelation().getParentDefinitionName(),
          d.getRelationSubjectTypeAdded().getRelation().getName(),
          "",
          "");
    } else if (d.hasRelationSubjectTypeRemoved()) {
      return new SchemaDiff(
          "relation_subject_type_removed",
          d.getRelationSubjectTypeRemoved().getRelation().getParentDefinitionName(),
          d.getRelationSubjectTypeRemoved().getRelation().getName(),
          "",
          "");
    } else if (d.hasPermissionAdded()) {
      return new SchemaDiff(
          "permission_added",
          d.getPermissionAdded().getParentDefinitionName(),
          "",
          d.getPermissionAdded().getName(),
          "");
    } else if (d.hasPermissionRemoved()) {
      return new SchemaDiff(
          "permission_removed",
          d.getPermissionRemoved().getParentDefinitionName(),
          "",
          d.getPermissionRemoved().getName(),
          "");
    } else if (d.hasPermissionDocCommentChanged()) {
      return new SchemaDiff(
          "permission_doc_comment_changed",
          d.getPermissionDocCommentChanged().getParentDefinitionName(),
          "",
          d.getPermissionDocCommentChanged().getName(),
          "");
    } else if (d.hasPermissionExprChanged()) {
      return new SchemaDiff(
          "permission_expr_changed",
          d.getPermissionExprChanged().getParentDefinitionName(),
          "",
          d.getPermissionExprChanged().getName(),
          "");
    } else if (d.hasCaveatAdded()) {
      return new SchemaDiff("caveat_added", "", "", "", d.getCaveatAdded().getName());
    } else if (d.hasCaveatRemoved()) {
      return new SchemaDiff("caveat_removed", "", "", "", d.getCaveatRemoved().getName());
    } else if (d.hasCaveatDocCommentChanged()) {
      return new SchemaDiff(
          "caveat_doc_comment_changed", "", "", "", d.getCaveatDocCommentChanged().getName());
    } else if (d.hasCaveatExprChanged()) {
      return new SchemaDiff("caveat_expr_changed", "", "", "", d.getCaveatExprChanged().getName());
    } else if (d.hasCaveatParameterAdded()) {
      return new SchemaDiff(
          "caveat_parameter_added", "", "", "", d.getCaveatParameterAdded().getParentCaveatName());
    } else if (d.hasCaveatParameterRemoved()) {
      return new SchemaDiff(
          "caveat_parameter_removed",
          "",
          "",
          "",
          d.getCaveatParameterRemoved().getParentCaveatName());
    } else if (d.hasCaveatParameterTypeChanged()) {
      return new SchemaDiff(
          "caveat_parameter_type_changed",
          "",
          "",
          "",
          d.getCaveatParameterTypeChanged().getParameter().getParentCaveatName());
    }
    return new SchemaDiff("unknown", "", "", "", "");
  }

  private static com.google.protobuf.Value toProtoValue(Object value) {
    if (value == null) {
      return com.google.protobuf.Value.newBuilder()
          .setNullValue(com.google.protobuf.NullValue.NULL_VALUE)
          .build();
    } else if (value instanceof Boolean b) {
      return com.google.protobuf.Value.newBuilder().setBoolValue(b).build();
    } else if (value instanceof Number n) {
      return com.google.protobuf.Value.newBuilder().setNumberValue(n.doubleValue()).build();
    } else if (value instanceof String s) {
      return com.google.protobuf.Value.newBuilder().setStringValue(s).build();
    } else {
      return com.google.protobuf.Value.newBuilder().setStringValue(value.toString()).build();
    }
  }

  private static Object fromProtoValue(com.google.protobuf.Value value) {
    return switch (value.getKindCase()) {
      case NULL_VALUE -> null;
      case BOOL_VALUE -> value.getBoolValue();
      case NUMBER_VALUE -> value.getNumberValue();
      case STRING_VALUE -> value.getStringValue();
      default -> value.toString();
    };
  }
}
