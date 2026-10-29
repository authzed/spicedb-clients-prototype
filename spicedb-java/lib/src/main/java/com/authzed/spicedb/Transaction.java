package com.authzed.spicedb;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;

/**
 * A transaction builder for batching relationship writes to SpiceDB.
 *
 * <p>Collects relationship mutations (create, touch, delete) and preconditions, then passes them to
 * {@link SpiceDBClient#write(Transaction)}. This builder is mutable and not thread-safe; {@code
 * write} snapshots it.
 *
 * <pre>{@code
 * var txn = new Transaction();
 * txn.create(relationship);
 * txn.touch(anotherRelationship);
 * txn.mustNotMatch(filter);
 * WriteResult result = client.write(txn);
 * ZedToken revision = result.revision();
 * }</pre>
 */
public final class Transaction {

  /** The operation type for a relationship mutation. */
  public enum Operation {
    CREATE,
    TOUCH,
    DELETE
  }

  /** A single relationship mutation within a transaction. */
  public record Mutation(Operation operation, Relationship relationship) {
    /** {@code OPERATION(tuple)}, e.g. {@code TOUCH(document:1#viewer@user:alice)}. */
    @Override
    public String toString() {
      return operation + "(" + relationship + ")";
    }

    /** Structured JSON diagnostic form. */
    public String toJson() {
      return Json.write(jsonValue());
    }

    Object jsonValue() {
      var m = new LinkedHashMap<String, Object>();
      m.put("operation", operation.name());
      m.put("relationship", relationship.toJsonValue());
      return m;
    }
  }

  /** The type of precondition check. */
  public enum PreconditionOperation {
    MUST_NOT_MATCH,
    MUST_MATCH
  }

  /** A precondition that must hold for the transaction to succeed. */
  public record Precondition(PreconditionOperation operation, Filter filter) {
    /** {@code OPERATION(filter)}, e.g. {@code MUST_MATCH(document:1#*@*)}. */
    @Override
    public String toString() {
      return operation + "(" + filter + ")";
    }

    /** Structured JSON diagnostic form. */
    public String toJson() {
      return "{\"operation\":\"" + operation.name() + "\",\"filter\":" + filter.toJson() + "}";
    }
  }

  private final List<Mutation> mutations = new ArrayList<>();
  private final List<Precondition> preconditions = new ArrayList<>();

  /** Adds a relationship create to the transaction. Fails if the relationship already exists. */
  public void create(Relationship r) {
    mutations.add(new Mutation(Operation.CREATE, r));
  }

  /** Adds a relationship touch to the transaction. Creates or updates the relationship. */
  public void touch(Relationship r) {
    mutations.add(new Mutation(Operation.TOUCH, r));
  }

  /** Adds a relationship delete to the transaction. */
  public void delete(Relationship r) {
    mutations.add(new Mutation(Operation.DELETE, r));
  }

  /**
   * Adds a precondition that no relationships match the given filter. The transaction will fail if
   * any matching relationship exists.
   */
  public void mustNotMatch(Filter f) {
    preconditions.add(new Precondition(PreconditionOperation.MUST_NOT_MATCH, f));
  }

  /**
   * Adds a precondition that at least one relationship matches the given filter. The transaction
   * will fail if no matching relationship exists.
   */
  public void mustMatch(Filter f) {
    preconditions.add(new Precondition(PreconditionOperation.MUST_MATCH, f));
  }

  /** Returns an unmodifiable view of the mutations in this transaction. */
  public List<Mutation> mutations() {
    return Collections.unmodifiableList(mutations);
  }

  /** Returns an unmodifiable view of the preconditions in this transaction. */
  public List<Precondition> preconditions() {
    return Collections.unmodifiableList(preconditions);
  }

  /** Returns true if this transaction has no mutations. */
  public boolean isEmpty() {
    return mutations.isEmpty();
  }

  /**
   * Whether replaying this transaction is safe: it contains only touches and no preconditions (an
   * empty transaction is). This is the rule the client uses to decide whether a write may be
   * retried.
   */
  public boolean isIdempotent() {
    if (!preconditions.isEmpty()) return false;
    for (var m : mutations) if (m.operation() != Operation.TOUCH) return false;
    return true;
  }

  /** The number of mutations, without copying them. */
  public int size() {
    return mutations.size();
  }

  /** Preconditions then mutations, e.g. {@code [MUST_MATCH(...), TOUCH(...)]}. */
  @Override
  public String toString() {
    var parts = new ArrayList<String>();
    for (var p : preconditions) parts.add(p.toString());
    for (var m : mutations) parts.add(m.toString());
    return "[" + String.join(", ", parts) + "]";
  }

  /** Structured JSON diagnostic form: {@code {"preconditions":[...],"mutations":[...]}}. */
  public String toJson() {
    var sb = new StringBuilder("{\"preconditions\":[");
    for (int i = 0; i < preconditions.size(); i++) {
      if (i > 0) sb.append(',');
      sb.append(preconditions.get(i).toJson());
    }
    sb.append("],\"mutations\":[");
    for (int i = 0; i < mutations.size(); i++) {
      if (i > 0) sb.append(',');
      sb.append(mutations.get(i).toJson());
    }
    return sb.append("]}").toString();
  }
}
