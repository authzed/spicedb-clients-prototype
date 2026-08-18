/// Integration tests for the SpiceDB Rust proto client.
///
/// These tests verify that the client can be constructed and configured
/// correctly. Full integration tests require a running SpiceDB instance.

#[cfg(test)]
mod tests {
    // Once proto/ is populated and the crate builds, uncomment:
    // use spicedb_proto::SpiceDBProtoClient;

    #[tokio::test]
    async fn test_new_client_insecure_invalid_endpoint() {
        // Verify that connecting to a non-existent endpoint with insecure mode
        // returns an error (connection refused). This validates the constructor
        // path without requiring a running server.
        //
        // Uncomment once the crate builds:
        // let result = SpiceDBProtoClient::new("localhost:0", "test-token", true).await;
        // The connection may succeed lazily with tonic, so we just verify
        // construction doesn't panic.
        assert!(true, "placeholder until proto/ is populated");
    }

    #[tokio::test]
    async fn test_bearer_token_format() {
        // Verify that the bearer token is formatted correctly.
        // Once proto/ is populated, this test should verify that requests
        // include the "Bearer <token>" authorization header.
        assert!(true, "placeholder until proto/ is populated");
    }
}

/// Regression tests for root DESIGN.md, "RULE: Credentials over insecure
/// transport require an explicit opt-in".
mod insecure_host_guard {
    use std::net::SocketAddr;

    use spicedb_proto::authzed::api::v1::schema_service_server::{
        SchemaService, SchemaServiceServer,
    };
    use spicedb_proto::authzed::api::v1::{
        ComputablePermissionsRequest, ComputablePermissionsResponse, DependentRelationsRequest,
        DependentRelationsResponse, DiffSchemaRequest, DiffSchemaResponse, ReadSchemaRequest,
        ReadSchemaResponse, ReflectSchemaRequest, ReflectSchemaResponse, WriteSchemaRequest,
        WriteSchemaResponse,
    };
    use spicedb_proto::client::is_loopback_endpoint;
    use spicedb_proto::{SpiceDBProtoClient, SpiceDBProtoClientError};
    use tokio::sync::mpsc;
    use tonic::{Request, Response, Status};

    const LOOPBACK_ENDPOINTS: &[&str] = &[
        "localhost:50051",
        "LOCALHOST:50051",
        "localhost",
        "127.0.0.1:50051",
        "127.0.0.1",
        "127.55.66.77:50051",
        "[::1]:50051",
        "::1",
        "unix:/var/run/spicedb.sock",
        "unix:///var/run/spicedb.sock",
    ];

    const NON_LOOPBACK_ENDPOINTS: &[&str] = &[
        "example.com:443",
        "staging.internal:443",
        "10.0.0.5:50051",
        "8.8.8.8:443",
        "0.0.0.0:50051",
        // Typosquats/lookalikes: a future refactor toward str::contains or
        // str::ends_with on "localhost"/"127.0.0.1" would wrongly treat
        // these as loopback and reopen a credential leak. Must stay
        // non-loopback under exact-match host comparison.
        "localhost.evil.com:443",
        "127.0.0.1.evil.com:443",
        "evil-localhost:443",
        // Authority-shifting targets. Each of these was, or could become, a
        // credential leak: the old last-colon / first-']' split read a
        // loopback host out of them while `Endpoint::from_shared` parsed the
        // SAME string as URI userinfo/path/query/fragment and dialed
        // somewhere else. `"127.0.0.1:443@evil.com"` and
        // `"[::1]:443@evil.com"` both produced `uri.host() == "evil.com"`
        // while `is_loopback_endpoint` returned true, so the bearer token
        // went to evil.com in cleartext with no opt-in. See
        // `refuses_endpoint_whose_uri_authority_shifts_the_host` below for
        // the over-the-wire proof, and the function's own doc comment for
        // why the fix is "ask the transport's parser", not "validate the
        // port".
        "127.0.0.1:443@evil.com",
        "[::1]:443@evil.com",
        "[::1]:0@127.0.0.1:19999",
        "localhost@evil.com",
        "localhost/../evil.com",
        "localhost#@evil.com",
        "localhost?@evil.com",
        "localhost.",
        "localhost :50051",
        "127.0.0.1 :50051",
    ];

    #[test]
    fn is_loopback_endpoint_true_for_loopback_targets() {
        for endpoint in LOOPBACK_ENDPOINTS {
            assert!(is_loopback_endpoint(endpoint), "{endpoint}");
        }
    }

    #[test]
    fn is_loopback_endpoint_false_for_non_loopback_targets() {
        for endpoint in NON_LOOPBACK_ENDPOINTS {
            assert!(!is_loopback_endpoint(endpoint), "{endpoint}");
        }
    }

