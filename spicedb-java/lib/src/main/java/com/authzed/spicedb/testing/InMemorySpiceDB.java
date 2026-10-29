package com.authzed.spicedb.testing;

import com.authzed.spicedb.BulkItemErrors;
import com.authzed.spicedb.CallOptions;
import com.authzed.spicedb.CheckOptions;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.ClientOperation;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.DeleteAllOptions;
import com.authzed.spicedb.DeleteOptions;
import com.authzed.spicedb.ExportOptions;
import com.authzed.spicedb.ExportedRelationship;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.ImportOptions;
import com.authzed.spicedb.LookupOptions;
import com.authzed.spicedb.LookupResult;
import com.authzed.spicedb.Materialize;
import com.authzed.spicedb.ObjectRef;
import com.authzed.spicedb.PartialDeletion;
import com.authzed.spicedb.PermissionCheck;
import com.authzed.spicedb.PermissionSetOptions;
import com.authzed.spicedb.PermissionsApi;
import com.authzed.spicedb.Permissionship;
import com.authzed.spicedb.ReadOptions;
import com.authzed.spicedb.ReadRelationship;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.ResponseMetadata;
import com.authzed.spicedb.RetryPolicy;
import com.authzed.spicedb.SpiceDBApi;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.SubjectRef;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.TransportOptions;
import com.authzed.spicedb.WatchOptions;
import com.authzed.spicedb.WriteOptions;
import com.authzed.spicedb.WriteResult;
import com.authzed.spicedb.ZedToken;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.Channel;
import io.grpc.Metadata;
import io.grpc.Status;
import java.time.Clock;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collection;
import java.util.EnumMap;
import java.util.HashMap;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.NoSuchElementException;
import java.util.Spliterator;
import java.util.Spliterators;
import java.util.function.Function;
import java.util.function.Predicate;
import java.util.stream.Stream;
import java.util.stream.StreamSupport;

