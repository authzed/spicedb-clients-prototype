use spicedb::*;

#[test]
fn opaque_tokens_round_trip_without_normalization() {
    for value in ["", "  opaque+/=\n", "revision:☃", "000123"] {
        let token = ZedToken::from(value);
        assert_eq!(token.as_ref(), value);
        assert_eq!(token.to_string(), value);
        assert_eq!(token.clone().into_string(), value);
        assert_eq!(
            consistency::snapshot(token.clone()),
            consistency::Strategy::Snapshot(token)
        );
    }
}

#[test]
fn enum_defaults_are_explicit_and_reversible() {
    assert_eq!(
        CheckOptions::new(),
        CheckOptions::new().with_tracing(TracingMode::Disabled)
    );
    assert_eq!(
        LookupResourcesOptions::new(),
        LookupResourcesOptions::new().with_debug(DebugMode::Disabled)
    );
    assert_eq!(
        WatchOptions::new(),
        WatchOptions::new()
            .with_checkpoints(CheckpointMode::Exclude)
            .with_content(WatchContent::Relationships)
    );
    assert_eq!(
        CheckOptions::new(),
        CheckOptions::new()
            .with_tracing(TracingMode::Enabled)
            .with_tracing(TracingMode::Disabled)
    );
    assert_eq!(
        WatchOptions::new(),
        WatchOptions::new()
            .with_checkpoints(CheckpointMode::Include)
            .with_content(WatchContent::RelationshipsAndSchema)
            .with_checkpoints(CheckpointMode::Exclude)
            .with_content(WatchContent::Relationships)
    );
}

#[tokio::test]
async fn remote_plaintext_policy_works_in_constructor_and_builder() {
    for endpoint in ["192.0.2.1:1234", "remote.example:1234"] {
        for policy in [
            RemoteCredentialsPolicy::LoopbackOnly,
            RemoteCredentialsPolicy::AllowInsecureRemote,
        ] {
            let constructor = SpiceDBClient::new_plaintext(endpoint, "secret", policy).await;
            let builder = SpiceDBClient::builder(endpoint, "secret")
                .plaintext()
                .remote_credentials_policy(policy)
                .build()
                .await;
            for result in [constructor, builder] {
                match policy {
                    RemoteCredentialsPolicy::LoopbackOnly => {
                        assert!(matches!(result, Err(SpiceDBError::InvalidArgument(_))))
                    }
                    RemoteCredentialsPolicy::AllowInsecureRemote => assert!(result.is_ok()),
                    _ => unreachable!(),
                }
            }
        }
    }
}

mod support;
use futures::StreamExt;

#[tokio::test]
async fn debug_enum_reaches_lookup_wire_and_can_be_disabled() {
    let mock = support::MockPermissionsService::new();
    let captured = mock.lookup_resources_debug();
    let address = support::spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        address.to_string(),
        "token",
        RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    for mode in [DebugMode::Enabled, DebugMode::Disabled] {
        let stream = client.lookup_resources(
            LookupResourcesRequest::new(
                "document",
                "view",
                SubjectRef::new(ObjectRef::new("user", "alice").unwrap(), None).unwrap(),
                consistency::full(),
            )
            .with_options(LookupResourcesOptions::new().with_debug(mode)),
        );
        tokio::pin!(stream);
        assert!(stream.next().await.is_none());
    }
    assert_eq!(*captured.lock().unwrap(), vec![true, false]);
}
