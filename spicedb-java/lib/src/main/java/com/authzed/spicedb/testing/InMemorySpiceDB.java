package com.authzed.spicedb.testing;

import com.authzed.spicedb.BulkItemErrors;
import com.authzed.spicedb.CallOptions;
import com.authzed.spicedb.CheckOptions;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.DeleteOptions;
import com.authzed.spicedb.ExportOptions;
import com.authzed.spicedb.ExportedRelationship;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.ImportOptions;
import com.authzed.spicedb.ObjectRef;
import com.authzed.spicedb.PartialDeletion;
import com.authzed.spicedb.PermissionCheck;
import com.authzed.spicedb.PermissionsApi;
import com.authzed.spicedb.Permissionship;
import com.authzed.spicedb.ReadOptions;
import com.authzed.spicedb.ReadRelationship;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.ResponseMetadata;
import com.authzed.spicedb.RetryPolicy;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.SubjectRef;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.TransportOptions;
import com.authzed.spicedb.WriteOptions;
import com.authzed.spicedb.WriteResult;
import com.authzed.spicedb.ZedToken;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.Metadata;
import io.grpc.Status;
import java.time.Clock;
import java.util.ArrayList;
import java.util.Collection;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Predicate;
import java.util.stream.Stream;

/**
 * An in-memory test double for {@link PermissionsApi}: the relationship, import/export, schema and
 * check operations of {@link SpiceDBClient}, with the same signatures, so code written against
 * {@code PermissionsApi} accepts either.
 *
 * <ul>
 *   <li>Writes apply atomically, evaluate preconditions with {@link Filter#matches} (server filter
 *       semantics), fail {@code CREATE} of an existing relationship with {@code ALREADY_EXISTS},
 *       and reject a transaction that updates the same relationship twice with {@code
 *       INVALID_ARGUMENT}. Failed preconditions raise {@code FAILED_PRECONDITION}. An empty
 *       transaction checks its preconditions and returns the current revision, as SpiceDB does.
 *   <li>Reads, exports and deletes use the same filter semantics; expired relationships are
 *       invisible, as on the server. Read and export items carry an opaque cursor that the options'
 *       {@code cursor} resumes from, and limits are honored; read items carry the current revision
 *       as read-at.
 *   <li>Imports add every relationship in one transaction: any that already exists (or repeats)
 *       fails the whole import with {@code ALREADY_EXISTS}.
 *   <li>{@link #writeSchema} stores the text without compiling it; {@link #readSchema} returns it,
 *       or {@code NOT_FOUND} before any write. {@link #reflectSchema} returns the result registered
 *       with {@link #setReflectSchemaResult}.
 *   <li>Checks return registered results ({@link #setCheckResult}, {@link #setCheckError});
 *       unregistered checks are {@code NO_PERMISSION}. The double does not evaluate schema.
 *   <li>Every operation reports the metadata registered with {@link #setResponseMetadata} to the
 *       call's {@link TransportOptions#responseMetadata()} capture.
 * </ul>
 *
 * <p>Errors have the real client's codes and message shape: server-side failures are built with
 * {@link ErrorMapper#newError} (so {@code serverMessage()} is SpiceDB's message), and local
 * validation raises {@code InvalidArgumentException} without a server message, as the client does.
 * Options are plain values: those that change results (cursors, limits, partial deletion,
 * preconditions, metadata capture, a per-call retry opt-in on a non-idempotent mutation) are
 * honored, and the rest are accepted and ignored. Consistency arguments are accepted and ignored:
 * the double is always fully consistent. Thread-safe.
 */
public final class InMemorySpiceDB implements PermissionsApi {
  /** SpiceDB's default limit for {@code DeleteRelationships} without one. */
  public static final int DEFAULT_DELETE_LIMIT = 1_000;

  private final Map<String, Relationship> relationships = new LinkedHashMap<>();
  private final Map<String, CheckResult> checkResults = new HashMap<>();
  private final Clock clock;
  private long revision;
  private String schema;
  private SpiceDBClient.ReflectSchemaResult reflectResult;
  private Metadata headers = new Metadata();
  private Metadata trailers = new Metadata();

  public InMemorySpiceDB() {
    this(Clock.systemUTC());
  }

  /** Uses {@code clock} to decide whether a relationship has expired. */
  public InMemorySpiceDB(Clock clock) {
    this.clock = clock;
  }

  private static String key(Relationship r) {
    return r.resource() + "#" + r.relation() + "@" + r.subject();
  }

  private static String checkKey(ObjectRef resource, String permission, SubjectRef subject) {
    return resource + "#" + permission + "@" + subject;
  }

  private ZedToken token() {
    return new ZedToken("inmemory-" + revision);
  }

