mod support;

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::Relationship;
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

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

/// Regression tests for root DESIGN.md, "RULE: Credentials over insecure
/// transport require an explicit opt-in".
///
/// The proto-layer test suite
/// (proto-clients/spicedb-rust-proto/tests/client_test.rs's
/// `insecure_host_guard` module) is what proves the token itself never
/// reaches the wire for a rejected combination -- via a real in-process gRPC
/// server and a structural proof that the guard runs before any
/// `tonic::transport::Endpoint`/TLS/channel work. These tests prove the
/// idiomatic builder actually reaches, and propagates options into, that
/// same guard, and that the loopback path still delivers a real request to a
/// real (loopback) server end-to-end.
mod insecure_host_guard {
    use super::*;

    #[tokio::test]
    async fn refuses_non_loopback_without_opt_in() {
        let result = SpiceDBClient::builder("evil.example.com:1234", "super-secret-token")
            .plaintext()
            .build()
            .await;

        match result {
            Err(SpiceDBError::InvalidArgument(msg)) => {
                assert!(msg.contains("evil.example.com:1234"), "{msg}");
                assert!(msg.contains("allow_insecure_remote_credentials"), "{msg}");
            }
            Err(other) => panic!("expected SpiceDBError::InvalidArgument, got {other:?}"),
            Ok(_) => panic!("expected construction to be refused for a non-loopback endpoint with no opt-in"),
        }
    }

    #[tokio::test]
    async fn loopback_allows_insecure_with_no_opt_in_and_actually_calls_the_server() {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
            checked_at: Some(proto::ZedToken {
                token: "rev-1".to_string(),
            }),
            pairs: vec![proto::CheckBulkPermissionsPair {
                request: None,
                response: Some(proto::check_bulk_permissions_pair::Response::Item(
                    proto::CheckBulkPermissionsResponseItem {
                        permissionship:
                            proto::check_permission_response::Permissionship::HasPermission as i32,
                        partial_caveat_info: None,
                        debug_trace: None,
                    },
                )),
            }],
        });
        let calls = mock.check_bulk_permissions_calls();
        let addr = spawn_permissions_server(mock).await;

        // addr is always 127.0.0.1:<port> -- real loopback -- so this needs
        // no allow_insecure_remote_credentials.
        let client = SpiceDBClient::builder(addr.to_string(), "test-token")
            .plaintext()
            .build()
            .await
            .expect("loopback endpoint must not require the opt-in");

        let rel = Relationship::new("document", "1", "view", "user", "alice", "")
            .expect("valid relationship");
        client
            .check_permissions(&consistency::full(), "view", &[rel])
            .await
            .expect("call against the real mock server must succeed");

        assert_eq!(
            calls.load(std::sync::atomic::Ordering::SeqCst),
            1,
            "the call must actually have reached the real (loopback) server"
        );
    }

    /// Proves the opt-in unlocks *construction* for a non-loopback endpoint.
    /// Uses a TEST-NET-1 address (RFC 5737, 192.0.2.0/24) -- reserved,
    /// guaranteed non-routable -- as the endpoint. The proto layer's
    /// `allow_insecure_remote_credentials_permits_non_loopback_construction`
    /// test is what proves this doesn't hang (tonic's insecure path uses
    /// `connect_lazy()`); this test proves the idiomatic builder's
    /// `allow_insecure_remote_credentials()` actually reaches that same
    /// underlying opt-in.
    #[tokio::test]
    async fn allow_insecure_remote_credentials_permits_non_loopback_construction() {
        let client = SpiceDBClient::builder("192.0.2.1:1234", "remote-token")
            .plaintext()
            .allow_insecure_remote_credentials()
            .build()
            .await;

        assert!(
            client.is_ok(),
            "allow_insecure_remote_credentials() must permit constructing a client for a \
             non-loopback endpoint: {:?}",
            client.err()
        );
    }
}
