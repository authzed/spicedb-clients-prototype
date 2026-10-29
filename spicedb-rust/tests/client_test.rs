mod support;

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb_proto::authzed::api::v1 as proto;
use std::error::Error;

use support::{spawn_permissions_server, MockPermissionsService};

/// A connection failure must keep a walkable `source()` chain down to the
/// underlying I/O error. This is the class of error where the cause is most
/// diagnostic, and it used to be the only one in the hierarchy with no chain at
/// all -- the transport error was stringified and dropped. See root DESIGN.md,
/// "RULE: Error mapping must not lose the server's detail".
///
/// Port 1 on loopback cannot be bound without privileges, so the connect is
/// refused deterministically and no network is involved. The TLS constructor is
/// used because it connects eagerly; the plaintext one connects lazily and so
/// cannot surface a transport failure at construction time.
#[tokio::test]
async fn transport_failure_keeps_a_source_chain_to_the_io_error() {
    let err = match SpiceDBClient::new_system_tls("127.0.0.1:1", "testtoken").await {
        Err(err) => err,
        // SpiceDBClient is not Debug, so this cannot use expect_err.
        Ok(_) => panic!("connecting to a closed loopback port must fail"),
    };

    assert!(
        matches!(err, SpiceDBError::Transport(_)),
        "expected SpiceDBError::Transport, got {err:?}"
    );

    let mut source = Error::source(&err);
    let mut found_refused = false;
    let mut depth = 0;
    while let Some(current) = source {
        depth += 1;
        if let Some(io) = current.downcast_ref::<std::io::Error>() {
            if io.kind() == std::io::ErrorKind::ConnectionRefused {
                found_refused = true;
            }
        }
        source = current.source();
    }

    assert!(
        depth > 0,
        "SpiceDBError::Transport must expose a source; it exposed none"
    );
    assert!(
        found_refused,
        "expected ConnectionRefused somewhere in the source chain (walked {depth} levels)"
    );
}

