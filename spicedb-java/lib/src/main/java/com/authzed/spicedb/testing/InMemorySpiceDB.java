package com.authzed.spicedb.testing;

import com.authzed.spicedb.BulkItemErrors;
import com.authzed.spicedb.CheckOptions;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.DeleteOptions;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.ObjectRef;
import com.authzed.spicedb.PartialDeletion;
import com.authzed.spicedb.PermissionCheck;
import com.authzed.spicedb.PermissionsApi;
import com.authzed.spicedb.Permissionship;
import com.authzed.spicedb.ReadOptions;
import com.authzed.spicedb.ReadRelationship;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.SubjectRef;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.WriteOptions;
import com.authzed.spicedb.WriteResult;
import com.authzed.spicedb.ZedToken;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.Status;
import java.time.Clock;
import java.util.ArrayList;
import java.util.Collection;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;

/**
 * An in-memory test double for {@link PermissionsApi}: the relationship and check operations of
 * {@link SpiceDBClient}, with the same signatures, so code written against {@code PermissionsApi}
 * accepts either.
 *
 * <ul>
 *   <li>Writes apply atomically, evaluate preconditions with {@link Filter#matches} (server filter
 *       semantics), fail {@code CREATE} of an existing relationship with {@code ALREADY_EXISTS},
 *       and reject a transaction that updates the same relationship twice with {@code
 *       INVALID_ARGUMENT}. Failed preconditions raise {@code FAILED_PRECONDITION}.
 *   <li>Reads and deletes use the same filter semantics; expired relationships are invisible, as on
 *       the server. Read items carry an opaque cursor that {@link ReadOptions.Builder#cursor}
 *       resumes from and the current revision as read-at.
 *   <li>Checks return registered results ({@link #setCheckResult}, {@link #setCheckError});
 *       unregistered checks are {@code NO_PERMISSION}. The double does not evaluate schema.
 * </ul>
 *
 * <p>Errors are built with {@link ErrorMapper#newError}, so every predicate treats them like server
 * errors. Consistency arguments are accepted and ignored: the double is always fully consistent.
 * Thread-safe.
 */
public final class InMemorySpiceDB implements PermissionsApi {
  /** SpiceDB's default limit for {@code DeleteRelationships} without one. */
  public static final int DEFAULT_DELETE_LIMIT = 1_000;

  private final Map<String, Relationship> relationships = new LinkedHashMap<>();
  private final Map<String, CheckResult> checkResults = new HashMap<>();
  private final Clock clock;
  private long revision;

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
    checkPreconditions(txn.preconditions());
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
    int start = 0;
    if (options.cursor() != null) {
      try {
        start = Integer.parseInt(options.cursor().substring("inmemory-cursor-".length()));
      } catch (RuntimeException e) {
        throw ErrorMapper.newError(Status.Code.INVALID_ARGUMENT, "invalid cursor");
      }
    }
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
    if (limit <= 0) throw new InvalidArgumentException("limit must be positive");
    return delete(filter, limit, options);
  }

  @Override
  public SpiceDBClient.DeleteResult deleteRelationships(Filter filter, DeleteOptions options) {
    if (options == null) throw new InvalidArgumentException("options must not be null");
    if (options.partialDeletion() != PartialDeletion.DISALLOWED)
      throw new InvalidArgumentException(
          "deleteRelationships without a limit requires PartialDeletion.DISALLOWED");
    return delete(filter, DEFAULT_DELETE_LIMIT, options);
  }

  private synchronized SpiceDBClient.DeleteResult delete(
      Filter filter, int limit, DeleteOptions options) {
    if (filter == null || options == null)
      throw new InvalidArgumentException("filter and options must not be null");
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
          Status.Code.FAILED_PRECONDITION,
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
}
