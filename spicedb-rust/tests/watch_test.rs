//! Behavioral test for `SpiceDBClient::updates()` (the watch API).
//!
//! Watch is an open-ended server stream: on a live watch the stream never
//! closes. The client must therefore deliver updates *incrementally*, as they
//! arrive, rather than buffering until the stream ends. This test proves that
//! by pointing the client at a mock server that emits two updates and then
//! holds the stream open forever — if `updates()` buffered until close (the old
//! `Vec`-returning behavior), the first `.next().await` would hang until the
//! 2-second timeout instead of returning immediately.

mod support;

use std::sync::atomic::Ordering;
use std::time::Duration;

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::types::UpdateOperation;
use spicedb_proto::authzed::api::v1 as proto;
use tonic::Status;

use support::{spawn_watch_server, MockWatchService};

/// Builds a minimal `proto::Relationship` for the mock to emit.
fn rel_proto(
    resource_type: &str,
    resource_id: &str,
    relation: &str,
    subject_type: &str,
    subject_id: &str,
) -> proto::Relationship {
    proto::Relationship {
        resource: Some(proto::ObjectReference {
            object_type: resource_type.to_string(),
            object_id: resource_id.to_string(),
        }),
        relation: relation.to_string(),
        subject: Some(proto::SubjectReference {
            object: Some(proto::ObjectReference {
                object_type: subject_type.to_string(),
                object_id: subject_id.to_string(),
            }),
            optional_relation: String::new(),
        }),
        optional_caveat: None,
        optional_expires_at: None,
    }
}

fn watch_response(
    op: proto::relationship_update::Operation,
    rel: proto::Relationship,
) -> proto::WatchResponse {
    proto::WatchResponse {
        updates: vec![proto::RelationshipUpdate {
            operation: op as i32,
            relationship: Some(rel),
        }],
        ..Default::default()
    }
}

/// Builds a `WatchResponse` carrying a raw operation discriminant, so a test
/// can emit a value this client has no enum variant for.
fn watch_response_raw_op(op: i32, rel: proto::Relationship) -> proto::WatchResponse {
    proto::WatchResponse {
        updates: vec![proto::RelationshipUpdate {
            operation: op,
            relationship: Some(rel),
        }],
        ..Default::default()
    }
}

/// An unrecognized watch operation — `OPERATION_UNSPECIFIED`, or a future wire
/// value added after this client shipped — must still be yielded, mapped to
/// `UpdateOperation::Unspecified`.
///
/// Before this fix the mapper's fallthrough arm was `_ => continue`, which
/// silently dropped the entire update: a consumer mirroring the stream into a
/// cache or index would never learn that the relationship had changed at all,
/// with no error and no gap it could detect. Root `DESIGN.md`, "RULE: A
/// conversion that cannot preserve meaning must fail", clause 2 —
/// server-supplied values the client does not recognise MUST NOT raise, and
/// MUST map to the safe, non-permissive default. Dropping is not that default:
/// `Unspecified` is inspectable, a missing event is not.
#[tokio::test]
async fn updates_yields_unrecognized_operation_as_unspecified_rather_than_dropping_it() {
    let responses = vec![
        // OPERATION_UNSPECIFIED, explicitly on the wire.
        watch_response_raw_op(
            proto::relationship_update::Operation::Unspecified as i32,
            rel_proto("document", "readme", "viewer", "user", "alice"),
        ),
        // A discriminant no version of this client knows about, standing in
        // for an operation added to the proto after this client shipped.
        watch_response_raw_op(
            9999,
            rel_proto("document", "readme", "viewer", "user", "bob"),
        ),
        // A recognized operation after the unrecognized ones, proving the
        // stream keeps working rather than stalling.
        watch_response(
            proto::relationship_update::Operation::Delete,
            rel_proto("document", "readme", "viewer", "user", "carol"),
        ),
    ];
    let addr = spawn_watch_server(MockWatchService::new(responses, /* keep_open */ true)).await;

    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(&object_types, None);
    tokio::pin!(stream);

    let first = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("OPERATION_UNSPECIFIED update must be yielded, not dropped")
        .expect("stream should yield a first item")
        .expect("first item should be Ok(Update)");
    assert_eq!(first.operation, UpdateOperation::Unspecified);
    assert_ne!(
        first.operation,
        UpdateOperation::Touch,
        "an unrecognized operation must never be reported as a write"
    );
    assert_eq!(first.relationship.subject_id, "alice");

    let second = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("unknown future operation must be yielded, not dropped")
        .expect("stream should yield a second item")
        .expect("second item should be Ok(Update)");
    assert_eq!(second.operation, UpdateOperation::Unspecified);
    assert_eq!(second.relationship.subject_id, "bob");

    let third = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("recognized operation should still arrive")
        .expect("stream should yield a third item")
        .expect("third item should be Ok(Update)");
    assert_eq!(third.operation, UpdateOperation::Delete);
    assert_eq!(third.relationship.subject_id, "carol");
}

