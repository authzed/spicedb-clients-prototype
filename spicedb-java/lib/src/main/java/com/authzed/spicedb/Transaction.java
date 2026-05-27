package com.authzed.spicedb;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * A transaction builder for batching relationship writes to SpiceDB.
 *
 * <p>Collects relationship mutations (create, touch, delete) and preconditions, then passes them to
 * {@link SpiceDBClient#write(Transaction)}.
 *
 * <pre>{@code
 * var txn = new Transaction();
 * txn.create(relationship);
 * txn.touch(anotherRelationship);
 * txn.mustNotMatch(filter);
 * String revision = client.write(txn);
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
  public record Mutation(Operation operation, Relationship relationship) {}

  /** The type of precondition check. */
  public enum PreconditionOperation {
    MUST_NOT_MATCH,
    MUST_MATCH
  }

  /** A precondition that must hold for the transaction to succeed. */
  public record Precondition(PreconditionOperation operation, Filter filter) {}

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
}
