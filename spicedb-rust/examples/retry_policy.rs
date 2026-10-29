//! Demonstrates which calls this client retries on your behalf and which it
//! deliberately does not -- see root DESIGN.md, "RULE: Automatic retry is for
//! idempotent operations only".
//!
//! The rule exists because a silently retried mutation produces a confident
//! wrong answer. If a `WriteRelationships` carrying `OPERATION_CREATE` commits
//! and the response is lost, the retry comes back `ALREADY_EXISTS` -- and the
//! caller concludes a write failed that in fact succeeded. Retrying reads is
//! free; retrying mutations is only safe when the caller opted in knowing that.
//!
//! Attempts are counted *server-side*, which is the only way to tell a retry
//! from its absence: from the caller's side a transparently-retried success and
//! a first-try success are identical, and that is exactly the property that
//! would rot unnoticed.
//!
//! It drives a stand-in because a real SpiceDB cannot be asked to fail
//! transiently on demand.
//!
//! Run with: `cargo run --example retry_policy`

mod common;

use common::CheckBehavior;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{Relationship, Transaction};
use tonic::Code;

fn doc() -> Relationship {
    spicedb::Relationship::from_tuple("document:readme#view@user:alice")
        .expect("invalid relationship")
}

#[tokio::main]
async fn main() {
    // ── 1. A read IS retried, transparently ──────────────────────────────
    //
    // Two UNAVAILABLE responses, then success. The caller sees one successful
    // check and never learns the first two attempts happened -- the entire
    // value of retrying reads, and safe precisely because a repeated read
    // changes nothing.
    let (addr, counts) = common::spawn(CheckBehavior::FailThenGrant {
        failures: 2,
        code: Code::Unavailable,
    })
    .await;
    let client = SpiceDBClient::new_plaintext(
        format!("{addr}"),
        "t",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("connect to the stand-in");

    let result = client
        .check(spicedb::PermissionCheck::new(
            doc().resource().clone(),
            "view",
            doc().subject().clone(),
            (consistency::full()).clone(),
        ))
        .await
        .expect("a read failing transiently should have been retried to success");
    assert!(
        result.has_permission(),
        "the retried check should have granted"
    );
    assert_eq!(
        counts.check(),
        3,
        "expected 2 failures plus 1 success = 3 attempts, got {} (0 or 1 means reads are not \
         being retried at all)",
        counts.check()
    );
    println!(
        "read: failed twice with UNAVAILABLE, retried to success in {} attempts",
        counts.check()
    );

    // ── 2. A mutation is NOT retried ─────────────────────────────────────
    //
    // The same transient code, on a write. The error reaches the caller on the
    // first attempt, so the caller -- who alone knows whether a replay is safe
    // for the transaction they built -- decides what happens next.
    let (write_addr, write_counts) = common::spawn(CheckBehavior::GrantUnlessStaleToken).await;
    let write_client = SpiceDBClient::new_plaintext(
        format!("{write_addr}"),
        "t",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("connect to the stand-in");

    let mut txn = Transaction::new();
    let write_rel = spicedb::Relationship::from_tuple("document:readme#viewer@user:alice")
        .expect("invalid relationship");
    txn.touch(&write_rel);
    let err = write_client
        .write(spicedb::WriteRequest::new((txn).clone()))
        .await
        .expect_err("the stand-in always fails writes; this should have surfaced an error");
    assert!(
        matches!(err, SpiceDBError::Unavailable(_)),
        "expected the transient failure to surface as Unavailable: {err:?}"
    );
    assert_eq!(
        write_counts.write(),
        1,
        "a mutation must not be retried silently: WriteRelationships saw {} attempts, so a lost \
         response would leave the caller believing a committed write had failed",
        write_counts.write()
    );
    println!(
        "mutation: failed with UNAVAILABLE and was attempted exactly {} time -- not retried",
        write_counts.write()
    );

    // ── 2b. An idempotent mutation may opt in, one call at a time ────────
    //
    // A touch-only write without preconditions converges to the same state
    // however often it is replayed, so the caller may pass a per-call retry
    // policy. The stand-in still fails every write, so one retry means two
    // attempts. A create cannot opt in: it fails locally, before any request.
    let retry_once = spicedb::CallOptions::new().with_retry_policy(
        spicedb::RetryPolicy::new()
            .with_max_retries(1)
            .with_initial_backoff(std::time::Duration::from_millis(1)),
    );
    let before = write_counts.write();
    let err = write_client
        .write(
            spicedb::WriteRequest::new(txn.clone())
                .with_options(spicedb::WriteOptions::new().with_call_options(retry_once.clone())),
        )
        .await
        .expect_err("the stand-in always fails writes");
    assert!(matches!(err, SpiceDBError::Unavailable(_)), "{err:?}");
    assert_eq!(
        write_counts.write() - before,
        2,
        "an opted-in touch-only write should be attempted twice"
    );
    let mut create = Transaction::new();
    create.create(&write_rel);
    let err = write_client
        .write(
            spicedb::WriteRequest::new(create)
                .with_options(spicedb::WriteOptions::new().with_call_options(retry_once)),
        )
        .await
        .expect_err("a create must refuse the retry opt-in");
    assert!(matches!(err, SpiceDBError::InvalidArgument(_)), "{err:?}");
    assert_eq!(
        write_counts.write() - before,
        2,
        "the refused opt-in must not send a request"
    );
    println!("idempotent mutation: a touch-only write opted in and was attempted twice; a create refused the opt-in locally");

    // ── 2c. A client-wide policy for one operation ───────────────────────
    //
    // `operation_retry_policy` applies to every call of one operation. For
    // writes it retries only the idempotent ones (`Transaction::is_idempotent`)
    // and sends the rest once, without error. Operations that are never
    // idempotent (deletes, imports, ...) refuse a retrying policy when the
    // client is built.
    let per_operation = SpiceDBClient::builder(format!("{write_addr}"), "t")
        .plaintext()
        .operation_retry_policy(
            spicedb::Operation::Write,
            spicedb::RetryPolicy::new()
                .with_max_retries(1)
                .with_initial_backoff(std::time::Duration::from_millis(1)),
        )
        .build()
        .await
        .expect("connect to the stand-in");
    assert!(txn.is_idempotent());
    let before = write_counts.write();
    per_operation
        .write(spicedb::WriteRequest::new(txn.clone()))
        .await
        .expect_err("the stand-in always fails writes");
    assert_eq!(
        write_counts.write() - before,
        2,
        "the touch-only write is retried"
    );
    let mut create = Transaction::new();
    create.create(&write_rel);
    let err = per_operation
        .write(spicedb::WriteRequest::new(create))
        .await
        .expect_err("the stand-in always fails writes");
    assert!(matches!(err, SpiceDBError::Unavailable(_)), "{err:?}");
    assert_eq!(
        write_counts.write() - before,
        3,
        "the create is sent once, not rejected"
    );
    let refused = SpiceDBClient::builder(format!("{write_addr}"), "t")
        .plaintext()
        .operation_retry_policy(
            spicedb::Operation::DeleteRelationships,
            spicedb::RetryPolicy::new().with_max_retries(1),
        )
        .build()
        .await;
    assert!(matches!(refused, Err(SpiceDBError::InvalidArgument(_))));
    println!("per-operation policy: touch-only writes retried, creates sent once, delete retries refused at build");

    // ── 3. RESOURCE_EXHAUSTED is not retryable, even on a read ───────────
    //
    // In SpiceDB this code means memory load-shed or a deterministic
    // MaxDepthExceeded. Retrying the first makes the overload worse; the second
    // can never succeed however many times it is tried.
    let (exhausted_addr, exhausted_counts) = common::spawn(CheckBehavior::FailThenGrant {
        failures: 99,
        code: Code::ResourceExhausted,
    })
    .await;
    let exhausted_client = SpiceDBClient::new_plaintext(
        format!("{exhausted_addr}"),
        "t",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("connect to the stand-in");

    let err = exhausted_client
        .check(spicedb::PermissionCheck::new(
            doc().resource().clone(),
            "view",
            doc().subject().clone(),
            (consistency::full()).clone(),
        ))
        .await
        .expect_err("the stand-in always fails here; this should have surfaced an error");
    assert!(
        matches!(err, SpiceDBError::ResourceExhausted(_)),
        "expected ResourceExhausted: {err:?}"
    );
    assert_eq!(
        exhausted_counts.check(),
        1,
        "RESOURCE_EXHAUSTED must not be retried: saw {} attempts, which turns a load-shedding \
         SpiceDB into a client-driven retry storm",
        exhausted_counts.check()
    );
    println!(
        "RESOURCE_EXHAUSTED: attempted exactly {} time -- no retry storm",
        exhausted_counts.check()
    );

    println!("retry_policy: reads retried, mutations and load-shed left to the caller");
}
