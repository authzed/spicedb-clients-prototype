//! The client-wide retry policy (the default, and with stream retries
//! enabled) never reopens a watch: a watch reopened without a start revision
//! begins at a later revision and silently skips the changes in between. Only
//! a per-call or per-operation policy opts a watch in to reopening before its
//! first event.

use std::time::Duration;

use futures::StreamExt;
use spicedb::testing::{server_error, InMemorySpiceDB};
use spicedb::tonic::Code;
use spicedb::{
    CallOptions, Operation, RetryPolicy, SpiceDBClient, SpiceDBError, StreamRetryMode,
    UpdatesRequest, WatchOptions, WatchPermissionSetsOptions, WatchPermissionSetsRequest,
};

fn reopen_once() -> RetryPolicy {
    RetryPolicy::new()
        .with_max_retries(1)
        .with_initial_backoff(Duration::from_millis(1))
}

async fn first_error(
    client: &SpiceDBClient,
    operation: Operation,
    call: Option<CallOptions>,
) -> SpiceDBError {
    let result = match operation {
        Operation::Updates => {
            let mut options = WatchOptions::new();
            if let Some(call) = call {
                options = options.with_call_options(call);
            }
            let mut stream =
                Box::pin(client.updates(UpdatesRequest::new(vec![]).with_options(options)));
            stream.next().await.map(|r| r.map(|_| ()))
        }
        Operation::WatchPermissionSets => {
            let mut options = WatchPermissionSetsOptions::new();
            if let Some(call) = call {
                options = options.with_call_options(call);
            }
            let mut stream = Box::pin(
                client
                    .watch_permission_sets(WatchPermissionSetsRequest::new().with_options(options)),
            );
            stream.next().await.map(|r| r.map(|_| ()))
        }
        other => panic!("not a watch: {other:?}"),
    };
    match result {
        Some(Err(err)) => err,
        other => panic!(
            "expected the watch to fail, got {:?}",
            other.map(|r| r.is_ok())
        ),
    }
}

const WATCHES: [Operation; 2] = [Operation::Updates, Operation::WatchPermissionSets];

#[tokio::test]
async fn the_default_client_never_reopens_a_watch() {
    for operation in WATCHES {
        let fake = InMemorySpiceDB::new();
        let channel = fake.channel();
        for client in [
            fake.client().unwrap(),
            SpiceDBClient::from_channel(channel)
                .retry_policy(reopen_once())
                .stream_retries(StreamRetryMode::Enabled)
                .build()
                .unwrap(),
        ] {
            let before = fake.calls_of(operation).len();
            fake.fail_next(operation, server_error(Code::Unavailable, "down"));
            let err = first_error(&client, operation, None).await;
            assert_eq!(err.code(), Code::Unavailable, "{operation:?}: {err}");
            assert_eq!(fake.calls_of(operation).len() - before, 1, "{operation:?}");
        }
    }
}

#[tokio::test]
async fn a_per_call_policy_reopens_a_watch_before_its_first_event() {
    for operation in WATCHES {
        let fake = InMemorySpiceDB::new();
        let client = fake.client().unwrap();
        fake.fail_next(operation, server_error(Code::Unavailable, "down"));
        let call = CallOptions::new().with_retry_policy(reopen_once());
        // The double does not model watches, so the reopened call is
        // answered UNIMPLEMENTED: reaching it proves the reopen.
        let err = first_error(&client, operation, Some(call)).await;
        assert_eq!(err.code(), Code::Unimplemented, "{operation:?}: {err}");
        assert_eq!(fake.calls_of(operation).len(), 2, "{operation:?}");
    }
}

#[tokio::test]
async fn a_per_operation_policy_reopens_a_watch_before_its_first_event() {
    for operation in WATCHES {
        let fake = InMemorySpiceDB::new();
        let client = SpiceDBClient::from_channel(fake.channel())
            .operation_retry_policy(operation, reopen_once())
            .build()
            .unwrap();
        fake.fail_next(operation, server_error(Code::Unavailable, "down"));
        let err = first_error(&client, operation, None).await;
        assert_eq!(err.code(), Code::Unimplemented, "{operation:?}: {err}");
        assert_eq!(fake.calls_of(operation).len(), 2, "{operation:?}");
    }
}
