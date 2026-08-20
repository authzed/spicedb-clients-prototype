//! Demonstrates the opt-in a plaintext connection to a remote host requires --
//! see root DESIGN.md, "RULE: Credentials over insecure transport require an
//! explicit opt-in".
//!
//! The failure this rule exists to prevent is mundane and common: a developer
//! copies a plaintext constructor out of a localhost example into a staging
//! config, and a long-lived SpiceDB token -- a complete authorization bypass in
//! anyone else's hands -- goes onto the wire in cleartext with nothing
//! signalling that it happened. So `.plaintext()` is loopback-only, and reaching
//! a remote host over plaintext takes a second, separately-named builder option
//! the caller cannot supply by accident:
//! `.allow_insecure_remote_credentials()`.
//!
//! The sharpest case is the last one. The rule requires the guard's answer to be
//! *the transport's* answer -- here `http::Uri`, what tonic parses with --
//! rather than a hand-rolled string split. Given `127.0.0.1:443@evil.com`, a
//! last-colon split reads the host as `127.0.0.1` and waves it through, while a
//! URI parser reads `127.0.0.1:443` as *userinfo* and the authority as
//! `evil.com`.
//!
//! Run with: `cargo run --example insecure_opt_in`

use spicedb::client::SpiceDBClient;
use spicedb::error::SpiceDBError;
use spicedb::types::Filter;

fn endpoint() -> String {
    std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string())
}

fn token() -> String {
    std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "somerandomkeyhere".to_string())
}

#[tokio::main]
async fn main() {
    // ── 1. Loopback plaintext needs no opt-in ────────────────────────────
    //
    // The case the rule deliberately leaves ergonomic: a token on a loopback
    // socket never leaves the machine, so requiring ceremony here would only
    // train developers to reach for the opt-in reflexively.
    let client = SpiceDBClient::new_plaintext(endpoint(), token())
        .await
        .expect("loopback plaintext must be allowed with no opt-in");

    // Prove the client is usable, not merely constructed.
    //
    // The schema below is narrower than the shared one every other example
    // writes, and they all run against the same SpiceDB. SpiceDB refuses a
    // WriteSchema that drops a relation while a relationship still exists under
    // it, so clear first -- an earlier example leaving document:firstdoc#editor
    // behind is enough to fail this outright, which is exactly how it failed the
    // first time this example ran in the full suite rather than alone.
    match client.delete_relationships(&Filter::new("document")).await {
        Ok(_) => {}
        // No `document` definition in the live schema yet: nothing to clear.
        // Only that error is tolerated -- an unreachable server or a bad token
        // must still fail this example, which is why this is not a blanket
        // `.ok()`.
        Err(SpiceDBError::FailedPrecondition(_)) => {}
        Err(e) => panic!("clearing document relationships failed: {e:?}"),
    }

    client
        .write_schema(
            "definition user {}\n\ndefinition document {\n\trelation viewer: user\n\tpermission view = viewer\n}",
        )
        .await
        .expect("loopback plaintext client should reach SpiceDB");
    println!("loopback plaintext: allowed with no opt-in, and works");

    // ── 2. Remote plaintext is refused ───────────────────────────────────
    //
    // The refusal happens while building, so the token never reaches a socket.
    // This is not about whether the host exists -- example.com is refused
    // because it is not loopback, full stop.
    let refused = SpiceDBClient::new_plaintext("example.com:50051", token()).await;
    assert!(
        refused.is_err(),
        "SECURITY: a bearer token was accepted for cleartext delivery to a non-loopback host"
    );
    println!("remote plaintext, no opt-in: refused");

    // ── 3. ...unless the caller says so, by name ─────────────────────────
    //
    // Two builder calls, not one, and that separation is the point: selecting
    // the plaintext transport and accepting the credential exposure that
    // follows are different decisions, and clause 1 forbids one flag from doing
    // both jobs.
    SpiceDBClient::builder("example.com:50051", token())
        .plaintext()
        .allow_insecure_remote_credentials()
        .build()
        .await
        .expect("the named opt-in should permit remote plaintext");
    println!("remote plaintext, explicit opt-in: allowed");

    // ── 4. Endpoints whose authority could move under URI parsing ────────
    //
    // Failing closed is what matters: the rule does not promise every client
    // agrees on loopback spellings, but it does require that anything the guard
    // calls loopback is somewhere the transport actually dials on loopback.
    for spoof in [
        "127.0.0.1:443@evil.com",
        "127.0.0.1:50051/../evil.com",
        "127.0.0.1:50051?x=evil.com",
        "127.0.0.1:50051#evil.com",
    ] {
        let result = SpiceDBClient::new_plaintext(spoof, token()).await;
        assert!(
            result.is_err(),
            "SECURITY: {spoof} was accepted as loopback -- the guard is splitting the string \
             instead of asking the transport's parser"
        );
        println!("authority-moving endpoint {spoof}: refused");
    }

    println!("insecure_opt_in: every case behaved as the rule requires");
}
