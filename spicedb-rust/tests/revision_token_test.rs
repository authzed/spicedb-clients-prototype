//! A caller-supplied revision token that is empty or whitespace-only is a
//! local validation error, never sent to the server. See root DESIGN.md,
//! "Reject unusable credentials and trust material at construction".

mod support;

use futures::StreamExt;
use spicedb::consistency::{self, Strategy};
use spicedb::*;
use std::sync::atomic::Ordering;
use support::{spawn_permissions_server, MockPermissionsService};

const BLANK: [&str; 4] = ["", " ", "\t", " \n "];

fn assert_blank_revision_error(result: Result<impl std::fmt::Debug, SpiceDBError>) {
    match result {
        Err(SpiceDBError::InvalidArgument(detail)) => {
            let message = detail.to_string();
            assert!(
                message.contains("must not be empty or whitespace"),
                "{message}"
            );
        }
        other => panic!("expected InvalidArgument, got {other:?}"),
    }
}

/// A client that never dials: every call here must fail before any request.
async fn unreachable_client() -> SpiceDBClient {
    SpiceDBClient::new_plaintext(
        "127.0.0.1:1",
        "token",
        RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap()
}

#[tokio::test]
async fn blank_consistency_token_is_refused_before_any_request() {
    let mock = MockPermissionsService::new();
    let bulk_calls = mock.check_bulk_permissions_calls();
    let singular = mock.check_permission_requests();
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    for token in BLANK {
        for strategy in [
            consistency::at_least(token.into()),
            consistency::snapshot(token.into()),
        ] {
            let check = PermissionCheck::new(
                ObjectRef::new("document", "one").unwrap(),
                "view",
                SubjectRef::direct(ObjectRef::new("user", "alice").unwrap()),
                strategy,
            );
            assert_blank_revision_error(client.check(check.clone()).await);
            assert_blank_revision_error(
                client.check_bulk(BulkCheckRequest::new(vec![check])).await,
            );
        }
    }
    assert_eq!(bulk_calls.load(Ordering::SeqCst), 0);
    assert!(singular.lock().unwrap().is_empty());
}

#[tokio::test]
async fn blank_watch_start_revision_is_refused() {
    let client = unreachable_client().await;
    for token in BLANK {
        let stream = client.updates(
            UpdatesRequest::new(vec![])
                .with_options(WatchOptions::new().with_start_revision(token.into())),
        );
        tokio::pin!(stream);
        assert_blank_revision_error(stream.next().await.expect("an error item"));

        let stream = client.watch_permission_sets(
            WatchPermissionSetsRequest::new()
                .with_options(WatchPermissionSetsOptions::new().with_start_revision(token.into())),
        );
        tokio::pin!(stream);
        assert_blank_revision_error(stream.next().await.expect("an error item"));
    }
}

#[test]
fn optional_revision_helpers_treat_blank_as_absent() {
    for token in BLANK {
        assert_eq!(
            consistency::at_least_or_full(Some(token.into())),
            Strategy::Full
        );
        assert_eq!(
            consistency::at_least_or_min_latency(Some(token.into())),
            Strategy::MinLatency
        );
    }
}