#[tokio::test]
async fn updates_yields_incrementally_while_stream_stays_open() {
    // The mock emits two updates and then KEEPS THE STREAM OPEN forever.
    let responses = vec![
        watch_response(
            proto::relationship_update::Operation::Create,
            rel_proto("document", "readme", "viewer", "user", "alice"),
        ),
        watch_response(
            proto::relationship_update::Operation::Delete,
            rel_proto("document", "readme", "viewer", "user", "bob"),
        ),
    ];
    let addr = spawn_watch_server(MockWatchService::new(responses, /* keep_open */ true)).await;

    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(&object_types, None);
    tokio::pin!(stream);

    // The FIRST update must arrive promptly — without waiting for the server
    // stream to close (it never will). A 2s timeout is generous; the update
    // arrives in milliseconds. The old buffer-until-end code would hang here.
    let first = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("first update should arrive quickly, not hang on the open stream")
        .expect("stream should yield a first item")
        .expect("first item should be Ok(Update)");
    assert_eq!(first.operation, UpdateOperation::Create);
    assert_eq!(first.relationship.resource_type, "document");
    assert_eq!(first.relationship.resource_id, "readme");
    assert_eq!(first.relationship.resource_relation, "viewer");
    assert_eq!(first.relationship.subject_type, "user");
    assert_eq!(first.relationship.subject_id, "alice");

    // The SECOND update likewise arrives incrementally, while the stream is
    // still open.
    let second = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("second update should arrive quickly")
        .expect("stream should yield a second item")
        .expect("second item should be Ok(Update)");
    assert_eq!(second.operation, UpdateOperation::Delete);
    assert_eq!(second.relationship.subject_id, "bob");
}

#[tokio::test]
async fn updates_retries_transient_establishment_failures() {
    // The mock fails the first two `Watch` establishment attempts with
    // UNAVAILABLE (transient), then succeeds on the third and streams one
    // update. This proves `updates()` retries stream *establishment*, not
    // just in-stream message reads.
    let responses = vec![watch_response(
        proto::relationship_update::Operation::Touch,
        rel_proto("document", "readme", "viewer", "user", "carol"),
    )];
    let mock = MockWatchService::new(responses, /* keep_open */ false)
        .fail_establish_times(2, Status::unavailable("mock transient failure"));
    let establish_calls = mock.establish_calls();
    let addr = spawn_watch_server(mock).await;

    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(&object_types, None);
    tokio::pin!(stream);

    // The retry backoff (100ms, 200ms) means this can take a few hundred ms;
    // a 5s timeout is generous.
    let first = tokio::time::timeout(Duration::from_secs(5), stream.next())
        .await
        .expect("update should eventually arrive after establishment retries")
        .expect("stream should yield an item")
        .expect("item should be Ok(Update)");
    assert_eq!(first.operation, UpdateOperation::Touch);
    assert_eq!(first.relationship.subject_id, "carol");

    // Three establishment attempts: two failures + one success.
    assert_eq!(establish_calls.load(Ordering::SeqCst), 3);
}