#[tokio::test]
async fn test_new_plaintext_creates_client() {
    let client = SpiceDBClient::new_plaintext(
        "localhost:50051",
        "testtoken",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await;
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

    // An https:// URL (default port 443) selects the same TLS transport
    // through the plain builder.
    let client = SpiceDBClient::builder("https://grpc.authzed.com", "not-a-real-token")
        .build()
        .await;
    assert!(
        client.is_ok(),
        "https:// endpoint must complete a TLS handshake: {:?}",
        client.err()
    );
}

/// Endpoint forms deployments use. `http://` and `https://` URLs are accepted
/// and select the transport (agreeing with the constructor), the loopback
/// check runs on the URL's host, and any other form fails at construction
/// rather than building a client whose every call fails.
mod endpoint_forms {
    use super::*;
    use spicedb::{ConnectMode, RemoteCredentialsPolicy};

    fn ok_bulk_response() -> proto::CheckBulkPermissionsResponse {
        proto::CheckBulkPermissionsResponse {
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
        }
    }

    async fn one_check(client: &SpiceDBClient) -> Result<(), SpiceDBError> {
        let rel = spicedb::Relationship::from_tuple("document:1#view@user:alice")?;
        let check = spicedb::PermissionCheck::new(
            rel.resource().clone(),
            "view",
            rel.subject().clone(),
            consistency::full(),
        );
        client
            .check_bulk(spicedb::BulkCheckRequest::new(vec![check]))
            .await
            .map(|_| ())
    }

    /// `http://127.0.0.1:<port>` connects as loopback plaintext, through the
    /// plain builder (the scheme selects plaintext), `.plaintext()` and
    /// `new_plaintext`, with no opt-in, and the call reaches the server.
    #[tokio::test]
    async fn loopback_http_url_connects_as_plaintext() {
        for constructor in ["builder", "builder.plaintext", "new_plaintext"] {
            let mock = MockPermissionsService::new();
            mock.push_check_bulk_permissions_response(ok_bulk_response());
            let calls = mock.check_bulk_permissions_calls();
            let addr = spawn_permissions_server(mock).await;
            let endpoint = format!("http://{addr}");
            let client = match constructor {
                "builder" => SpiceDBClient::builder(&endpoint, "t").build().await,
                "builder.plaintext" => {
                    SpiceDBClient::builder(&endpoint, "t")
                        .plaintext()
                        .build()
                        .await
                }
                _ => {
                    SpiceDBClient::new_plaintext(
                        &endpoint,
                        "t",
                        RemoteCredentialsPolicy::LoopbackOnly,
                    )
                    .await
                }
            }
            .unwrap_or_else(|e| panic!("{constructor} {endpoint}: {e:?}"));
            one_check(&client)
                .await
                .unwrap_or_else(|e| panic!("{constructor} {endpoint}: {e:?}"));
            assert_eq!(calls.load(std::sync::atomic::Ordering::SeqCst), 1);
        }
    }

    /// A remote `http://` host is refused at construction under the default
    /// policy (not reported as "dns error" on every call), and allowed with
    /// the explicit opt-in.
    #[tokio::test]
    async fn remote_http_url_is_refused_without_the_opt_in() {
        for result in [
            SpiceDBClient::builder("http://spicedb:50051", "t")
                .build()
                .await,
            SpiceDBClient::new_plaintext(
                "http://spicedb:50051",
                "t",
                RemoteCredentialsPolicy::LoopbackOnly,
            )
            .await,
        ] {
            match result {
                Err(SpiceDBError::InvalidArgument(msg)) => {
                    assert!(msg.contains("AllowInsecureRemote"), "{msg}")
                }
                Err(other) => panic!("expected InvalidArgument, got {other:?}"),
                Ok(_) => panic!("a remote http:// endpoint must be refused"),
            }
        }
        let allowed = SpiceDBClient::builder("http://192.0.2.1:1234", "t")
            .remote_credentials_policy(RemoteCredentialsPolicy::AllowInsecureRemote)
            .build()
            .await;
        assert!(allowed.is_ok(), "{:?}", allowed.err());
    }

    /// An `https://` URL uses TLS: against a plaintext server the TLS
    /// handshake fails at construction and no call reaches it.
    #[tokio::test]
    async fn https_url_uses_tls() {
        let mock = MockPermissionsService::new();
        let calls = mock.check_bulk_permissions_calls();
        let addr = spawn_permissions_server(mock).await;
        let endpoint = format!("https://{addr}");
        match SpiceDBClient::builder(&endpoint, "t").build().await {
            Err(err @ SpiceDBError::Transport(_)) => {
                // The TLS mismatch is named, with the endpoint, not as a
                // bare "transport error".
                let shown = err.to_string();
                assert!(shown.contains(&endpoint), "{shown}");
                assert!(
                    shown.len() > format!("transport error: connecting to {endpoint}: ").len(),
                    "{shown}"
                );
            }
            Err(other) => panic!("expected a TLS transport failure, got {other:?}"),
            Ok(_) => panic!("a TLS client must not connect to a plaintext server"),
        }
        assert_eq!(calls.load(std::sync::atomic::Ordering::SeqCst), 0);
    }

    /// A scheme that disagrees with the constructor, an unsupported scheme, or
    /// an unparseable endpoint fails at construction with `InvalidArgument`
    /// naming the expected format.
    #[tokio::test]
    async fn other_forms_fail_at_construction_naming_the_format() {
        let results = [
            SpiceDBClient::new_system_tls("http://127.0.0.1:50051", "t").await,
            SpiceDBClient::builder("https://127.0.0.1:50051", "t")
                .plaintext()
                .build()
                .await,
            SpiceDBClient::new_plaintext(
                "https://127.0.0.1:50051",
                "t",
                RemoteCredentialsPolicy::LoopbackOnly,
            )
            .await,
            SpiceDBClient::builder("grpc://127.0.0.1:50051", "t")
                .build()
                .await,
            SpiceDBClient::builder("https://spicedb.example.com/api", "t")
                .build()
                .await,
            SpiceDBClient::builder("spicedb.example.com:http", "t")
                .build()
                .await,
        ];
        for result in results {
            match result {
                Err(SpiceDBError::InvalidArgument(msg)) => {
                    assert!(msg.contains("host:port"), "{msg}");
                    assert!(msg.contains("http://"), "{msg}");
                }
                Err(other) => panic!("expected InvalidArgument, got {other:?}"),
                Ok(_) => panic!("expected the endpoint to be refused at construction"),
            }
        }
    }

    /// `host:port` still works for both transports.
    #[tokio::test]
    async fn host_port_still_works() {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(ok_bulk_response());
        let addr = spawn_permissions_server(mock).await;
        let client = SpiceDBClient::builder(addr.to_string(), "t")
            .plaintext()
            .build()
            .await
            .expect("host:port plaintext");
        one_check(&client).await.expect("call reaches the server");
        // TLS host:port is still dialed as TLS (here: handshake failure).
        assert!(matches!(
            SpiceDBClient::new_system_tls(addr.to_string(), "t").await,
            Err(SpiceDBError::Transport(_))
        ));
    }

    /// Plaintext connects lazily by default, so a wrong or down SpiceDB
    /// passes construction; `ConnectMode::Eager` fails it at startup with
    /// `Transport`, and succeeds when the server is up.
    #[tokio::test]
    async fn eager_connect_fails_at_startup_for_an_unreachable_server() {
        let lazy = SpiceDBClient::builder("http://127.0.0.1:1", "t")
            .build()
            .await;
        assert!(lazy.is_ok(), "{:?}", lazy.err());

        match SpiceDBClient::builder("http://127.0.0.1:1", "t")
            .connect_mode(ConnectMode::Eager)
            .build()
            .await
        {
            Err(SpiceDBError::Transport(_)) => {}
            Err(other) => panic!("expected Transport, got {other:?}"),
            Ok(_) => panic!("an eager connect to a closed port must fail"),
        }

        let addr = spawn_permissions_server(MockPermissionsService::new()).await;
        let eager = SpiceDBClient::builder(addr.to_string(), "t")
            .plaintext()
            .connect_mode(ConnectMode::Eager)
            .build()
            .await;
        assert!(eager.is_ok(), "{:?}", eager.err());

        // A lazy TLS client also constructs without a server.
        let lazy_tls = SpiceDBClient::builder("127.0.0.1:1", "t")
            .connect_mode(ConnectMode::Lazy)
            .build()
            .await;
        assert!(lazy_tls.is_ok(), "{:?}", lazy_tls.err());
    }

    /// Under the default loopback-only policy, a malformed URL is reported as
    /// what is wrong with it, not as a remote-plaintext refusal; with the
    /// opt-in, a path or query is not reported as a port problem.
    #[tokio::test]
    async fn malformed_endpoints_name_the_actual_problem() {
        for (endpoint, problem) in [
            ("http://127.0.0.1:50066/x", "path"),
            ("http://127.0.0.1:50066?x=1", "query"),
            ("http://127.0.0.1:50066#x", "fragment"),
            ("http://127.0.0.1:50066@evil.com", "userinfo"),
            ("http://127.0.0.1:nope", "port"),
            ("http://127.0.0.1", "http://host:50051"),
            ("ftp://127.0.0.1:50066", "scheme"),
        ] {
            for policy in [
                RemoteCredentialsPolicy::LoopbackOnly,
                RemoteCredentialsPolicy::AllowInsecureRemote,
            ] {
                let result = SpiceDBClient::builder(endpoint, "t")
                    .remote_credentials_policy(policy)
                    .build()
                    .await;
                match result {
                    Err(SpiceDBError::InvalidArgument(msg)) => {
                        assert!(msg.contains(problem), "{endpoint} {policy:?}: {msg}");
                        assert!(!msg.contains("AllowInsecureRemote"), "{msg}");
                    }
                    Err(other) => panic!("{endpoint} {policy:?}: {other:?}"),
                    Ok(_) => panic!("{endpoint} {policy:?} must be refused"),
                }
            }
        }
    }

    /// An `http://` URL must name its port; `https://` defaults to 443.
    #[tokio::test]
    async fn scheme_url_ports() {
        match SpiceDBClient::builder("http://spicedb", "t")
            .remote_credentials_policy(RemoteCredentialsPolicy::AllowInsecureRemote)
            .build()
            .await
        {
            Err(SpiceDBError::InvalidArgument(msg)) => {
                assert!(msg.contains("http://host:50051"), "{msg}")
            }
            Err(other) => panic!("{other:?}"),
            Ok(_) => panic!("http:// without a port must be refused"),
        }
        let tls = SpiceDBClient::builder("https://spicedb.example.com", "t")
            .connect_mode(ConnectMode::Lazy)
            .build()
            .await;
        assert!(tls.is_ok(), "{:?}", tls.err());
    }

    /// A connection failure displays the endpoint and its cause (refused,
    /// DNS), and `source()` still walks to the underlying error.
    #[tokio::test]
    async fn connection_errors_name_the_endpoint_and_cause() {
        for (endpoint, cause) in [
            ("http://127.0.0.1:1", "refused"),
            ("http://spicedb.invalid:50051", "dns"),
        ] {
            let err = match SpiceDBClient::builder(endpoint, "t")
                .remote_credentials_policy(RemoteCredentialsPolicy::AllowInsecureRemote)
                .connect_mode(ConnectMode::Eager)
                .build()
                .await
            {
                Err(err @ SpiceDBError::Transport(_)) => err,
                Err(other) => panic!("expected Transport for {endpoint}, got {other:?}"),
                Ok(_) => panic!("{endpoint} must fail to connect"),
            };
            let shown = format!("{err}");
            assert!(
                shown.starts_with(&format!("transport error: connecting to {endpoint}: ")),
                "{shown}"
            );
            assert!(shown.to_lowercase().contains(cause), "{shown}");
            assert!(Error::source(&err).is_some(), "source chain kept");
        }
    }
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
                assert!(msg.contains("remote plaintext"), "{msg}");
                assert!(msg.contains("AllowInsecureRemote"), "{msg}");
            }
            Err(other) => panic!("expected SpiceDBError::InvalidArgument, got {other:?}"),
            Ok(_) => panic!(
                "expected construction to be refused for a non-loopback endpoint with no opt-in"
            ),
        }
    }

    /// The bypass shapes, at the public entry point. The proto tier holds the
    /// full fixture set and the over-the-wire "token never sent" proof; these
    /// are the ones that must not slip through `new_plaintext` itself, which
    /// uses the explicit LoopbackOnly policy.
    #[tokio::test]
    async fn new_plaintext_refuses_endpoints_whose_uri_authority_shifts_the_host() {
        for endpoint in [
            "127.0.0.1:443@evil.com",
            "[::1]:443@evil.com",
            "[::1]:0@127.0.0.1:19999",
            "localhost@evil.com",
        ] {
            match SpiceDBClient::new_plaintext(
                endpoint,
                "super-secret-token",
                spicedb::RemoteCredentialsPolicy::LoopbackOnly,
            )
            .await
            {
                // Refused as malformed (userinfo), before the credentials
                // policy is consulted, so the message names the real problem.
                Err(SpiceDBError::InvalidArgument(msg)) => {
                    assert!(msg.contains("userinfo"), "{msg}");
                    assert!(!msg.contains("AllowInsecureRemote"), "{msg}");
                }
                Err(other) => panic!("expected InvalidArgument for {endpoint:?}, got {other:?}"),
                Ok(_) => panic!("expected {endpoint:?} to be refused"),
            }
        }
    }

    /// Bare `::1` must produce a working client through the public entry
    /// point, not merely satisfy the guard: it is item 8 of the loopback
    /// contract. It is not a legal URI authority, so it has to be bracketed
    /// when the transport address is built, and it previously was not.
    #[tokio::test]
    async fn new_plaintext_accepts_bare_ipv6_loopback() {
        for endpoint in ["::1", "[::1]", "[::1]:50051"] {
            let result = SpiceDBClient::new_plaintext(
                endpoint,
                "testtoken",
                spicedb::RemoteCredentialsPolicy::LoopbackOnly,
            )
            .await;
            assert!(
                result.is_ok(),
                "bare IPv6 loopback {endpoint:?} must construct a client, got {:?}",
                result.err()
            );
        }
    }

    /// A multi-byte character must not panic out of the constructor.
    #[tokio::test]
    async fn new_plaintext_returns_err_for_multibyte_endpoint() {
        match SpiceDBClient::new_plaintext(
            "abcdé.example.com:443",
            "testtoken",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        {
            Err(SpiceDBError::InvalidArgument(_)) => {}
            Err(other) => panic!("expected InvalidArgument, got {other:?}"),
            Ok(_) => panic!("expected a multi-byte non-loopback endpoint to be refused"),
        }
    }

    /// A token with a trailing newline -- the shape you get from reading a
    /// secret out of a file or a mounted k8s secret -- must return `Err`
    /// through the public entry point, not panic out of it.
    #[tokio::test]
    async fn new_plaintext_returns_err_for_a_token_that_cannot_be_a_header() {
        match SpiceDBClient::new_plaintext(
            "localhost:50051",
            "secret-from-file\n",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        {
            Err(SpiceDBError::InvalidArgument(msg)) => {
                assert!(msg.contains("HTTP header"), "{msg}");
            }
            Err(other) => panic!("expected InvalidArgument, got {other:?}"),
            Ok(_) => panic!("expected a token with a trailing newline to be refused"),
        }
    }

    /// A unix-socket endpoint is refused, not silently turned into a DNS
    /// lookup for the hostname `unix`. tonic cannot dial a UDS path from a URI.
    #[tokio::test]
    async fn new_plaintext_refuses_unix_socket_targets() {
        for endpoint in ["unix:/var/run/spicedb.sock", "unix:///var/run/spicedb.sock"] {
            match SpiceDBClient::new_plaintext(
                endpoint,
                "super-secret-token",
                spicedb::RemoteCredentialsPolicy::LoopbackOnly,
            )
            .await
            {
                Err(SpiceDBError::InvalidArgument(msg)) => {
                    assert!(msg.contains("unix-domain-socket"), "{msg}");
                }
                Err(other) => panic!("expected InvalidArgument for {endpoint:?}, got {other:?}"),
                Ok(_) => panic!("expected {endpoint:?} to be refused"),
            }
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

        let rel = spicedb::Relationship::from_tuple("document:1#view@user:alice")
            .expect("valid relationship");
        client
            .check_bulk(spicedb::BulkCheckRequest::new(
                [rel]
                    .iter()
                    .map(|r| {
                        spicedb::PermissionCheck::new(
                            (r).resource().clone(),
                            "view",
                            (r).subject().clone(),
                            (consistency::full()).clone(),
                        )
                    })
                    .collect(),
            ))
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
            .remote_credentials_policy(spicedb::RemoteCredentialsPolicy::AllowInsecureRemote)
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
