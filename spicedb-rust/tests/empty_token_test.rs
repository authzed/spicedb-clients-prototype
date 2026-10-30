//! An empty or whitespace-only token is a local validation error at
//! construction, never a bare `Bearer` header the server rejects on the first
//! call. See root DESIGN.md, "Reject unusable credentials and trust material
//! at construction".

use spicedb::client::SpiceDBClient;
use spicedb::error::SpiceDBError;
use spicedb::RemoteCredentialsPolicy;

fn assert_blank_token_error(result: Result<SpiceDBClient, SpiceDBError>) {
    match result {
        Err(SpiceDBError::InvalidArgument(detail)) => {
            let message = detail.to_string();
            assert!(
                message.contains("token: must not be empty or whitespace"),
                "{message}"
            );
        }
        Err(other) => panic!("expected InvalidArgument, got {other:?}"),
        Ok(_) => panic!("a blank token must be refused at construction"),
    }
}

#[tokio::test]
async fn blank_token_is_refused_by_every_constructor() {
    for token in ["", " ", "\t\n"] {
        assert_blank_token_error(
            SpiceDBClient::new_plaintext(
                "localhost:50116",
                token,
                RemoteCredentialsPolicy::LoopbackOnly,
            )
            .await,
        );
        // TLS connects eagerly, so this also proves the refusal precedes any dial.
        assert_blank_token_error(SpiceDBClient::new_system_tls("localhost:50116", token).await);
        assert_blank_token_error(
            SpiceDBClient::builder("http://localhost:50116", token)
                .build()
                .await,
        );
        let channel =
            tonic::transport::Endpoint::from_static("http://localhost:50116").connect_lazy();
        assert_blank_token_error(SpiceDBClient::from_channel(channel).token(token).build());
    }
}
