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
use spicedb::types::{UpdateOperation, WatchOptions};
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

/// Builds a `WatchResponse` carrying only `changes_through` -- no updates.
fn checkpoint_response(token: &str) -> proto::WatchResponse {
    proto::WatchResponse {
        changes_through: Some(proto::ZedToken {
            token: token.to_string(),
        }),
        is_checkpoint: true,
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

    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(spicedb::UpdatesRequest::new(object_types.to_vec()));
    tokio::pin!(stream);

    let first = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("OPERATION_UNSPECIFIED update must be yielded, not dropped")
        .expect("stream should yield a first item")
        .expect("first item should be Ok(WatchEvent)");
    assert_eq!(first.updates.len(), 1);
    assert_eq!(first.updates[0].operation, UpdateOperation::Unspecified);
    assert_ne!(
        first.updates[0].operation,
        UpdateOperation::Touch,
        "an unrecognized operation must never be reported as a write"
    );
    assert_eq!(
        first.updates[0].relationship.subject().object().object_id(),
        "alice"
    );

    let second = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("unknown future operation must be yielded, not dropped")
        .expect("stream should yield a second item")
        .expect("second item should be Ok(WatchEvent)");
    assert_eq!(second.updates[0].operation, UpdateOperation::Unspecified);
    assert_eq!(
        second.updates[0]
            .relationship
            .subject()
            .object()
            .object_id(),
        "bob"
    );

    let third = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("recognized operation should still arrive")
        .expect("stream should yield a third item")
        .expect("third item should be Ok(WatchEvent)");
    assert_eq!(third.updates[0].operation, UpdateOperation::Delete);
    assert_eq!(
        third.updates[0].relationship.subject().object().object_id(),
        "carol"
    );
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

    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(spicedb::UpdatesRequest::new(object_types.to_vec()));
    tokio::pin!(stream);

    // The FIRST update must arrive promptly — without waiting for the server
    // stream to close (it never will). A 2s timeout is generous; the update
    // arrives in milliseconds. The old buffer-until-end code would hang here.
    let first = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("first update should arrive quickly, not hang on the open stream")
        .expect("stream should yield a first item")
        .expect("first item should be Ok(WatchEvent)");
    assert_eq!(first.updates.len(), 1);
    assert_eq!(first.updates[0].operation, UpdateOperation::Create);
    assert_eq!(
        first.updates[0].relationship.resource().object_type(),
        "document"
    );
    assert_eq!(
        first.updates[0].relationship.resource().object_id(),
        "readme"
    );
    assert_eq!(first.updates[0].relationship.relation(), "viewer");
    assert_eq!(
        first.updates[0]
            .relationship
            .subject()
            .object()
            .object_type(),
        "user"
    );
    assert_eq!(
        first.updates[0].relationship.subject().object().object_id(),
        "alice"
    );

    // The SECOND update likewise arrives incrementally, while the stream is
    // still open.
    let second = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("second update should arrive quickly")
        .expect("stream should yield a second item")
        .expect("second item should be Ok(WatchEvent)");
    assert_eq!(second.updates[0].operation, UpdateOperation::Delete);
    assert_eq!(
        second.updates[0]
            .relationship
            .subject()
            .object()
            .object_id(),
        "bob"
    );
}

#[tokio::test]
async fn updates_retries_transient_establishment_failures() {
    // The mock fails the first two `Watch` establishment attempts with
    // UNAVAILABLE (transient), then succeeds on the third and streams one
    // update. This proves `updates()` retries stream *establishment*, not
    // just in-stream message reads, when the call opts in with its own retry
    // policy (the client-wide policy never reopens a watch).
    let responses = vec![watch_response(
        proto::relationship_update::Operation::Touch,
        rel_proto("document", "readme", "viewer", "user", "carol"),
    )];
    let mock = MockWatchService::new(responses, /* keep_open */ false)
        .fail_establish_times(2, Status::unavailable("mock transient failure"));
    let establish_calls = mock.establish_calls();
    let addr = spawn_watch_server(mock).await;

    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(
        spicedb::UpdatesRequest::new(object_types.to_vec()).with_options(
            spicedb::WatchOptions::new().with_call_options(
                spicedb::CallOptions::new().with_retry_policy(spicedb::RetryPolicy::new()),
            ),
        ),
    );
    tokio::pin!(stream);

    // The retry backoff (100ms, 200ms) means this can take a few hundred ms;
    // a 5s timeout is generous.
    let first = tokio::time::timeout(Duration::from_secs(5), stream.next())
        .await
        .expect("update should eventually arrive after establishment retries")
        .expect("stream should yield an item")
        .expect("item should be Ok(WatchEvent)");
    assert_eq!(first.updates[0].operation, UpdateOperation::Touch);
    assert_eq!(
        first.updates[0].relationship.subject().object().object_id(),
        "carol"
    );

    // Three establishment attempts: two failures + one success.
    assert_eq!(establish_calls.load(Ordering::SeqCst), 3);
}

// ---------------------------------------------------------------------------
// Watch resumability: a watch stream that dies cannot be correctly resumed
// unless the client surfaces changes_through (proto: "This token can be used
// in a subsequent WatchRequest to resume watching from this point"), and
// cannot survive an idle-timeout proxy unless the client can request
// WATCH_KIND_INCLUDE_CHECKPOINTS.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn watch_event_exposes_usable_resume_token() {
    let responses = vec![proto::WatchResponse {
        changes_through: Some(proto::ZedToken {
            token: "resume-me".to_string(),
        }),
        ..watch_response(
            proto::relationship_update::Operation::Touch,
            rel_proto("document", "readme", "viewer", "user", "alice"),
        )
    }];
    let addr = spawn_watch_server(MockWatchService::new(responses, false)).await;

    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(spicedb::UpdatesRequest::new(object_types.to_vec()));
    tokio::pin!(stream);

    let event = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("event should arrive")
        .expect("stream should yield an item")
        .expect("item should be Ok(WatchEvent)");
    assert_eq!(event.changes_through.as_ref(), "resume-me");
}