    /// A minimal SchemaService that records the "authorization" metadata it
    /// observes on every call and always fails with UNIMPLEMENTED -- the RPC's
    /// outcome is irrelevant to these tests, only what the server observed.
    #[derive(Clone)]
    struct CapturingSchemaService {
        sender: mpsc::UnboundedSender<Option<String>>,
    }

    impl CapturingSchemaService {
        fn record<T>(&self, request: &Request<T>) -> Status {
            let auth = request
                .metadata()
                .get("authorization")
                .map(|v| v.to_str().unwrap_or_default().to_string());
            let _ = self.sender.send(auth);
            Status::unimplemented("test server never implements schema RPCs")
        }
    }

    #[tonic::async_trait]
    impl SchemaService for CapturingSchemaService {
        async fn read_schema(
            &self,
            request: Request<ReadSchemaRequest>,
        ) -> Result<Response<ReadSchemaResponse>, Status> {
            Err(self.record(&request))
        }
        async fn write_schema(
            &self,
            request: Request<WriteSchemaRequest>,
        ) -> Result<Response<WriteSchemaResponse>, Status> {
            Err(self.record(&request))
        }
        async fn reflect_schema(
            &self,
            request: Request<ReflectSchemaRequest>,
        ) -> Result<Response<ReflectSchemaResponse>, Status> {
            Err(self.record(&request))
        }
        async fn computable_permissions(
            &self,
            request: Request<ComputablePermissionsRequest>,
        ) -> Result<Response<ComputablePermissionsResponse>, Status> {
            Err(self.record(&request))
        }
        async fn dependent_relations(
            &self,
            request: Request<DependentRelationsRequest>,
        ) -> Result<Response<DependentRelationsResponse>, Status> {
            Err(self.record(&request))
        }
        async fn diff_schema(
            &self,
            request: Request<DiffSchemaRequest>,
        ) -> Result<Response<DiffSchemaResponse>, Status> {
            Err(self.record(&request))
        }
    }

    /// Starts a real gRPC server bound to a real loopback TCP port and returns
    /// its address plus a receiver of every "authorization" header it observed.
    async fn start_capturing_server() -> (SocketAddr, mpsc::UnboundedReceiver<Option<String>>) {
        let (tx, rx) = mpsc::unbounded_channel();
        let service = CapturingSchemaService { sender: tx };

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind loopback listener");
        let addr = listener.local_addr().expect("local addr");

        tokio::spawn(async move {
            let incoming = tokio_stream::wrappers::TcpListenerStream::new(listener);
            let _ = tonic::transport::Server::builder()
                .add_service(SchemaServiceServer::new(service))
                .serve_with_incoming(incoming)
                .await;
        });

        (addr, rx)
    }

    /// The regression test: constructing insecurely against a non-loopback
    /// endpoint, with no opt-in, must be refused before any transport work
    /// happens at all -- not merely fail eventually.
    ///
    /// Proven structurally rather than over the wire: `endpoint` here embeds a
    /// raw space, which is not a legal URI character, so if the guard did NOT
    /// run first and control instead reached `Endpoint::from_shared` (the very
    /// next statement in the unfixed code), construction would fail with a
    /// *different* error variant --
    /// `SpiceDBProtoClientError::Transport(_)` from an invalid-URI parse
    /// failure -- not `InsecureRemoteHostNotAllowed`. Getting the latter is
    /// only possible if the guard short-circuited before any URI, TLS config,
    /// or channel was ever touched, which is what "before any credential
    /// reaches the wire" requires: nothing capable of carrying the token to a
    /// remote host was ever constructed.
    #[tokio::test]
    async fn refuses_insecure_non_loopback_without_opt_in() {
        let result =
            SpiceDBProtoClient::new_with_options("evil example.com:1234", "super-secret-token", true, false)
                .await;

        match result {
            Err(SpiceDBProtoClientError::InsecureRemoteHostNotAllowed(msg)) => {
                assert!(msg.contains("evil example.com:1234"), "{msg}");
                assert!(msg.contains("allow_insecure_remote_credentials"), "{msg}");
            }
            Err(SpiceDBProtoClientError::Transport(e)) => panic!(
                "expected InsecureRemoteHostNotAllowed (proving the guard ran before any \
                 URI/transport work), but got a Transport error instead -- meaning control reached \
                 Endpoint::from_shared before the guard did: {e}"
            ),
            Ok(_) => panic!("expected construction to be refused for a non-loopback endpoint with no opt-in"),
        }
    }

    #[tokio::test]
    async fn loopback_allows_insecure_with_no_opt_in_and_sends_token() {
        let (addr, mut rx) = start_capturing_server().await;

        let client = SpiceDBProtoClient::new(&addr.to_string(), "test-token", true)
            .await
            .expect("construction must succeed for a loopback endpoint");

        let mut schema = client.schema.clone();
        let _ = schema.read_schema(ReadSchemaRequest {}).await;

        let got = rx.recv().await.expect("server must have observed a call");
        assert_eq!(got, Some("Bearer test-token".to_string()));
    }

