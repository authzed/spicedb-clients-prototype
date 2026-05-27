package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

class TransactionTest {

  private static final Relationship REL =
      Relationship.of("document", "doc1", "viewer", "user", "alice");

  @Test
  void emptyTransactionIsEmpty() {
    Transaction txn = new Transaction();
    assertTrue(txn.isEmpty());
    assertTrue(txn.mutations().isEmpty());
    assertTrue(txn.preconditions().isEmpty());
  }

  @Test
  void createAddsCreateMutation() {
    Transaction txn = new Transaction();
    txn.create(REL);
    assertFalse(txn.isEmpty());
    assertEquals(1, txn.mutations().size());
    assertEquals(Transaction.Operation.CREATE, txn.mutations().get(0).operation());
    assertEquals(REL, txn.mutations().get(0).relationship());
  }

  @Test
  void touchAddsTouchMutation() {
    Transaction txn = new Transaction();
    txn.touch(REL);
    assertEquals(Transaction.Operation.TOUCH, txn.mutations().get(0).operation());
  }

  @Test
  void deleteAddsDeleteMutation() {
    Transaction txn = new Transaction();
    txn.delete(REL);
    assertEquals(Transaction.Operation.DELETE, txn.mutations().get(0).operation());
  }

  @Test
  void multipleMutationsPreserveOrder() {
    Relationship r2 = Relationship.of("document", "doc2", "editor", "user", "bob");
    Transaction txn = new Transaction();
    txn.create(REL);
    txn.touch(r2);
    txn.delete(REL);

    assertEquals(3, txn.mutations().size());
    assertEquals(Transaction.Operation.CREATE, txn.mutations().get(0).operation());
    assertEquals(Transaction.Operation.TOUCH, txn.mutations().get(1).operation());
    assertEquals(Transaction.Operation.DELETE, txn.mutations().get(2).operation());
  }

  @Test
  void mustNotMatchAddsPrecondition() {
    Transaction txn = new Transaction();
    Filter f = Filter.of("document").withResourceID("doc1");
    txn.mustNotMatch(f);

    assertEquals(1, txn.preconditions().size());
    assertEquals(
        Transaction.PreconditionOperation.MUST_NOT_MATCH, txn.preconditions().get(0).operation());
    assertEquals(f, txn.preconditions().get(0).filter());
  }

  @Test
  void mustMatchAddsPrecondition() {
    Transaction txn = new Transaction();
    Filter f = Filter.of("document").withResourceID("doc1");
    txn.mustMatch(f);

    assertEquals(1, txn.preconditions().size());
    assertEquals(
        Transaction.PreconditionOperation.MUST_MATCH, txn.preconditions().get(0).operation());
  }

  @Test
  void mutationsListIsUnmodifiable() {
    Transaction txn = new Transaction();
    txn.create(REL);
    assertThrows(
        UnsupportedOperationException.class,
        () -> txn.mutations().add(new Transaction.Mutation(Transaction.Operation.TOUCH, REL)));
  }

  @Test
  void preconditionsListIsUnmodifiable() {
    Transaction txn = new Transaction();
    Filter f = Filter.of("document");
    txn.mustNotMatch(f);
    assertThrows(
        UnsupportedOperationException.class,
        () ->
            txn.preconditions()
                .add(
                    new Transaction.Precondition(Transaction.PreconditionOperation.MUST_MATCH, f)));
  }
}