#[tokio::test]
async fn updates_without_include_checkpoints_requests_no_update_kinds() {
    let mock = MockWatchService::new(Vec::new(), false);
    let requests = mock.requests();
    let addr = spawn_watch_server(mock).await;

    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(spicedb::UpdatesRequest::new(object_types.to_vec()));
    tokio::pin!(stream);
    let _ = tokio::time::timeout(Duration::from_secs(2), stream.next()).await;

    let requests = requests.lock().unwrap();
    assert_eq!(requests.len(), 1);
    assert!(requests[0].optional_update_kinds.is_empty());
}

#[tokio::test]
async fn include_checkpoints_reaches_the_wire() {
    let mock = MockWatchService::new(Vec::new(), false);
    let requests = mock.requests();
    let addr = spawn_watch_server(mock).await;

    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(
        spicedb::UpdatesRequest::new(object_types.to_vec()).with_options(
            (WatchOptions::new().with_checkpoints(spicedb::CheckpointMode::Include)).clone(),
        ),
    );
    tokio::pin!(stream);
    let _ = tokio::time::timeout(Duration::from_secs(2), stream.next()).await;

    let requests = requests.lock().unwrap();
    assert_eq!(requests.len(), 1);
    assert!(requests[0]
        .optional_update_kinds
        .contains(&(proto::WatchKind::IncludeCheckpoints as i32)));
    // Requesting checkpoints must not silently drop relationship updates --
    // optional_update_kinds is empty-means-default, so a non-empty list is
    // the exact set requested.
    assert!(requests[0]
        .optional_update_kinds
        .contains(&(proto::WatchKind::IncludeRelationshipUpdates as i32)));
}

#[tokio::test]
async fn checkpoint_event_is_distinguishable_from_update_event() {
    let responses = vec![
        checkpoint_response("checkpoint-rev"),
        watch_response(
            proto::relationship_update::Operation::Touch,
            rel_proto("document", "readme", "viewer", "user", "dave"),
        ),
    ];
    let addr = spawn_watch_server(MockWatchService::new(responses, false)).await;

    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let object_types: Vec<String> = Vec::new();
    let stream = client.updates(
        spicedb::UpdatesRequest::new(object_types.to_vec()).with_options(
            (WatchOptions::new().with_checkpoints(spicedb::CheckpointMode::Include)).clone(),
        ),
    );
    tokio::pin!(stream);

    let checkpoint = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("checkpoint event should arrive")
        .expect("stream should yield a first item")
        .expect("first item should be Ok(WatchEvent)");
    assert!(checkpoint.is_checkpoint);
    assert!(checkpoint.updates.is_empty());
    assert_eq!(checkpoint.changes_through.as_ref(), "checkpoint-rev");

    let update = tokio::time::timeout(Duration::from_secs(2), stream.next())
        .await
        .expect("update event should arrive")
        .expect("stream should yield a second item")
        .expect("second item should be Ok(WatchEvent)");
    assert!(!update.is_checkpoint);
    assert_eq!(update.updates.len(), 1);
}