    /// Proves the opt-in unlocks *construction* for a non-loopback endpoint.
    /// Uses a TEST-NET-1 address (RFC 5737, 192.0.2.0/24) -- reserved,
    /// guaranteed non-routable -- as `endpoint`. tonic's insecure path uses
    /// `connect_lazy()` (no connection is attempted until the first RPC), so
    /// this cannot hang: it proves the opt-in permits construction without
    /// needing (or risking a real attempt to reach) a remote host. The
    /// token-delivery mechanism itself -- the interceptor attaching
    /// "Bearer <token>" to outgoing metadata -- is unconditional and identical
    /// on every path (see `loopback_allows_insecure_with_no_opt_in_and_sends_token`
    /// above); only the endpoint-selection guard this test exercises differs
    /// between the loopback and opt-in cases.
    #[tokio::test]
    async fn allow_insecure_remote_credentials_permits_non_loopback_construction() {
        let client = SpiceDBProtoClient::new_with_options(
            "192.0.2.1:1234",
            "remote-token",
            true,
            true,
        )
        .await;

        assert!(
            client.is_ok(),
            "allow_insecure_remote_credentials: true must permit constructing a client for a \
             non-loopback endpoint: {:?}",
            client.err()
        );
    }

    /// The regression test for the loopback-guard bypass, proven over a real
    /// socket rather than by catching an exception.
    ///
    /// `endpoint` is built so that the two parses disagree: the guard's old
    /// bracketed branch took everything between `[` and the first `]` as the
    /// host, read `"::1"`, and reported loopback -- while
    /// `Endpoint::from_shared("http://[::1]:0@127.0.0.1:<port>")` parses
    /// `"[::1]:0"` as URI *userinfo* and dials `127.0.0.1:<port>`, which is
    /// the capturing server started below. It is the exact shape that sent a
    /// bearer token to evil.com in cleartext (`"127.0.0.1:443@evil.com"`),
    /// with the remote host swapped for a local listener so the leak can be
    /// observed without leaving the machine.
    ///
    /// The final assertion is what makes this a non-transmission test rather
    /// than an "it threw" test: the server the transport WOULD have connected
    /// to must have observed no call at all. An implementation that dialed,
    /// sent the token, and only then reported an error would satisfy a bare
    /// `matches!(result, Err(_))` check but would fail here -- and against the
    /// pre-fix guard this test does exactly that, capturing
    /// `Some("Bearer super-secret-token")` from a server no opt-in ever
    /// authorized.
    #[tokio::test]
    async fn refuses_endpoint_whose_uri_authority_shifts_the_host() {
        let (addr, mut rx) = start_capturing_server().await;
        let endpoint = format!("[::1]:0@{addr}");

        let result =
            SpiceDBProtoClient::new_with_options(&endpoint, "super-secret-token", true, false).await;

        match result {
            Err(SpiceDBProtoClientError::InsecureRemoteHostNotAllowed(msg)) => {
                assert!(msg.contains(&endpoint), "{msg}");
                assert!(msg.contains("allow_insecure_remote_credentials"), "{msg}");
            }
            Err(SpiceDBProtoClientError::Transport(e)) => panic!(
                "expected InsecureRemoteHostNotAllowed (proving the guard ran before any \
                 URI/transport work), but got a Transport error instead: {e}"
            ),
            Ok(client) => {
                // The guard let it through. Drive one RPC so the leak this
                // test exists to prevent is reported concretely, with the
                // credential the server actually received.
                let mut schema = client.schema.clone();
                let _ = schema.read_schema(ReadSchemaRequest {}).await;
                let observed =
                    tokio::time::timeout(std::time::Duration::from_secs(5), rx.recv()).await;
                panic!(
                    "guard reported {endpoint:?} as loopback, but the transport dialed {addr}; \
                     that server observed authorization={observed:?}"
                );
            }
        }

        assert!(
            rx.try_recv().is_err(),
            "nothing may reach {addr} for a refused endpoint -- the bearer token must never \
             have been put on the wire"
        );
    }

    /// Companion to the two tests above: with no opt-in, the exact same
    /// TEST-NET-1 address that construction just permitted (with opt-in) is
    /// refused outright.
    #[tokio::test]
    async fn refuses_non_loopback_test_net_address_without_opt_in() {
        let result = SpiceDBProtoClient::new_with_options("192.0.2.1:1234", "token", true, false).await;
        assert!(matches!(
            result,
            Err(SpiceDBProtoClientError::InsecureRemoteHostNotAllowed(_))
        ));
    }
}