  private boolean live(Relationship r) {
    return r.expiration() == null || r.expiration().isAfter(clock.instant());
  }

  private List<Relationship> matching(Filter filter) {
    var out = new ArrayList<Relationship>();
    for (var r : relationships.values()) if (live(r) && filter.matches(r)) out.add(r);
    return out;
  }

  private void checkPreconditions(List<Transaction.Precondition> preconditions) {
    for (var p : preconditions) {
      boolean any = !matching(p.filter()).isEmpty();
      if (p.operation() == Transaction.PreconditionOperation.MUST_MATCH && !any)
        throw ErrorMapper.newError(
            Status.Code.FAILED_PRECONDITION, "unable to satisfy write precondition `" + p + "`");
      if (p.operation() == Transaction.PreconditionOperation.MUST_NOT_MATCH && any)
        throw ErrorMapper.newError(
            Status.Code.FAILED_PRECONDITION, "unable to satisfy write precondition `" + p + "`");
    }
  }

  /** Rejects a per-call retry opt-in on a mutation that is not idempotent, as the client does. */
  private static void rejectRetry(RetryPolicy policy, String operation) {
    if (policy != null && policy.maxRetries() > 0)
      throw new InvalidArgumentException(
          operation
              + " cannot be retried: only touch-only writes without preconditions and schema"
              + " writes are idempotent on replay");
  }

  /** Fills the call's metadata capture with the registered headers and trailers. */
  private void respond(TransportOptions transport) {
    ResponseMetadata capture = transport.responseMetadata();
    if (capture == null) return;
    var h = new Metadata();
    h.merge(headers);
    var t = new Metadata();
    t.merge(trailers);
    capture.recordHeaders(h);
    capture.recordTrailers(t);
  }

  /** The 0-based start position encoded in a cursor this double issued. */
  private static int cursorStart(String cursor) {
    if (cursor == null || cursor.isEmpty()) return 0;
    try {
      int n = Integer.parseInt(cursor.substring("inmemory-cursor-".length()));
      if (n < 0) throw new NumberFormatException();
      return n;
    } catch (RuntimeException e) {
      throw ErrorMapper.newError(Status.Code.INVALID_ARGUMENT, "invalid cursor");
    }
  }

  /** Sets the headers and trailers every operation reports to its metadata capture. */
  public synchronized void setResponseMetadata(Metadata headers, Metadata trailers) {
    this.headers = new Metadata();
    if (headers != null) this.headers.merge(headers);
    this.trailers = new Metadata();
    if (trailers != null) this.trailers.merge(trailers);
  }

  /** Registers the result {@link #reflectSchema} returns (stamped with the current revision). */
  public synchronized void setReflectSchemaResult(SpiceDBClient.ReflectSchemaResult result) {
    reflectResult = result;
  }

  /** A snapshot of every stored, unexpired relationship, in write order. */
  public synchronized List<Relationship> relationships() {
    return relationships.values().stream().filter(this::live).toList();
  }

  /** Registers the result {@link #check} returns for this resource, permission and subject. */
  public synchronized void setCheckResult(
      ObjectRef resource, String permission, SubjectRef subject, Permissionship result) {
    checkResults.put(
        checkKey(resource, permission, subject),
        new CheckResult(result, List.of(), null, null, null));
  }

  /** Registers an error {@link #check} throws (or a bulk check reports) for this triple. */
  public synchronized void setCheckError(
      ObjectRef resource, String permission, SubjectRef subject, SpiceDBException error) {
    checkResults.put(checkKey(resource, permission, subject), CheckResult.ofError(error, null));
  }

  @Override
  public WriteResult write(Transaction txn) {
    return write(txn, WriteOptions.builder().build());
  }

  @Override
  public synchronized WriteResult write(Transaction txn, WriteOptions options) {
    if (txn == null || options == null)
      throw new InvalidArgumentException("transaction and options must not be null");
    boolean touchOnly =
        txn.preconditions().isEmpty()
            && txn.mutations().stream().allMatch(m -> m.operation() == Transaction.Operation.TOUCH);
    if (!touchOnly) rejectRetry(options.retryPolicy(), "write");
    respond(options.transport());
    checkPreconditions(txn.preconditions());
    if (txn.isEmpty()) return new WriteResult(token());
    var next = new LinkedHashMap<>(relationships);
    var seen = new HashMap<String, Boolean>();
    for (var m : txn.mutations()) {
      String k = key(m.relationship());
      if (seen.put(k, true) != null)
        throw ErrorMapper.newError(
            Status.Code.INVALID_ARGUMENT,
            "found more than one update with relationship `" + k + "` in this request");
      switch (m.operation()) {
        case CREATE -> {
          var existing = next.get(k);
          if (existing != null && live(existing))
            throw ErrorMapper.newError(
                Status.Code.ALREADY_EXISTS,
                "could not CREATE relationship `" + k + "`, as it already existed");
          next.put(k, m.relationship());
        }
        case TOUCH -> next.put(k, m.relationship());
        case DELETE -> next.remove(k);
      }
    }
    relationships.clear();
    relationships.putAll(next);
    revision++;
    return new WriteResult(token());
  }

