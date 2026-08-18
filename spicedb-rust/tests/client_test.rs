use spicedb::client::SpiceDBClient;

#[tokio::test]
async fn test_new_plaintext_creates_client() {
    let client = SpiceDBClient::new_plaintext("localhost:50051", "testtoken").await;
    assert!(client.is_ok());
}

#[tokio::test]
async fn test_builder_plaintext() {
    let client = SpiceDBClient::builder("localhost:50051", "token")
        .plaintext()
        .build()
        .await;
    assert!(client.is_ok());
}

#[tokio::test]
async fn test_builder_accepts_owned_strings() {
    let endpoint = String::from("localhost:50051");
    let token = String::from("my-token");
    let client = SpiceDBClient::builder(endpoint, token)
        .plaintext()
        .build()
        .await;
    assert!(client.is_ok());
}

/// Completes a real TLS handshake against a live endpoint.
///
/// This is the only test that proves `new_system_tls` works. The two tests it
/// replaced asserted `is_err()` against an unreachable host, so they passed
/// identically whether the cause was DNS failure or — as was in fact the case — a
/// completely empty trust store, letting the client ship unable to reach any TLS
/// server at all.
///
/// A local self-signed server cannot substitute: the platform trust store under test
/// is precisely the thing that would reject it. So this needs the network, and is
/// gated to keep offline runs green. CI runs it in the `unit` job's "TLS handshake
/// test (requires network)" step in `.github/workflows/rust.yaml`, which sets the
/// variable and runs only this test.
///
/// No credentials are exercised — `connect()` performs TCP and TLS only, so the token
/// below is never validated and never sent.
#[tokio::test]
async fn test_system_tls_completes_real_handshake() {
    if std::env::var("SPICEDB_TLS_INTEGRATION").is_err() {
        eprintln!("skipping: set SPICEDB_TLS_INTEGRATION=1 to run the TLS handshake test");
        return;
    }

    let client = SpiceDBClient::new_system_tls("grpc.authzed.com:443", "not-a-real-token").await;

    assert!(
        client.is_ok(),
        "system TLS handshake failed — the platform trust store is probably not loaded: {:?}",
        client.err()
    );
}
