//! Demonstrates the two error codes a caller actually recovers from -- see root
//! DESIGN.md, "RULE: Error mapping must not lose the server's detail".
//!
//! The rule names both consequences, and this example is those two recoveries
//! written out as running code:
//!
//! - `OUT_OF_RANGE` is SpiceDB's signal that a ZedToken has expired or been
//!   garbage-collected. Recovery is mechanical: discard the stale token and
//!   re-read at full consistency. Collapsed into a generic error, every caller
//!   would have to string-match a message to recover something the client
//!   already knew the shape of.
//! - `UNAUTHENTICATED` is the most common error a new integration produces --
//!   a wrong, expired or rotated token. Distinguishing it is what lets a caller
//!   write "refresh credentials on auth failure, page someone on internal
//!   error".
//!
//! Neither code is reachable from the SpiceDB the integration job starts, which
//! was verified rather than assumed -- see `examples/common/mod.rs` -- so the
//! first two parts drive a stand-in. The third asserts what the real server
//! actually does with a bad preshared key, because that is the case a reader
//! hits first.
//!
//! Run with: `cargo run --example error_mapping`

mod common;

use common::{CheckBehavior, STALE_TOKEN};
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::Relationship;

fn doc() -> Relationship {
    Relationship::new("document", "readme", "view", "user", "alice", "")
        .expect("invalid relationship")
}

#[tokio::main]
async fn main() {
    // ── 1. OUT_OF_RANGE: discard the stale token, re-read at full ────────
    let (addr, _) = common::spawn(CheckBehavior::GrantUnlessStaleToken).await;
    let client = SpiceDBClient::new_plaintext(format!("{addr}"), "some-token")
        .await
        .expect("connect to the stand-in");

    let err = client
        .check_permission(&consistency::at_least(STALE_TOKEN), "view", &doc())
        .await
        .expect_err("a check pinned to a collected ZedToken must fail");
    assert!(
        matches!(err, SpiceDBError::OutOfRange(_)),
        "it must surface as OutOfRange, not a generic failure a caller has to string-match: {err:?}"
    );
    println!("stale ZedToken: SpiceDBError::OutOfRange");

    // The recovery the rule calls mechanical, in full: drop the token, re-read
    // at full consistency. Nothing here parses a message.
    let result = client
        .check_permission(&consistency::full(), "view", &doc())
        .await
        .expect("re-reading at full consistency is the documented recovery and must succeed");
    assert!(result.has_permission(), "the re-read should have granted");
    println!("recovery: discarded the token, re-read at full consistency, got an answer");

    // ── 2. UNAUTHENTICATED: refresh credentials, do not page anyone ──────
    let (auth_addr, _) = common::spawn(CheckBehavior::Unauthenticated).await;
    let auth_client = SpiceDBClient::new_plaintext(format!("{auth_addr}"), "rotated-token")
        .await
        .expect("connect to the stand-in");

    let err = auth_client
        .check_permission(&consistency::full(), "view", &doc())
        .await
        .expect_err("a rejected token must fail");
    assert!(
        matches!(err, SpiceDBError::Unauthenticated(_)),
        "a rejected token must be distinguishable from an internal fault: {err:?}"
    );
    // Asserting the negative is the half that would silently rot if every code
    // collapsed into one variant.
    assert!(
        !matches!(err, SpiceDBError::Unavailable(_)),
        "an auth failure must not also be an unavailable error"
    );
    println!("rotated token: SpiceDBError::Unauthenticated, distinct from a transport fault");

    // ── 3. What the real SpiceDB actually does with a bad preshared key ──
    //
    // PERMISSION_DENIED, not UNAUTHENTICATED. Recorded because assuming
    // otherwise is how a credential-refresh branch ends up unreachable.
    let endpoint =
        std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string());
    let bad = SpiceDBClient::new_plaintext(endpoint, "definitely-the-wrong-key")
        .await
        .expect("connect to SpiceDB");
    let err = bad
        .read_schema()
        .await
        .expect_err("a wrong preshared key must fail");
    assert!(
        matches!(err, SpiceDBError::PermissionDenied(_)),
        "SpiceDB rejects a bad preshared key with PERMISSION_DENIED; if this now reports \
         something else, this example's guidance is stale and must be updated: {err:?}"
    );
    println!("real SpiceDB, wrong preshared key: PermissionDenied (not Unauthenticated)");

    println!("error_mapping: both recoveries work without parsing a message");
}
