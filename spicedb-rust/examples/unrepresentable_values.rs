//! Demonstrates both directions of root DESIGN.md, "RULE: A conversion that
//! cannot preserve meaning must fail".
//!
//! The rule has two clauses that point opposite ways, and confusing them is the
//! failure mode either way:
//!
//! 1. Data the CALLER supplied that the client cannot represent must raise a
//!    typed error naming what could not be converted. The caller can see the
//!    failure and fix their input, so the client neither approximates the value
//!    nor drops it -- silently discarding it turns a caller's mistake into a
//!    silent wrong answer.
//! 2. Values the SERVER supplied that the client does not recognise must NOT
//!    raise, and must map to the safe, non-permissive default -- never a grant.
//!    Raising would turn a routine SpiceDB upgrade that adds an enum value into
//!    a client-side outage.
//!
//! # What clause 1 looks like in Rust, and what it cannot look like
//!
//! The sibling clients each demonstrate clause 1 twice: an unconvertible caveat
//! context value, and a filter the wire format cannot express. **Only the second
//! is reachable here.** This client types caveat context as
//! `HashMap<String, serde_json::Value>`, and every `serde_json::Value` has a
//! protobuf `Struct` representation, so there is no such thing as an
//! unconvertible caveat value to demonstrate -- the type system already closed
//! that hole at compile time. That is a real difference from Python, where the
//! same case is a runtime failure that had to be fixed to name its key, and it
//! is recorded rather than faked with a contrived value.
//!
//! Run with: `cargo run --example unrepresentable_values`

mod common;

use common::CheckBehavior;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{Filter, Relationship};

#[tokio::main]
async fn main() {
    // ── 1. Caller data: a filter the wire format cannot express ──────────
    //
    // A subject ID with no subject type is not a narrower filter -- the wire
    // format simply drops it, so the filter silently WIDENS. Applied to
    // delete_relationships that is the difference between deleting alice's
    // relationships and deleting every relationship on every document.
    let endpoint =
        std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string());
    let token =
        std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "somerandomkeyhere".to_string());
    let live = SpiceDBClient::new_plaintext(endpoint, token)
        .await
        .expect("connect to SpiceDB");

    // The filter is rejected before a request is built, so nothing reaches the
    // server: this is the caller-facing surface, not an internal helper.
    let err = live
        .delete_relationships(&Filter::new("document").with_subject_id("alice"))
        .await
        .expect_err("a filter whose subject constraint the wire cannot express must fail, not widen");
    // Typed AND client-side. Asserting only the variant would not discriminate:
    // a widened filter that reached the server comes back InvalidArgument too,
    // so this pins the client's own message -- the refusal that happens before
    // any request is built.
    assert!(
        matches!(err, SpiceDBError::InvalidArgument(_)),
        "the failure must be typed, got {err:?}"
    );
    assert!(
        format!("{err:?}").contains("subject_type"),
        "the failure must name the missing piece, and must come from this client rather than \
         from the server rejecting a widened filter: {err:?}"
    );
    println!("subject ID without subject type: refused rather than silently widened");

    // The same filter with the missing piece supplied converts fine, which is
    // what makes the check above a real constraint rather than a blanket ban.
    live.delete_relationships(
        &Filter::new("document")
            .with_subject_type("user")
            .with_subject_id("alice"),
    )
    .await
    .expect("a fully-specified subject filter must convert");
    println!("...and converts once subject_type is supplied");

    // ── 2. Server data: an enum this client has never seen ───────────────
    //
    // The opposite posture. This must not raise, and must not be a grant.
    let (addr, _) = common::spawn(CheckBehavior::UnknownPermissionship).await;
    let client = SpiceDBClient::new_plaintext(format!("{addr}"), "some-token")
        .await
        .expect("connect to the stand-in");

    let rel = Relationship::new("document", "readme", "view", "user", "alice", "")
        .expect("invalid relationship");
    let result = client
        .check_permission(&consistency::full(), "view", &rel)
        .await
        .expect("an unrecognised server enum must not raise -- that would turn a SpiceDB upgrade into a client-side outage");

    assert!(
        !result.has_permission(),
        "SECURITY: an unrecognised permissionship was treated as a grant"
    );
    println!("unknown server permissionship: no error, and not a grant");

    println!("unrepresentable_values: caller data fails loudly, server data degrades safely");
}
