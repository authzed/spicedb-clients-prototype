package com.authzed.spicedb;

import build.buf.gen.authzed.api.v1.*;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.Context;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
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
 *     CheckResult result = client.checkPermission(
 *         Consistency.full(), "view",
 *         Relationship.of("document", "doc1", "viewer", "user", "alice"));
 *     boolean allowed = result.hasPermission();
 * }
 * }</pre>
 */
public final class SpiceDBClient implements AutoCloseable {

  private static final int DEFAULT_READ_PAGE_SIZE = 512;
  private static final int DEFAULT_LOOKUP_PAGE_SIZE = 512;
  private static final int DEFAULT_DELETE_PAGE_SIZE = 1_000;
  private static final int DEFAULT_IMPORT_BATCH_SIZE = 1_000;
  private static final int DEFAULT_EXPORT_PAGE_SIZE = 512;

  private static final int MAX_RETRIES = 4;
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
   * <p>{@link ClientOption}s may include advanced escape-hatch options that expose the underlying
   * gRPC channel builder for configuration not covered by the primary API. Most users should prefer
   * {@link #createPlaintext} or {@link #createSystemTls}.
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
    /**
     * Applies this option to the underlying gRPC {@link ManagedChannelBuilder}.
     *
     * <p><b>Advanced escape hatch:</b> this method exposes {@code io.grpc.ManagedChannelBuilder}
     * directly for configuration not covered by the primary API. Most users should prefer {@link
     * #createPlaintext} or {@link #createSystemTls} and the standard {@code withInsecure()} option.
     *
     * @param builder the channel builder to configure
     */
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
   * Checks a single permission, returning a {@link CheckResult} carrying the server's full
   * three-valued answer, the caveat context that was missing (if any), and the {@link ZedToken}
   * revision the check was evaluated at. Uses BulkCheckPermissions under the hood.
   *
   * <p><b>RULE (root DESIGN.md, "Only an unconditional grant is true"):</b> prefer {@link
   * CheckResult#hasPermission()} over comparing {@link CheckResult#permissionship()} directly — a
   * {@code CONDITIONAL_PERMISSION} result means the server needed caveat context that was not
   * supplied, and is NOT a grant.
   */
  public CheckResult checkPermission(Consistency consistency, String permission, Relationship r) {
    List<CheckResult> results = checkPermissions(consistency, permission, r);
    return results.get(0);
  }

  /**
   * Checks a single permission with a caveat CHECK-TIME {@code context}, in addition to any
   * per-item context set via {@link Relationship#withCheckContext} on {@code r} itself (item wins
   * per-key over this call-level default; see {@link #checkPermissions(Consistency, String, Map,
   * Relationship...)} for the merge rule). Distinct from {@code r}'s write-time {@code
   * caveatContext} (set via {@link Relationship#withCaveat}) — this context is never written, only
   * used to evaluate the caveat for this check.
   *
   * <p>See {@link #checkPermission(Consistency, String, Relationship)} for the RULE governing how
   * to interpret the result.
   */
  public CheckResult checkPermission(
      Consistency consistency, String permission, Relationship r, Map<String, Object> context) {
    List<CheckResult> results = checkPermissions(consistency, permission, context, r);
    return results.get(0);
  }

  /**
   * Checks permissions for multiple relationships, returning a {@link CheckResult} for each. All
   * checks use BulkCheckPermissions under the hood.
   *
   * <p>See {@link #checkPermission} for the RULE governing how to interpret each result.
   */
  public List<CheckResult> checkPermissions(
      Consistency consistency, String permission, Relationship... relationships) {
    return checkPermissions(consistency, permission, (Map<String, Object>) null, relationships);
  }

  /**
   * Checks permissions for multiple relationships with a caveat CHECK-TIME {@code context} applied,
   * by default, to every relationship — plus any per-item context set via {@link
   * Relationship#withCheckContext} on individual relationships.
   *
   * <p><b>Merge rule (key-level, item wins):</b> for each relationship, the context sent to the
   * server is the call-level {@code context} map with that relationship's own {@link
   * Relationship#checkContext()} entries overwriting matching keys — call-level keys absent from
   * the item are retained, never wholesale-replaced. For example, call-level {@code {now: 42,
   * region: "us"}} plus a per-item {@code {region: "eu"}} sends {@code {now: 42, region: "eu"}} for
   * that item, while a sibling relationship with no per-item context still sends {@code {now: 42,
   * region: "us"}}. When neither call-level nor per-item context is supplied for a relationship, no
   * {@code context} field is set on the wire at all (not an empty {@code Struct}).
   *
   * <p>Distinct from write-time {@code caveatContext} (set via {@link Relationship#withCaveat}) —
   * this context is never written, only used to evaluate the caveat for this check.
   *
   * <p>See {@link #checkPermission} for the RULE governing how to interpret each result.
   */
  public List<CheckResult> checkPermissions(
      Consistency consistency,
      String permission,
      Map<String, Object> context,
      Relationship... relationships) {
    if (relationships.length == 0) {
      return List.of();
    }

    var items = new ArrayList<CheckBulkPermissionsRequestItem>(relationships.length);
    for (Relationship r : relationships) {
      items.add(checkItemFromRel(r, permission, context));
    }

    CheckBulkPermissionsResponse resp =
        withRetry(
            () ->
                permissionsStub.checkBulkPermissions(
                    CheckBulkPermissionsRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .addAllItems(items)
                        .build()));

    // CheckBulkPermissionsResponseItem carries no per-item checked_at of its own — the token lives
    // once on the enclosing response and applies to every pair in it.
    String checkedAt = resp.getCheckedAt().getToken();

    var results = new ArrayList<CheckResult>(resp.getPairsCount());
    for (int i = 0; i < resp.getPairsCount(); i++) {
      CheckBulkPermissionsPair pair = resp.getPairs(i);
      if (pair.hasError()) {
        // Route the per-item error through ErrorMapper (by way of a synthesized
        // StatusRuntimeException carrying the item's own code) so callers get the SPECIFIC typed
        // exception (e.g. PermissionDeniedException) instead of the untyped base SpiceDBException
        // — the item's code was previously discarded here. The item index is preserved in the
        // message, matching spicedb-go's `fmt.Sprintf("check item %d", i)`.
        build.buf.gen.google.rpc.Status errorStatus = pair.getError();
        StatusRuntimeException sre =
            Status.fromCodeValue(errorStatus.getCode())
                .withDescription("check item " + i + ": " + errorStatus.getMessage())
                .asRuntimeException();
        throw ErrorMapper.toSpiceDBException(sre);
      }
      results.add(checkResultFromBulkItem(pair.getItem(), checkedAt));
    }
    return results;
  }

  /**
   * Returns true if any of the given relationships have the permission unconditionally. A {@code
   * CONDITIONAL_PERMISSION} result does not count as granted — only {@link
   * CheckResult#hasPermission()} results are considered (RULE, clause 3).
   */
  public boolean checkAny(
      Consistency consistency, String permission, Relationship... relationships) {
    return checkAny(consistency, permission, null, relationships);
  }

  /**
   * Returns true if any of the given relationships have the permission unconditionally, evaluating
   * caveats with the given call-level/per-item CHECK-TIME {@code context} (see {@link
   * #checkPermissions(Consistency, String, Map, Relationship...)} for the merge rule). A {@code
   * CONDITIONAL_PERMISSION} result does not count as granted — only {@link
   * CheckResult#hasPermission()} results are considered (RULE, clause 3).
   */
  public boolean checkAny(
      Consistency consistency,
      String permission,
      Map<String, Object> context,
      Relationship... relationships) {
    List<CheckResult> results = checkPermissions(consistency, permission, context, relationships);
    for (CheckResult r : results) {
      if (r.hasPermission()) return true;
    }
    return false;
  }

  /**
   * Returns true if all of the given relationships have the permission unconditionally. A {@code
   * CONDITIONAL_PERMISSION} result does not count as granted — every result must be {@link
   * CheckResult#hasPermission()} for this to return true (RULE, clause 3).
   */
  public boolean checkAll(
      Consistency consistency, String permission, Relationship... relationships) {
    return checkAll(consistency, permission, null, relationships);
  }

  /**
   * Returns true if all of the given relationships have the permission unconditionally, evaluating
   * caveats with the given call-level/per-item CHECK-TIME {@code context} (see {@link
   * #checkPermissions(Consistency, String, Map, Relationship...)} for the merge rule). A {@code
   * CONDITIONAL_PERMISSION} result does not count as granted — every result must be {@link
   * CheckResult#hasPermission()} for this to return true (RULE, clause 3).
   */
  public boolean checkAll(
      Consistency consistency,
      String permission,
      Map<String, Object> context,
      Relationship... relationships) {
    List<CheckResult> results = checkPermissions(consistency, permission, context, relationships);
    for (CheckResult r : results) {
      if (!r.hasPermission()) return false;
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
  // Delete Relationships — auto-paging 1,000-item batches
  // -----------------------------------------------------------------------

  /**
   * Optional preconditions and page-size override for {@link #deleteRelationships(Filter,
   * DeleteOptions)}.
   *
   * <p>Immutable — {@code withMustMatch}/{@code withMustNotMatch}/{@code withLimit} each return a
   * new instance, mirroring {@link Filter}'s builder style. Start from {@link #none()}, which is
   * exactly the behavior of the single-argument {@link #deleteRelationships(Filter)} overload.
   *
   * <p>Preconditions are a per-request proto field, so when a delete spans multiple pages (i.e.
   * more matches than the page size), they are re-evaluated by the server on every page — there is
   * no "check-once, apply-to-all-pages" semantics. This means a delete that starts successfully can
   * still fail partway through if the guarded state changes between pages, after earlier pages have
   * already been deleted. For a single-shot, all-or-nothing guarded delete, pair the precondition
   * with a {@link #withLimit} large enough to cover every matching relationship in one call.
   * Mirrors {@code spicedb-go}'s {@code WithDeleteMustMatch}/{@code WithDeleteMustNotMatch}/{@code
   * WithDeleteLimit} (client/relationships.go).
   *
   * <pre>{@code
   * var options = SpiceDBClient.DeleteOptions.none()
   *     .withMustMatch(existsFilter)
   *     .withLimit(500);
   * client.deleteRelationships(filter, options);
   * }</pre>
   */
  public record DeleteOptions(List<Filter> mustMatch, List<Filter> mustNotMatch, Integer limit) {

    public DeleteOptions {
      mustMatch = mustMatch == null ? List.of() : List.copyOf(mustMatch);
      mustNotMatch = mustNotMatch == null ? List.of() : List.copyOf(mustNotMatch);
      if (limit != null && limit <= 0) {
        throw new IllegalArgumentException("limit must be positive");
      }
    }

    /**
     * No preconditions, default page size (1,000) — identical behavior to {@link
     * #deleteRelationships(Filter)}.
     */
    public static DeleteOptions none() {
      return new DeleteOptions(List.of(), List.of(), null);
    }

    /**
     * Adds a MUST_MATCH precondition: the server rejects the delete (and deletes nothing) unless at
     * least one relationship matching {@code filter} exists at evaluation time. Multiple calls
     * accumulate; all are sent with every page of the delete.
     */
    public DeleteOptions withMustMatch(Filter filter) {
      var updated = new ArrayList<>(mustMatch);
      updated.add(filter);
      return new DeleteOptions(updated, mustNotMatch, limit);
    }

    /**
     * Adds a MUST_NOT_MATCH precondition: the server rejects the delete (and deletes nothing) if
     * any relationship matching {@code filter} exists at evaluation time. Multiple calls
     * accumulate; all are sent with every page of the delete.
     */
    public DeleteOptions withMustNotMatch(Filter filter) {
      var updated = new ArrayList<>(mustNotMatch);
      updated.add(filter);
      return new DeleteOptions(mustMatch, updated, limit);
    }

    /** Overrides the per-request page size used by the auto-paging delete loop (default 1,000). */
    public DeleteOptions withLimit(int limit) {
      return new DeleteOptions(mustMatch, mustNotMatch, limit);
    }
  }

  /**
   * Deletes all relationships matching the given filter, guarded by optional preconditions and with
   * an optional page-size override supplied via {@code options}. Returns the revision of the final
   * deletion. See {@link DeleteOptions} for precondition/paging semantics.
   */
  public String deleteRelationships(Filter filter, DeleteOptions options) {
    var preconditions = new ArrayList<Precondition>();
    for (Filter f : options.mustMatch()) {
      preconditions.add(
          toPrecondition(
              new Transaction.Precondition(Transaction.PreconditionOperation.MUST_MATCH, f)));
    }
    for (Filter f : options.mustNotMatch()) {
      preconditions.add(
          toPrecondition(
              new Transaction.Precondition(Transaction.PreconditionOperation.MUST_NOT_MATCH, f)));
    }
    int pageSize = options.limit() != null ? options.limit() : DEFAULT_DELETE_PAGE_SIZE;

    String revision = "";
    while (true) {
      DeleteRelationshipsResponse resp =
          withRetry(
              () ->
                  permissionsStub.deleteRelationships(
                      DeleteRelationshipsRequest.newBuilder()
                          .setRelationshipFilter(toRelationshipFilter(filter))
                          .addAllOptionalPreconditions(preconditions)
                          .setOptionalLimit(pageSize)
                          .setOptionalAllowPartialDeletions(true)
                          .build()));
      revision = resp.getDeletedAt().getToken();
      if (resp.getDeletionProgress()
          == DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_COMPLETE) {
        return revision;
      }
    }
  }

  /**
   * Deletes all relationships matching the given filter. Large result sets are automatically paged
   * in batches of 1,000. Returns the revision of the final deletion.
   */
  public String deleteRelationships(Filter filter) {
    return deleteRelationships(filter, DeleteOptions.none());
  }

  // -----------------------------------------------------------------------
  // Lookups — cursor-based auto-pagination (512-item pages)
  // -----------------------------------------------------------------------

  /**
   * Returns a stream over resources of the given type that the subject has the specified permission
   * on. Each result carries the permissionship (full grant vs conditional on caveat context) and,
   * for conditional results, which caveat context was missing. Cursors are handled transparently.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<LookupResult.LookupResource> lookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      String subjectType,
      String subjectID) {
    Context.CancellableContext cancelCtx = Context.current().withCancellation();
    Iterator<LookupResult.LookupResource> iterator =
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
          public LookupResult.LookupResource next() {
            if (!hasNext()) throw new NoSuchElementException();
            LookupResourcesResponse resp = currentPage.next();
            pageCount++;
            cursor = resp.getAfterResultCursor();
            return lookupResourceFromProto(resp);
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
            Iterator<LookupResourcesResponse> serverStream;
            Context previous = cancelCtx.attach();
            try {
              serverStream =
                  openStreamWithRetry(() -> permissionsStub.lookupResources(reqBuilder.build()));
            } finally {
              cancelCtx.detach(previous);
            }
            // The first hasNext() is already primed by openStreamWithRetry (retried above); every
            // poll from here on is mapped-but-not-retried, since an item may already have been
            // appended to `responses` by the time a later one fails.
            while (mapStreamErrors(serverStream::hasNext)) {
              responses.add(mapStreamErrors(serverStream::next));
            }

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

    return cancelOnClose(
        StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false),
        cancelCtx);
  }

  /**
   * Returns a stream over subjects of the given type that have the specified permission on the
   * resource. Unlike lookupResources, this does not use cursor-based pagination (not supported in
   * SpiceDB yet) and streams all results in a single call.
   *
   * <p>When a yielded {@link LookupResult.LookupSubject#subject} is the wildcard {@code "*"}, the
   * server has granted the permission to every subject of the requested subject type EXCEPT those
   * listed in {@link LookupResult.LookupSubject#excludedSubjects}. Callers MUST check {@code
   * excludedSubjects} before treating a wildcard match as a blanket grant, or they risk granting
   * access to subjects the server explicitly excluded.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<LookupResult.LookupSubject> lookupSubjects(
      Consistency consistency,
      String resourceType,
      String resourceID,
      String permission,
      String subjectType) {
    Iterator<LookupSubjectsResponse> serverStream =
        openStreamWithRetry(
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
    // The first hasNext() is already primed by openStreamWithRetry (retried above); every poll
    // from here on is mapped-but-not-retried, since an item may already have been added to
    // `responses` by the time a later one fails.
    var responses = new ArrayList<LookupSubjectsResponse>();
    while (mapStreamErrors(serverStream::hasNext)) {
      responses.add(mapStreamErrors(serverStream::next));
    }

    return responses.stream().map(SpiceDBClient::lookupSubjectFromProto);
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
    Context.CancellableContext cancelCtx = Context.current().withCancellation();
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

            Iterator<ExportBulkRelationshipsResponse> serverStream;
            Context previous = cancelCtx.attach();
            try {
              serverStream =
                  openStreamWithRetry(
                      () -> permissionsStub.exportBulkRelationships(reqBuilder.build()));
            } finally {
              cancelCtx.detach(previous);
            }

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

    return cancelOnClose(
        StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false),
        cancelCtx);
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

    Context.CancellableContext cancelCtx = Context.current().withCancellation();

    Iterator<Update> iterator =
        new Iterator<>() {
          private final Queue<Update> buffer = new ArrayDeque<>();
          // Opened lazily, on the first hasNext() call below — not eagerly here in updates()
          // itself. This matters for correctness, not just style: grpc-java's blocking-stub
          // priming call (which openStreamWithRetry needs, to make retry effective — see its
          // Javadoc) blocks until the first message OR the call terminates. For watch, "the first
          // message" can be arbitrarily far in the future (it's an indefinite live feed), so
          // priming eagerly would turn updates() into a call that can hang for however long
          // there's no relationship change — a real behavioral regression for a method whose
          // whole contract is "return a stream promptly, block only when the caller pulls from
          // it". Deferring the open (and its retry) to the caller's first pull preserves that
          // contract while still making establishment retry effective once the caller does pull.
          private Iterator<WatchResponse> serverStream;

          @Override
          public boolean hasNext() {
            if (!buffer.isEmpty()) return true;

            if (serverStream == null) {
              // Establishment retry: applies ONLY to this first open. Once serverStream is set,
              // every later call skips straight to mapStreamErrors below (no retry) — so a
              // transient error after the watch has connected (mid-watch) is mapped and rethrown,
              // never retried, since retrying would replay/duplicate already-delivered updates.
              Context previous = cancelCtx.attach();
              try {
                serverStream = openStreamWithRetry(() -> watchStub.watch(reqBuilder.build()));
              } finally {
                cancelCtx.detach(previous);
              }
            }

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

    return cancelOnClose(
        StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false),
        cancelCtx);
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

  /**
   * Registers an {@code onClose} handler that cancels {@code cancelCtx} (and, transitively, any
   * gRPC call bound to it) when the returned stream is closed. Used by the lazy streaming methods
   * to make {@code close()} actually cancel the underlying server-streaming call, rather than
   * leaving it open server-side.
   */
  private static <T> Stream<T> cancelOnClose(
      Stream<T> stream, Context.CancellableContext cancelCtx) {
    return stream.onClose(
        () ->
            cancelCtx.cancel(
                Status.CANCELLED.withDescription("stream closed by caller").asRuntimeException()));
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

  /**
   * Opens a server-streaming RPC and makes stream/page ESTABLISHMENT effectively retryable on
   * transient errors, reusing {@link #withRetry}'s transient predicate, {@code MAX_RETRIES}, and
   * backoff verbatim (no divergent retry policy).
   *
   * <p>For grpc-java's blocking stub, {@code stub.someStreamingMethod(request)} never throws — it
   * only enqueues the call and returns an {@link Iterator}; the RPC's actual outcome (including a
   * transient {@code UNAVAILABLE}/{@code RESOURCE_EXHAUSTED}/{@code ABORTED}) only surfaces on the
   * iterator's first {@code hasNext()}/{@code next()} call. Wrapping only the stub call in {@link
   * #withRetry} (the old code) therefore never actually retries anything — the exception always
   * escapes on the caller's first poll, past the retry loop. This method fixes that by folding the
   * priming {@code hasNext()} call INTO the retried unit of work: if it throws a transient error,
   * {@link #withRetry} re-issues {@code openCall} (a fresh RPC, e.g. from the same page cursor)
   * after backoff, exactly as it would for a unary call.
   *
   * <p><b>No-replay guarantee:</b> this method must only ever be used to open a stream/page BEFORE
   * any item has been handed to the caller. Once it returns, the returned iterator is primed (its
   * first {@code hasNext()} result is already cached), and every subsequent poll MUST go through
   * {@link #mapStreamErrors} — not this method — since re-opening after an item has already been
   * yielded would replay/duplicate it.
   */
  private <T> Iterator<T> openStreamWithRetry(RetryableCall<Iterator<T>> openCall) {
    return withRetry(
        () -> {
          Iterator<T> serverStream = openCall.call();
          // Force establishment now, inside the retried unit of work, instead of leaving it to
          // whatever unretried call the caller makes next.
          serverStream.hasNext();
          return serverStream;
        });
  }

  private Stream<Relationship> paginatedRelationshipStream(
      Consistency consistency, Filter filter, int pageSize) {
    Context.CancellableContext cancelCtx = Context.current().withCancellation();
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

            Iterator<ReadRelationshipsResponse> serverStream;
            Context previous = cancelCtx.attach();
            try {
              serverStream =
                  openStreamWithRetry(() -> permissionsStub.readRelationships(reqBuilder.build()));
            } finally {
              cancelCtx.detach(previous);
            }

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

    return cancelOnClose(
        StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false),
        cancelCtx);
  }

  private static CheckBulkPermissionsRequestItem checkItemFromRel(
      Relationship r, String permission, Map<String, Object> callLevelContext) {
    var builder =
        CheckBulkPermissionsRequestItem.newBuilder()
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
                    .build());

    // CHECK-TIME context only -- r.caveatContext() (write-time) is never read here. See
    // mergeCheckContext for the key-level, item-wins merge rule.
    Map<String, Object> merged = mergeCheckContext(callLevelContext, r.checkContext());
    if (merged != null) {
      builder.setContext(toProtoStruct(merged));
    }
    return builder.build();
  }

  /**
   * Merges call-level and per-item CHECK-TIME caveat context using a key-level merge where the
   * item's keys win: {@code {...callLevel, ...item}}. Call-level keys absent from {@code item} are
   * retained -- this is NOT wholesale replacement, since an item supplying one key must not
   * silently drop every call-level key (the caveat would then fail to evaluate for the dropped
   * keys, landing the caller back in the confusing CONDITIONAL_PERMISSION state this contract
   * exists to make legible). Returns {@code null} (never an empty map) when both inputs are
   * null/empty, so the caller knows to omit the wire {@code context} field entirely rather than
   * sending an empty {@code Struct}.
   */
  private static Map<String, Object> mergeCheckContext(
      Map<String, Object> callLevel, Map<String, Object> item) {
    boolean callEmpty = callLevel == null || callLevel.isEmpty();
    boolean itemEmpty = item == null || item.isEmpty();
    if (callEmpty && itemEmpty) {
      return null;
    }
    var merged = new LinkedHashMap<String, Object>();
    if (callLevel != null) {
      merged.putAll(callLevel);
    }
    if (item != null) {
      merged.putAll(item);
    }
    return merged;
  }

  /**
   * Converts a check-time context map to a proto {@code Struct}, reusing {@link
   * #checkContextToProtoValue}.
   */
  private static com.google.protobuf.Struct toProtoStruct(Map<String, Object> context) {
    var builder = com.google.protobuf.Struct.newBuilder();
    for (var entry : context.entrySet()) {
      builder.putFields(entry.getKey(), checkContextToProtoValue(entry.getValue()));
    }
    return builder.build();
  }

  /**
   * Converts a native Java value into a protobuf {@code Value} for CHECK-TIME caveat context.
   * Unlike {@link #toProtoValue} (used by the write-time relationship caveat-context path, which
   * intentionally stringifies anything it doesn't recognize, including nested {@link Map}/{@link
   * List} values), this recurses into nested maps and lists so a caveat expecting a nested object
   * or list receives a proper {@code Struct}/{@code ListValue} instead of a string the caveat
   * evaluator can't use. Do not reuse this for the write-time path -- write-time stringification is
   * intentional there and out of scope for this conversion.
   */
  private static com.google.protobuf.Value checkContextToProtoValue(Object value) {
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
    } else if (value instanceof Map<?, ?> m) {
      var structBuilder = com.google.protobuf.Struct.newBuilder();
      for (var entry : m.entrySet()) {
        structBuilder.putFields(
            String.valueOf(entry.getKey()), checkContextToProtoValue(entry.getValue()));
      }
      return com.google.protobuf.Value.newBuilder().setStructValue(structBuilder.build()).build();
    } else if (value instanceof List<?> l) {
      var listBuilder = com.google.protobuf.ListValue.newBuilder();
      for (var item : l) {
        listBuilder.addValues(checkContextToProtoValue(item));
      }
      return com.google.protobuf.Value.newBuilder().setListValue(listBuilder.build()).build();
    } else {
      return com.google.protobuf.Value.newBuilder().setStringValue(value.toString()).build();
    }
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
        expiration,
        // checkContext is CHECK-TIME only and never round-trips through a write/read — a
        // relationship read back from the server never carries it.
        null);
  }

  /**
   * Maps the proto {@code LookupPermissionship} enum to its native equivalent. Unrecognized values
   * map to {@code UNSPECIFIED}.
   */
  private static LookupResult.Permissionship permissionshipFromProto(LookupPermissionship v) {
    return switch (v) {
      case LOOKUP_PERMISSIONSHIP_HAS_PERMISSION -> LookupResult.Permissionship.HAS_PERMISSION;
      case LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION ->
          LookupResult.Permissionship.CONDITIONAL_PERMISSION;
      default -> LookupResult.Permissionship.UNSPECIFIED;
    };
  }

  /** Maps a proto {@code PartialCaveatInfo} to its native equivalent. A null input maps to null. */
  private static LookupResult.PartialCaveatInfo partialCaveatFromProto(
      build.buf.gen.authzed.api.v1.PartialCaveatInfo v) {
    if (v == null) {
      return null;
    }
    return new LookupResult.PartialCaveatInfo(List.copyOf(v.getMissingRequiredContextList()));
  }

  /**
   * Maps the proto {@code CheckPermissionResponse.Permissionship} enum to its native equivalent.
   * Unlike {@code LookupPermissionship} (mapped by {@link #permissionshipFromProto}), this enum has
   * a {@code NO_PERMISSION} value — the check surface answers a yes/no/conditional question about
   * one specific pair, so "no" is itself an answer. Unrecognized values map to {@code UNSPECIFIED}.
   */
  private static LookupResult.Permissionship checkPermissionshipFromProto(
      CheckPermissionResponse.Permissionship v) {
    return switch (v) {
      case PERMISSIONSHIP_HAS_PERMISSION -> LookupResult.Permissionship.HAS_PERMISSION;
      case PERMISSIONSHIP_CONDITIONAL_PERMISSION ->
          LookupResult.Permissionship.CONDITIONAL_PERMISSION;
      case PERMISSIONSHIP_NO_PERMISSION -> LookupResult.Permissionship.NO_PERMISSION;
      default -> LookupResult.Permissionship.UNSPECIFIED;
    };
  }

  /**
   * Maps a proto {@code CheckBulkPermissionsResponseItem} (one pair's successful result from a
   * CheckBulkPermissions call) to a native {@link CheckResult}. {@code
   * CheckBulkPermissionsResponseItem} carries no per-item {@code checked_at} of its own — the token
   * lives once on the enclosing {@code CheckBulkPermissionsResponse} and applies to every pair in
   * it, so callers pass it in as {@code responseCheckedAt} to propagate onto each item.
   */
  private static CheckResult checkResultFromBulkItem(
      CheckBulkPermissionsResponseItem item, String responseCheckedAt) {
    return new CheckResult(
        checkPermissionshipFromProto(item.getPermissionship()),
        item.hasPartialCaveatInfo()
            ? item.getPartialCaveatInfo().getMissingRequiredContextList()
            : List.of(),
        responseCheckedAt);
  }

  /**
   * Maps a proto {@code LookupResourcesResponse} to a native {@link LookupResult.LookupResource}.
   */
  private static LookupResult.LookupResource lookupResourceFromProto(LookupResourcesResponse resp) {
    return new LookupResult.LookupResource(
        resp.getResourceObjectId(),
        permissionshipFromProto(resp.getPermissionship()),
        partialCaveatFromProto(resp.hasPartialCaveatInfo() ? resp.getPartialCaveatInfo() : null),
        resp.getLookedUpAt().getToken());
  }

  /**
   * Maps a proto {@code ResolvedSubject} to its native equivalent. A null input maps to a
   * zero-value {@link LookupResult.ResolvedSubject} (empty {@code subjectId}), which callers use as
   * the trigger for falling back to deprecated response-level fields.
   */
  private static LookupResult.ResolvedSubject resolvedSubjectFromProto(
      build.buf.gen.authzed.api.v1.ResolvedSubject v) {
    if (v == null) {
      return new LookupResult.ResolvedSubject("", LookupResult.Permissionship.UNSPECIFIED, null);
    }
    return new LookupResult.ResolvedSubject(
        v.getSubjectObjectId(),
        permissionshipFromProto(v.getPermissionship()),
        partialCaveatFromProto(v.hasPartialCaveatInfo() ? v.getPartialCaveatInfo() : null));
  }

  /**
   * Maps a proto {@code LookupSubjectsResponse} to a native {@link LookupResult.LookupSubject},
   * falling back to the deprecated {@code subject_object_id}/{@code permissionship}/{@code
   * partial_caveat_info} fields when {@code subject} isn't populated (older servers), and to the
   * deprecated {@code excluded_subject_ids} (IDs only, no permissionship/caveat info) when {@code
   * excluded_subjects} isn't populated. Mirrors {@code spicedb-go}'s {@code lookup.go}.
   */
  @SuppressWarnings("deprecation") // intentional fallback to deprecated fields, see below
  private static LookupResult.LookupSubject lookupSubjectFromProto(LookupSubjectsResponse resp) {
    LookupResult.ResolvedSubject subject =
        resp.hasSubject() ? resolvedSubjectFromProto(resp.getSubject()) : null;
    if (subject == null || subject.subjectId().isEmpty()) {
      // Fall back to the deprecated top-level fields for servers that don't yet populate the
      // non-deprecated `subject` field.
      subject =
          new LookupResult.ResolvedSubject(
              resp.getSubjectObjectId(),
              permissionshipFromProto(resp.getPermissionship()),
              partialCaveatFromProto(
                  resp.hasPartialCaveatInfo() ? resp.getPartialCaveatInfo() : null));
    }

    List<LookupResult.ResolvedSubject> excluded;
    if (!resp.getExcludedSubjectsList().isEmpty()) {
      excluded =
          resp.getExcludedSubjectsList().stream()
              .map(SpiceDBClient::resolvedSubjectFromProto)
              .toList();
    } else if (!resp.getExcludedSubjectIdsList().isEmpty()) {
      // Fall back to the deprecated excluded_subject_ids field, which carries only IDs (no
      // permissionship/caveat info).
      excluded =
          resp.getExcludedSubjectIdsList().stream()
              .map(
                  id ->
                      new LookupResult.ResolvedSubject(
                          id, LookupResult.Permissionship.UNSPECIFIED, null))
              .toList();
    } else {
      excluded = List.of();
    }

    return new LookupResult.LookupSubject(subject, excluded, resp.getLookedUpAt().getToken());
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