  @Override
  public Stream<ReadRelationship> readRelationships(Consistency consistency, Filter filter) {
    return readRelationships(consistency, filter, ReadOptions.builder().build());
  }

  @Override
  public synchronized Stream<ReadRelationship> readRelationships(
      Consistency consistency, Filter filter, ReadOptions options) {
    if (consistency == null || filter == null || options == null)
      throw new InvalidArgumentException("consistency, filter and options must not be null");
    var matches = matching(filter);
    int start = cursorStart(options.cursor());
    respond(options.transport());
    var out = new ArrayList<ReadRelationship>();
    for (int i = start; i < matches.size(); i++) {
      if (options.limit() > 0 && out.size() >= options.limit()) break;
      out.add(new ReadRelationship(matches.get(i), "inmemory-cursor-" + (i + 1), token()));
    }
    return out.stream();
  }

  @Override
  public SpiceDBClient.DeleteResult deleteRelationships(Filter filter, int limit) {
    return deleteRelationships(filter, limit, DeleteOptions.builder().build());
  }

  @Override
  public SpiceDBClient.DeleteResult deleteRelationships(
      Filter filter, int limit, DeleteOptions options) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    rejectRetry(options.retryPolicy(), "delete relationships");
    if (limit <= 0) throw new InvalidArgumentException("limit must be positive");
    return delete(filter, limit, options);
  }

  @Override
  public SpiceDBClient.DeleteResult deleteRelationships(Filter filter, DeleteOptions options) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    rejectRetry(options.retryPolicy(), "delete relationships");
    if (options.partialDeletion() != PartialDeletion.DISALLOWED)
      throw new InvalidArgumentException(
          "deleteRelationships without a limit requires PartialDeletion.DISALLOWED");
    return delete(filter, DEFAULT_DELETE_LIMIT, options);
  }

  private synchronized SpiceDBClient.DeleteResult delete(
      Filter filter, int limit, DeleteOptions options) {
    if (filter == null || options == null)
      throw new InvalidArgumentException("filter and options must not be null");
    respond(options.transport());
    var preconditions = new ArrayList<Transaction.Precondition>();
    for (var f : options.mustMatch())
      preconditions.add(
          new Transaction.Precondition(Transaction.PreconditionOperation.MUST_MATCH, f));
    for (var f : options.mustNotMatch())
      preconditions.add(
          new Transaction.Precondition(Transaction.PreconditionOperation.MUST_NOT_MATCH, f));
    checkPreconditions(preconditions);
    var matches = matching(filter);
    if (options.partialDeletion() == PartialDeletion.DISALLOWED && matches.size() > limit)
      throw ErrorMapper.newError(
          Status.Code.INVALID_ARGUMENT,
          "found more than "
              + limit
              + " relationships to be deleted and partial deletion was not requested");
    int n = Math.min(limit, matches.size());
    for (int i = 0; i < n; i++) relationships.remove(key(matches.get(i)));
    revision++;
    return new SpiceDBClient.DeleteResult(token(), n, n == matches.size(), null);
  }

  @Override
  public CheckResult check(PermissionCheck request) {
    return check(request, CheckOptions.builder().build());
  }

  @Override
  public synchronized CheckResult check(PermissionCheck request, CheckOptions options) {
    if (request == null || options == null)
      throw new InvalidArgumentException("request and options must not be null");
    respond(options.transport());
    var r = lookup(request);
    if (r.error() != null) throw r.error();
    return r;
  }

  private CheckResult lookup(PermissionCheck request) {
    var r = checkResults.get(checkKey(request.resource(), request.permission(), request.subject()));
    if (r == null)
      return new CheckResult(Permissionship.NO_PERMISSION, List.of(), token(), null, null);
    return r.error() != null
        ? CheckResult.ofError(r.error(), token())
        : new CheckResult(r.permissionship(), r.missingContext(), token(), null, null);
  }

  @Override
  public List<CheckResult> checkBulk(Collection<PermissionCheck> requests) {
    return checkBulk(requests, CheckOptions.builder().build());
  }

  @Override
  public synchronized List<CheckResult> checkBulk(
      Collection<PermissionCheck> requests, CheckOptions options) {
    if (requests == null || options == null)
      throw new InvalidArgumentException("requests and options must not be null");
    respond(options.transport());
    var out = new ArrayList<CheckResult>();
    int i = 0;
    for (var request : requests) {
      var r = lookup(request);
      if (r.error() != null && options.bulkItemErrors() == BulkItemErrors.FAIL_CALL)
        throw ErrorMapper.toSpiceDBException(
            r.error().toStatusRuntimeException(),
            "check item " + i + ": " + r.error().getMessage());
      out.add(r);
      i++;
    }
    return List.copyOf(out);
  }

  @Override
  public long importRelationships(Iterable<Relationship> relationships) {
    return importRelationships(relationships, ImportOptions.builder().build());
  }

  @Override
  public synchronized long importRelationships(
      Iterable<Relationship> input, ImportOptions options) {
    if (input == null || options == null)
      throw new InvalidArgumentException("relationships and options must not be null");
    rejectRetry(options.retryPolicy(), "import relationships");
    var batch = new ArrayList<Relationship>();
    for (var r : input) {
      if (r == null) throw new InvalidArgumentException("relationships must not contain null");
      batch.add(r);
    }
    respond(options.transport());
    var staged = new LinkedHashMap<String, Relationship>();
    for (var r : relationships.values()) if (live(r)) staged.put(key(r), r);
    for (var r : batch) {
      String k = key(r);
      if (staged.containsKey(k))
        throw ErrorMapper.newError(
            Status.Code.ALREADY_EXISTS, "found duplicate relationship `" + r + "` while importing");
      staged.put(k, r);
    }
    relationships.clear();
    relationships.putAll(staged);
    revision++;
    return batch.size();
  }

  @Override
  public Stream<ExportedRelationship> exportRelationships(Consistency consistency, Filter filter) {
    return exportRelationships(consistency, filter, ExportOptions.builder().build());
  }

  /** Every live relationship, or those matching {@code filter}; cursors and limits are honored. */
  @Override
  public synchronized Stream<ExportedRelationship> exportRelationships(
      Consistency consistency, Filter filter, ExportOptions options) {
    if (consistency == null || options == null)
      throw new InvalidArgumentException("consistency and options must not be null");
    Predicate<Relationship> match = filter == null ? r -> true : filter::matches;
    var matches = new ArrayList<Relationship>();
    for (var r : relationships.values()) if (live(r) && match.test(r)) matches.add(r);
    int start = cursorStart(options.cursor());
    respond(options.transport());
    var out = new ArrayList<ExportedRelationship>();
    for (int i = start; i < matches.size(); i++) {
      if (options.limit() > 0 && out.size() >= options.limit()) break;
      out.add(new ExportedRelationship(matches.get(i), "inmemory-cursor-" + (i + 1)));
    }
    return out.stream();
  }

  @Override
  public SpiceDBClient.SchemaResult readSchema() {
    return readSchema(CallOptions.builder().build());
  }

  /** The schema last written, or {@code NOT_FOUND}, as SpiceDB does. */
  @Override
  public synchronized SpiceDBClient.SchemaResult readSchema(CallOptions options) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    respond(options.transport());
    if (schema == null)
      throw ErrorMapper.newError(
          Status.Code.NOT_FOUND, "No schema has been defined; please call WriteSchema to start");
    return new SpiceDBClient.SchemaResult(schema, token());
  }

  @Override
  public ZedToken writeSchema(String schema) {
    return writeSchema(schema, CallOptions.builder().build());
  }

  /** Stores the schema text. It is not compiled or validated. */
  @Override
  public synchronized ZedToken writeSchema(String schema, CallOptions options) {
    if (schema == null || options == null)
      throw new InvalidArgumentException("schema and options must not be null");
    respond(options.transport());
    this.schema = schema;
    revision++;
    return token();
  }

  @Override
  public SpiceDBClient.ReflectSchemaResult reflectSchema(Consistency consistency) {
    return reflectSchema(consistency, CallOptions.builder().build());
  }

  /**
   * The result registered with {@link #setReflectSchemaResult}, stamped with the current revision;
   * without one, {@code NOT_FOUND}.
   */
  @Override
  public synchronized SpiceDBClient.ReflectSchemaResult reflectSchema(
      Consistency consistency, CallOptions options) {
    if (consistency == null || options == null)
      throw new InvalidArgumentException("consistency and options must not be null");
    respond(options.transport());
    if (reflectResult == null)
      throw ErrorMapper.newError(
          Status.Code.NOT_FOUND, "No schema has been defined; please call WriteSchema to start");
    return new SpiceDBClient.ReflectSchemaResult(
        reflectResult.definitions(), reflectResult.caveats(), token());
  }
}