/**
 * An in-memory test double for {@link SpiceDBApi}, the full method set of {@link SpiceDBClient},
 * with the same signatures, so code written against {@code SpiceDBApi} (or its relationship, schema
 * and check subset {@link PermissionsApi}) accepts either.
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
 *   <li>{@link #readSchema} reports the current revision, or none after {@link
 *       #setReadSchemaRevision}{@code (SchemaRevision.OMITTED)}, as some SpiceDB deployments do.
 *   <li>Checks return registered results ({@link #setCheckResult}, {@link #setCheckError});
 *       unregistered checks are {@code NO_PERMISSION}, and {@code checkAny}/{@code checkAll} are
 *       bulk checks over them. The double does not evaluate schema.
 *   <li>{@link #deleteAllRelationshipsNonAtomic} deletes every match in one step after checking its
 *       preconditions and reports progress once; {@link #close} does nothing.
 *   <li>Operations without in-memory semantics (lookups, expand, schema analysis, watch, counters
 *       and the other experimental operations) are recorded and consult the fault hook like any
 *       other, then throw {@code UNIMPLEMENTED} in the client's error shape. {@link #rawChannel}
 *       returns {@code null}.
 *   <li>Every operation reports the metadata registered with {@link #setResponseMetadata} to the
 *       call's {@link TransportOptions#responseMetadata()} capture.
 *   <li>Every call is recorded ({@link #calls}, {@link #callsOf}) with its arguments and options,
 *       so tests can assert on what was sent without a strict mock.
 *   <li>Faults can be injected ({@link #setFaultHook}, {@link #failNext}) before any call, before
 *       each import batch, before an import commits, and before each read or export item, so
 *       failure paths need no fake gRPC server. A failed write or import applies nothing.
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
public final class InMemorySpiceDB implements SpiceDBApi {
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
  private final List<Call> calls = new ArrayList<>();
  private final Map<ClientOperation, Integer> callCounts = new EnumMap<>(ClientOperation.class);
  private Function<Fault, ? extends RuntimeException> faultHook;
  private SchemaRevision schemaRevision = SchemaRevision.INCLUDED;

  /**
   * One recorded call.
   *
   * @param operation the operation called
   * @param index counts calls of this operation, starting at 0
   * @param arguments the call's arguments other than its options, in signature order (for example
   *     {@code [consistency, filter]} for a read, {@code [filter, limit]} for a bounded delete); a
   *     bulk check's requests are a snapshot list, and other values are the caller's own objects
   * @param options the call's options object: the caller's, or the defaults the overload without
   *     options uses
   */
  public record Call(ClientOperation operation, int index, List<Object> arguments, Object options) {
    public Call {
      arguments = java.util.Collections.unmodifiableList(new ArrayList<>(arguments));
    }

    /**
     * The options cast to the operation's options class, e.g. {@code options(WriteOptions.class)}.
     */
    public <T> T options(Class<T> type) {
      return type.cast(options);
    }
  }

  /** Where in an operation a fault hook runs. */
  public enum FaultStage {
    /**
     * Before the operation does anything. Streaming operations run it when the stream is first
     * consumed, where the real client reports a failed call.
     */
    CALL,
    /**
     * Before import batch {@link Fault#item()} (0-based) is sent; {@link Fault#batch()} holds its
     * relationships. Batches have the call's {@code ImportOptions.batchSize} (default 1,000).
     */
    IMPORT_BATCH,
    /** After every import batch, before the import commits. */
    IMPORT_COMMIT,
    /** Before a read or export yields item {@link Fault#item()} (0-based). */
    ITEM
  }

  /**
   * The point at which a fault hook is consulted.
   *
   * @param item the batch index for {@link FaultStage#IMPORT_BATCH} and the item index for {@link
   *     FaultStage#ITEM}; 0 otherwise
   * @param batch the relationships in the batch at {@link FaultStage#IMPORT_BATCH}; empty otherwise
   */
  public record Fault(Call call, FaultStage stage, int item, List<Relationship> batch) {
    public Fault {
      batch = batch == null ? List.of() : List.copyOf(batch);
    }

    /** A fault point without import batch contents. */
    public Fault(Call call, FaultStage stage, int item) {
      this(call, stage, item, List.of());
    }
  }

  /** Whether {@link #readSchema} reports a revision. */
  public enum SchemaRevision {
    /** Report the current revision. The default. */
    INCLUDED,
    /**
     * Report none ({@code SchemaResult.revision()} is {@code null}), as some SpiceDB deployments
     * do, so code that falls back to another revision source can be tested.
     */
    OMITTED
  }

  /** Selects whether {@link #readSchema} reports a revision. */
  public synchronized void setReadSchemaRevision(SchemaRevision mode) {
    if (mode == null) throw new InvalidArgumentException("mode must not be null");
    schemaRevision = mode;
  }

  private static final String CURSOR_PREFIX = "inmemory-cursor-";

  /**
   * The cursor that resumes a read or export after the first {@code n} matching relationships (in
   * write order): the same cursor this double puts on the {@code n}-th item. The format, {@code
   * "inmemory-cursor-"} followed by the decimal {@code n}, is stable for tests.
   */
  public static String cursorAfter(int n) {
    if (n < 0) throw new InvalidArgumentException("n must not be negative");
    return CURSOR_PREFIX + n;
  }

  /**
   * Builds the error the real client throws when SpiceDB answers with {@code code} and {@code
   * message}, for fault hooks. Same as {@link ErrorMapper#newError}.
   */
  public static SpiceDBException serverError(Status.Code code, String message) {
    return ErrorMapper.newError(code, message);
  }

  /**
   * Installs {@code hook}, which runs at every {@link Fault} point: a non-null exception it returns
   * is thrown by the operation at that point (by the stream, for stream items), and a failed write
   * or import applies nothing. Build errors with {@link #serverError} to match the real client's
   * shape. The hook must not call this double. Pass {@code null} to remove it.
   */
  public synchronized void setFaultHook(Function<Fault, ? extends RuntimeException> hook) {
    faultHook = hook;
  }

  /**
   * Makes the next call of {@code operation} throw {@code error} before doing anything; later calls
   * succeed. It replaces any fault hook.
   */
  public void failNext(ClientOperation operation, RuntimeException error) {
    if (operation == null || error == null)
      throw new InvalidArgumentException("operation and error must not be null");
    var failed = new java.util.concurrent.atomic.AtomicBoolean();
    setFaultHook(
        f ->
            f.stage() == FaultStage.CALL
                    && f.call().operation() == operation
                    && failed.compareAndSet(false, true)
                ? error
                : null);
  }

  /** Every recorded call, in order. */
  public synchronized List<Call> calls() {
    return List.copyOf(calls);
  }

  /** The recorded calls of one operation, in order. */
  public synchronized List<Call> callsOf(ClientOperation operation) {
    return calls.stream().filter(c -> c.operation() == operation).toList();
  }

  /** Records a call; non-streaming operations then run its {@link FaultStage#CALL} hook. */
  private synchronized Call record(ClientOperation operation, Object options, Object... arguments) {
    int index = callCounts.merge(operation, 1, Integer::sum) - 1;
    var call = new Call(operation, index, Arrays.asList(arguments), options);
    calls.add(call);
    return call;
  }

  private Call begin(ClientOperation operation, Object options, Object... arguments) {
    var call = record(operation, options, arguments);
    fault(call, FaultStage.CALL, 0);
    return call;
  }

  /** Runs the fault hook, if any, at one point of a call, throwing the fault it returns. */
  private void fault(Call call, FaultStage stage, int item) {
    fault(call, stage, item, List.of());
  }

  private void fault(Call call, FaultStage stage, int item, List<Relationship> batch) {
    Function<Fault, ? extends RuntimeException> hook;
    synchronized (this) {
      hook = faultHook;
    }
    if (hook == null) return;
    var error = hook.apply(new Fault(call, stage, item, batch));
    if (error != null) throw error;
  }

  /**
   * A lazy stream over {@code items} that runs the call's {@link FaultStage#CALL} hook when first
   * consumed and the {@link FaultStage#ITEM} hook before each item.
   */
  private <T> Stream<T> faultingStream(Call call, List<T> items) {
    Iterator<T> it =
        new Iterator<>() {
          private boolean started;
          private int next;

          @Override
          public boolean hasNext() {
            if (!started) {
              started = true;
              fault(call, FaultStage.CALL, 0);
            }
            return next < items.size();
          }

          @Override
          public T next() {
            if (!hasNext()) throw new NoSuchElementException();
            fault(call, FaultStage.ITEM, next);
            return items.get(next++);
          }
        };
    return StreamSupport.stream(
        Spliterators.spliteratorUnknownSize(it, Spliterator.ORDERED | Spliterator.NONNULL), false);
  }

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
      if (!cursor.startsWith(CURSOR_PREFIX)) throw new NumberFormatException();
      int n = Integer.parseInt(cursor.substring(CURSOR_PREFIX.length()));
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
    begin(ClientOperation.WRITE, options, txn);
    if (!txn.isIdempotent()) rejectRetry(options.retryPolicy(), "write");
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
    var call = record(ClientOperation.READ_RELATIONSHIPS, options, consistency, filter);
    var matches = matching(filter);
    int start = cursorStart(options.cursor());
    respond(options.transport());
    var out = new ArrayList<ReadRelationship>();
    for (int i = start; i < matches.size(); i++) {
      if (options.limit() > 0 && out.size() >= options.limit()) break;
      out.add(new ReadRelationship(matches.get(i), cursorAfter(i + 1), token()));
    }
    return faultingStream(call, out);
  }

  @Override
  public SpiceDBClient.DeleteResult deleteRelationships(Filter filter, int limit) {
    return deleteRelationships(filter, limit, DeleteOptions.builder().build());
  }

  @Override
  public SpiceDBClient.DeleteResult deleteRelationships(
      Filter filter, int limit, DeleteOptions options) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    begin(ClientOperation.DELETE_RELATIONSHIPS, options, filter, limit);
    rejectRetry(options.retryPolicy(), "delete relationships");
    if (limit <= 0) throw new InvalidArgumentException("limit must be positive");
    return delete(filter, limit, options);
  }

  @Override
  public SpiceDBClient.DeleteResult deleteRelationships(Filter filter, DeleteOptions options) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    begin(ClientOperation.DELETE_RELATIONSHIPS, options, filter);
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
    begin(ClientOperation.CHECK, options, request);
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
    begin(ClientOperation.CHECK_BULK, options, new ArrayList<>(requests));
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
    var call = begin(ClientOperation.IMPORT_RELATIONSHIPS, options, input);
    rejectRetry(options.retryPolicy(), "import relationships");
    int batchSize = Math.max(options.batchSize(), 1);
    var batch = new ArrayList<Relationship>();
    for (var r : input) {
      if (r == null) throw new InvalidArgumentException("relationships must not contain null");
      batch.add(r);
    }
    for (int start = 0; start < batch.size(); start += batchSize)
      fault(
          call,
          FaultStage.IMPORT_BATCH,
          start / batchSize,
          batch.subList(start, Math.min(start + batchSize, batch.size())));
    fault(call, FaultStage.IMPORT_COMMIT, 0);
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
    var call = record(ClientOperation.EXPORT_RELATIONSHIPS, options, consistency, filter);
    Predicate<Relationship> match = filter == null ? r -> true : filter::matches;
    var matches = new ArrayList<Relationship>();
    for (var r : relationships.values()) if (live(r) && match.test(r)) matches.add(r);
    int start = cursorStart(options.cursor());
    respond(options.transport());
    var out = new ArrayList<ExportedRelationship>();
    for (int i = start; i < matches.size(); i++) {
      if (options.limit() > 0 && out.size() >= options.limit()) break;
      out.add(new ExportedRelationship(matches.get(i), cursorAfter(i + 1)));
    }
    return faultingStream(call, out);
  }

  @Override
  public SpiceDBClient.SchemaResult readSchema() {
    return readSchema(CallOptions.builder().build());
  }

  /** The schema last written, or {@code NOT_FOUND}, as SpiceDB does. */
  @Override
  public synchronized SpiceDBClient.SchemaResult readSchema(CallOptions options) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    begin(ClientOperation.READ_SCHEMA, options);
    respond(options.transport());
    if (schema == null)
      throw ErrorMapper.newError(
          Status.Code.NOT_FOUND, "No schema has been defined; please call WriteSchema to start");
    return new SpiceDBClient.SchemaResult(
        schema, schemaRevision == SchemaRevision.OMITTED ? null : token());
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
    begin(ClientOperation.WRITE_SCHEMA, options, schema);
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
    begin(ClientOperation.REFLECT_SCHEMA, options, consistency);
    respond(options.transport());
    if (reflectResult == null)
      throw ErrorMapper.newError(
          Status.Code.NOT_FOUND, "No schema has been defined; please call WriteSchema to start");
    return new SpiceDBClient.ReflectSchemaResult(
        reflectResult.definitions(), reflectResult.caveats(), token());
  }

  /** A bulk check (recorded as {@link ClientOperation#CHECK_BULK}), as the client does. */
  @Override
  public boolean checkAny(Collection<PermissionCheck> requests) {
    return checkAny(requests, CheckOptions.builder().build());
  }

  @Override
  public boolean checkAny(Collection<PermissionCheck> requests, CheckOptions options) {
    return checkBulk(requests, options).stream().anyMatch(CheckResult::hasPermission);
  }

  /** A bulk check (recorded as {@link ClientOperation#CHECK_BULK}), as the client does. */
  @Override
  public boolean checkAll(Collection<PermissionCheck> requests) {
    return checkAll(requests, CheckOptions.builder().build());
  }

  @Override
  public boolean checkAll(Collection<PermissionCheck> requests, CheckOptions options) {
    var results = checkBulk(requests, options);
    return !results.isEmpty() && results.stream().allMatch(CheckResult::hasPermission);
  }

  @Override
  public SpiceDBClient.DeleteResult deleteAllRelationshipsNonAtomic(Filter filter) {
    return deleteAllRelationshipsNonAtomic(filter, DeleteAllOptions.builder().build());
  }

  /**
   * Deletes every match in one step after checking the preconditions, and reports progress once.
   * Page size and resume cursor are accepted and ignored.
   */
  @Override
  public synchronized SpiceDBClient.DeleteResult deleteAllRelationshipsNonAtomic(
      Filter filter, DeleteAllOptions options) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    begin(ClientOperation.DELETE_ALL_RELATIONSHIPS_NON_ATOMIC, options, filter);
    rejectRetry(options.retryPolicy(), "delete relationships");
    if (filter == null) throw new InvalidArgumentException("filter must not be null");
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
    for (var r : matches) relationships.remove(key(r));
    revision++;
    var result = new SpiceDBClient.DeleteResult(token(), matches.size(), true, null);
    if (options.onProgress() != null) options.onProgress().accept(result);
    return result;
  }

  /** Does nothing: the double holds no transport. */
  @Override
  public void close() {}

  /** {@code null}: the double has no transport. */
  @Override
  public Channel rawChannel() {
    return null;
  }

  // ---------------------------------------------------------------------------------------------
  // Operations without in-memory semantics: recorded, consult the fault hook, then UNIMPLEMENTED.
  // ---------------------------------------------------------------------------------------------

  private static SpiceDBException notImplemented() {
    return ErrorMapper.newError(Status.Code.UNIMPLEMENTED, "not implemented by InMemorySpiceDB");
  }

  /**
   * Records the call, runs its {@link FaultStage#CALL} hook, refuses a retry opt-in on a mutation
   * that is never idempotent (as the client does), then throws UNIMPLEMENTED.
   */
  private <T> T unimplemented(ClientOperation operation, CallOptions options, Object... arguments) {
    return unimplementedWithRetry(
        operation, options, options == null ? null : options.retryPolicy(), arguments);
  }

  private <T> T unimplementedWithRetry(
      ClientOperation operation, Object options, RetryPolicy retry, Object... arguments) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    begin(operation, options, arguments);
    if (operation.isNeverIdempotent())
      rejectRetry(retry, operation.name().toLowerCase(java.util.Locale.ROOT).replace('_', ' '));
    throw notImplemented();
  }

  /**
   * Records the call and returns a stream that, when first consumed, runs the {@link
   * FaultStage#CALL} hook and then throws UNIMPLEMENTED, where the real client reports a failed
   * call.
   */
  private <T> Stream<T> unimplementedStream(
      ClientOperation operation, Object options, Object... arguments) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    var call = record(operation, options, arguments);
    Iterator<T> it =
        new Iterator<>() {
          @Override
          public boolean hasNext() {
            fault(call, FaultStage.CALL, 0);
            throw notImplemented();
          }

          @Override
          public T next() {
            hasNext();
            throw new NoSuchElementException();
          }
        };
    return StreamSupport.stream(
        Spliterators.spliteratorUnknownSize(it, Spliterator.ORDERED), false);
  }

  @Override
  public Stream<LookupResult.LookupResource> lookupResources(
      Consistency consistency, String resourceType, String permission, SubjectRef subject) {
    return lookupResources(
        consistency, resourceType, permission, subject, LookupOptions.builder().build());
  }

  @Override
  public Stream<LookupResult.LookupResource> lookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      SubjectRef subject,
      LookupOptions options) {
    return unimplementedStream(
        ClientOperation.LOOKUP_RESOURCES, options, consistency, resourceType, permission, subject);
  }

  @Override
  public Stream<LookupResult.LookupSubject> lookupSubjects(
      Consistency consistency, ObjectRef resource, String permission, String subjectType) {
    return lookupSubjects(
        consistency, resource, permission, subjectType, LookupOptions.builder().build());
  }

  @Override
  public Stream<LookupResult.LookupSubject> lookupSubjects(
      Consistency consistency,
      ObjectRef resource,
      String permission,
      String subjectType,
      LookupOptions options) {
    return unimplementedStream(
        ClientOperation.LOOKUP_SUBJECTS, options, consistency, resource, permission, subjectType);
  }

  @Override
  public SpiceDBClient.ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName) {
    return computablePermissions(
        consistency, definitionName, relationName, CallOptions.builder().build());
  }

  @Override
  public SpiceDBClient.ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName, CallOptions options) {
    return unimplemented(
        ClientOperation.COMPUTABLE_PERMISSIONS, options, consistency, definitionName, relationName);
  }

  @Override
  public SpiceDBClient.DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName) {
    return dependentRelations(
        consistency, definitionName, permissionName, CallOptions.builder().build());
  }

  @Override
  public SpiceDBClient.DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName, CallOptions options) {
    return unimplemented(
        ClientOperation.DEPENDENT_RELATIONS, options, consistency, definitionName, permissionName);
  }

  @Override
  public SpiceDBClient.DiffSchemaResult diffSchema(
      Consistency consistency, String comparisonSchema) {
    return diffSchema(consistency, comparisonSchema, CallOptions.builder().build());
  }

  @Override
  public SpiceDBClient.DiffSchemaResult diffSchema(
      Consistency consistency, String comparisonSchema, CallOptions options) {
    return unimplemented(ClientOperation.DIFF_SCHEMA, options, consistency, comparisonSchema);
  }

  @Override
  public SpiceDBClient.ExpandResult expandPermissionTree(
      Consistency consistency, ObjectRef resource, String permission) {
    return expandPermissionTree(consistency, resource, permission, CallOptions.builder().build());
  }

  @Override
  public SpiceDBClient.ExpandResult expandPermissionTree(
      Consistency consistency, ObjectRef resource, String permission, CallOptions options) {
    return unimplemented(
        ClientOperation.EXPAND_PERMISSION_TREE, options, consistency, resource, permission);
  }

  @Override
  public Stream<SpiceDBClient.WatchEvent> updates(
      List<String> objectTypes, ZedToken startRevision) {
    return updates(objectTypes, startRevision, WatchOptions.builder().build());
  }

  @Override
  public Stream<SpiceDBClient.WatchEvent> updates(
      List<String> objectTypes, ZedToken startRevision, WatchOptions options) {
    return unimplementedStream(ClientOperation.WATCH, options, objectTypes, startRevision);
  }

  @Override
  public void experimentalRegisterRelationshipCounter(String name, Filter filter) {
    experimentalRegisterRelationshipCounter(name, filter, CallOptions.builder().build());
  }

  @Override
  public void experimentalRegisterRelationshipCounter(
      String name, Filter filter, CallOptions options) {
    unimplemented(ClientOperation.REGISTER_RELATIONSHIP_COUNTER, options, name, filter);
  }

  @Override
  public SpiceDBClient.CountResult experimentalCountRelationships(String name) {
    return experimentalCountRelationships(name, CallOptions.builder().build());
  }

  @Override
  public SpiceDBClient.CountResult experimentalCountRelationships(
      String name, CallOptions options) {
    return unimplemented(ClientOperation.COUNT_RELATIONSHIPS, options, name);
  }

  @Override
  public void experimentalUnregisterRelationshipCounter(String name) {
    experimentalUnregisterRelationshipCounter(name, CallOptions.builder().build());
  }

  @Override
  public void experimentalUnregisterRelationshipCounter(String name, CallOptions options) {
    unimplemented(ClientOperation.UNREGISTER_RELATIONSHIP_COUNTER, options, name);
  }

  @Override
  public Materialize.RoaringLookupResourcesResult experimentalRoaringLookupResources(
      Consistency consistency, String resourceType, String permission, SubjectRef subject) {
    return experimentalRoaringLookupResources(
        consistency, resourceType, permission, subject, LookupOptions.builder().build());
  }

  @Override
  public Materialize.RoaringLookupResourcesResult experimentalRoaringLookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      SubjectRef subject,
      LookupOptions options) {
    return unimplementedWithRetry(
        ClientOperation.ROARING_LOOKUP_RESOURCES,
        options,
        null,
        consistency,
        resourceType,
        permission,
        subject);
  }

  @Override
  public SpiceDBClient.CountResult experimentalCountRelationshipsByFilter(Filter filter) {
    return experimentalCountRelationshipsByFilter(filter, CallOptions.builder().build());
  }

  @Override
  public SpiceDBClient.CountResult experimentalCountRelationshipsByFilter(
      Filter filter, CallOptions options) {
    return unimplemented(ClientOperation.COUNT_RELATIONSHIPS_BY_FILTER, options, filter);
  }

  @Override
  public Stream<Materialize.PermissionSetEvent> experimentalWatchPermissionSets(
      ZedToken startRevision) {
    return experimentalWatchPermissionSets(startRevision, CallOptions.builder().build());
  }

  @Override
  public Stream<Materialize.PermissionSetEvent> experimentalWatchPermissionSets(
      ZedToken startRevision, CallOptions options) {
    return unimplementedStream(ClientOperation.WATCH_PERMISSION_SETS, options, startRevision);
  }

  @Override
  public Stream<Materialize.LookupPermissionSet> experimentalLookupPermissionSets(
      ZedToken atRevision) {
    return experimentalLookupPermissionSets(atRevision, PermissionSetOptions.builder().build());
  }

  @Override
  public Stream<Materialize.LookupPermissionSet> experimentalLookupPermissionSets(
      ZedToken atRevision, PermissionSetOptions options) {
    return unimplementedStream(ClientOperation.LOOKUP_PERMISSION_SETS, options, atRevision);
  }

  @Override
  public Materialize.DownloadPermissionSetsResult experimentalDownloadPermissionSets(
      ZedToken atRevision) {
    return experimentalDownloadPermissionSets(atRevision, CallOptions.builder().build());
  }

  @Override
  public Materialize.DownloadPermissionSetsResult experimentalDownloadPermissionSets(
      ZedToken atRevision, CallOptions options) {
    return unimplemented(ClientOperation.DOWNLOAD_PERMISSION_SETS, options, atRevision);
  }
}