#[tokio::test]
async fn watch_content_and_checkpoint_enums_preserve_exact_wire_selection() {
    for (content, checkpoints, expected) in [
        (
            spicedb::WatchContent::Relationships,
            spicedb::CheckpointMode::Exclude,
            vec![],
        ),
        (
            spicedb::WatchContent::Relationships,
            spicedb::CheckpointMode::Include,
            vec![
                proto::WatchKind::IncludeRelationshipUpdates as i32,
                proto::WatchKind::IncludeCheckpoints as i32,
            ],
        ),
        (
            spicedb::WatchContent::RelationshipsAndSchema,
            spicedb::CheckpointMode::Exclude,
            vec![
                proto::WatchKind::IncludeRelationshipUpdates as i32,
                proto::WatchKind::IncludeSchemaUpdates as i32,
            ],
        ),
        (
            spicedb::WatchContent::RelationshipsAndSchema,
            spicedb::CheckpointMode::Include,
            vec![
                proto::WatchKind::IncludeRelationshipUpdates as i32,
                proto::WatchKind::IncludeCheckpoints as i32,
                proto::WatchKind::IncludeSchemaUpdates as i32,
            ],
        ),
    ] {
        let mock = MockWatchService::new(Vec::new(), false);
        let requests = mock.requests();
        let addr = spawn_watch_server(mock).await;
        let client = SpiceDBClient::new_plaintext(
            addr.to_string(),
            "token",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        .unwrap();
        let stream = client.updates(
            spicedb::UpdatesRequest::new(vec![]).with_options(
                WatchOptions::new()
                    .with_content(content)
                    .with_checkpoints(checkpoints)
                    .with_start_revision("opaque+/=".into()),
            ),
        );
        tokio::pin!(stream);
        let _ = stream.next().await;
        let requests = requests.lock().unwrap();
        assert_eq!(requests[0].optional_update_kinds, expected);
        assert_eq!(
            requests[0].optional_start_cursor.as_ref().unwrap().token,
            "opaque+/="
        );
    }
}

#[tokio::test]
async fn watch_relationship_filters_preserve_or_wire_semantics() {
    use spicedb::{Filter, SubjectRelationFilter};
    let filters = vec![
        Filter::new("document")
            .with_resource_id("one")
            .with_relation("viewer")
            .with_subject_type("user")
            .with_subject_id("alice")
            .with_subject_relation(SubjectRelationFilter::Direct),
        Filter::new("folder")
            .with_resource_id_prefix("team-")
            .with_relation("reader")
            .with_subject_type("group")
            .with_subject_id("eng")
            .with_subject_relation(SubjectRelationFilter::Named("member".into())),
    ];
    for empty in [false, true] {
        let mock = MockWatchService::new(Vec::new(), false);
        let requests = mock.requests();
        let addr = spawn_watch_server(mock).await;
        let client = SpiceDBClient::new_plaintext(
            addr.to_string(),
            "token",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        .unwrap();
        let stream = client.updates(spicedb::UpdatesRequest::new(vec![]).with_options(
            WatchOptions::new().with_filters(if empty { vec![] } else { filters.clone() }),
        ));
        tokio::pin!(stream);
        assert!(stream.next().await.is_none());
        let requests = requests.lock().unwrap();
        assert!(requests[0].optional_object_types.is_empty());
        if empty {
            assert!(requests[0].optional_relationship_filters.is_empty());
            continue;
        }
        assert_eq!(
            requests[0].optional_relationship_filters,
            vec![
                proto::RelationshipFilter {
                    resource_type: "document".into(),
                    optional_resource_id: "one".into(),
                    optional_relation: "viewer".into(),
                    optional_subject_filter: Some(proto::SubjectFilter {
                        subject_type: "user".into(),
                        optional_subject_id: "alice".into(),
                        optional_relation: Some(proto::subject_filter::RelationFilter {
                            relation: "".into()
                        })
                    }),
                    ..Default::default()
                },
                proto::RelationshipFilter {
                    resource_type: "folder".into(),
                    optional_resource_id_prefix: "team-".into(),
                    optional_relation: "reader".into(),
                    optional_subject_filter: Some(proto::SubjectFilter {
                        subject_type: "group".into(),
                        optional_subject_id: "eng".into(),
                        optional_relation: Some(proto::subject_filter::RelationFilter {
                            relation: "member".into()
                        })
                    }),
                    ..Default::default()
                },
            ]
        );
    }
}

#[tokio::test]
async fn watch_invalid_filters_fail_before_rpc() {
    for (types, filter) in [
        (vec!["document".into()], spicedb::Filter::new("document")),
        (vec![], spicedb::Filter::new("")),
        (
            vec![],
            spicedb::Filter::new("document").with_subject_id("alice"),
        ),
    ] {
        let mock = MockWatchService::new(Vec::new(), false);
        let requests = mock.requests();
        let addr = spawn_watch_server(mock).await;
        let client = SpiceDBClient::new_plaintext(
            addr.to_string(),
            "token",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        .unwrap();
        let stream = client.updates(
            spicedb::UpdatesRequest::new(types)
                .with_options(WatchOptions::new().with_filters(vec![filter])),
        );
        tokio::pin!(stream);
        assert!(matches!(
            stream.next().await.unwrap(),
            Err(spicedb::error::SpiceDBError::InvalidArgument(_))
        ));
        assert!(requests.lock().unwrap().is_empty());
    }
}
