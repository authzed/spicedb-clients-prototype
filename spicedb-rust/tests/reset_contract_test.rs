use spicedb::consistency::{self, Strategy};
use spicedb::{
    BulkCheckRequest, CheckOptions, ObjectRef, PermissionCheck, Relationship, SubjectRef,
    SubjectRelationFilter, ZedToken,
};

#[test]
fn native_values_and_snapshot_builders() {
    let object = ObjectRef::new("document", "one").unwrap();
    let subject = SubjectRef::new(ObjectRef::new("user", "alice").unwrap(), None).unwrap();
    let relationship = Relationship::new(object.clone(), "viewer", subject.clone()).unwrap();
    assert_eq!(
        relationship.filter().subject_relation(),
        &SubjectRelationFilter::Direct
    );
    let mut context = spicedb::Context::new();
    context.insert(
        "nested".into(),
        serde_json::json!({"values":[null,true,12]}),
    );
    let request = PermissionCheck::new(object, "view", subject, consistency::full())
        .with_context(context.clone());
    context.insert("nested".into(), serde_json::json!(false));
    assert_eq!(
        request.context().unwrap()["nested"]["values"][0],
        serde_json::Value::Null
    );
    let _ = BulkCheckRequest::new(vec![request])
        .with_options(CheckOptions::new().with_tracing(spicedb::TracingMode::Enabled));
    let token = ZedToken::from("opaque token");
    assert_eq!(token.to_string(), "opaque token");
    assert_eq!(
        consistency::at_least_or_full(Some(ZedToken::from(""))),
        Strategy::Full
    );
}

#[test]
fn client_is_clone_send_sync() {
    fn assert_traits<T: Clone + Send + Sync>() {}
    assert_traits::<spicedb::SpiceDBClient>();
}

mod support;
use spicedb::*;
use spicedb_proto::authzed::api::v1 as proto;
use std::sync::atomic::Ordering;
use std::time::Duration;
use support::{spawn_permissions_server, MockPermissionsService};

fn question(id: &str) -> PermissionCheck {
    PermissionCheck::new(
        ObjectRef::new("document", id).unwrap(),
        "view",
        SubjectRef::new(ObjectRef::new("user", "alice").unwrap(), None).unwrap(),
        consistency::full(),
    )
}
fn trace() -> proto::DebugInformation {
    proto::DebugInformation {
        schema_used: "definition user {}".into(),
        check: Some(proto::CheckDebugTrace {
            resource: Some(proto::ObjectReference {
                object_type: "document".into(),
                object_id: "one".into(),
            }),
            permission: "view".into(),
            permission_type: 2,
            subject: Some(proto::SubjectReference {
                object: Some(proto::ObjectReference {
                    object_type: "user".into(),
                    object_id: "alice".into(),
                }),
                optional_relation: "".into(),
            }),
            result: 3,
            duration: Some(prost_types::Duration {
                seconds: 1,
                nanos: 25,
            }),
            resolution: Some(proto::check_debug_trace::Resolution::SubProblems(
                proto::check_debug_trace::SubProblems {
                    traces: vec![proto::CheckDebugTrace {
                        permission: "viewer".into(),
                        permission_type: 999,
                        result: 999,
                        resolution: Some(proto::check_debug_trace::Resolution::WasCachedResult(
                            false,
                        )),
                        ..Default::default()
                    }],
                },
            )),
            caveat_evaluation_info: Some(proto::CaveatEvalInfo {
                expression: "x && y".into(),
                result: 4,
                context: Some(prost_types::Struct {
                    fields: [(
                        "nested".into(),
                        prost_types::Value {
                            kind: Some(prost_types::value::Kind::ListValue(
                                prost_types::ListValue {
                                    values: vec![prost_types::Value {
                                        kind: Some(prost_types::value::Kind::NullValue(0)),
                                    }],
                                },
                            )),
                        },
                    )]
                    .into_iter()
                    .collect(),
                }),
                partial_caveat_info: Some(proto::PartialCaveatInfo {
                    missing_required_context: vec!["x".into()],
                }),
                caveat_name: "condition".into(),
            }),
            optional_expires_at: Some(prost_types::Timestamp {
                seconds: 1_700_000_000,
                nanos: 123,
            }),
            trace_operation_id: "operation".into(),
            source: "spicedb:node".into(),
        }),
    }
}
fn bulk_response(n: usize, traced: bool) -> proto::CheckBulkPermissionsResponse {
    proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "revision".into(),
        }),
        pairs: (0..n)
            .map(|_| proto::CheckBulkPermissionsPair {
                request: None,
                response: Some(proto::check_bulk_permissions_pair::Response::Item(
                    proto::CheckBulkPermissionsResponseItem {
                        permissionship: 2,
                        partial_caveat_info: None,
                        debug_trace: traced.then(trace),
                    },
                )),
            })
            .collect(),
    }
}
#[tokio::test]
async fn singular_tracing_preserves_complete_native_tree_and_absence() {
    let mock = MockPermissionsService::new();
    let captured = mock.check_permission_requests();
    let bulk_calls = mock.check_bulk_permissions_calls();
    mock.push_check_permission_response(proto::CheckPermissionResponse {
        checked_at: Some(proto::ZedToken {
            token: "one".into(),
        }),
        permissionship: 3,
        partial_caveat_info: Some(proto::PartialCaveatInfo {
            missing_required_context: vec!["x".into()],
        }),
        debug_trace: Some(trace()),
        optional_expires_at: Some(prost_types::Timestamp {
            seconds: 1_700_000_000,
            nanos: 999,
        }),
    });
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let result = client
        .check(
            question("one")
                .with_options(CheckOptions::new().with_tracing(spicedb::TracingMode::Enabled)),
        )
        .await
        .unwrap();
    assert_eq!(result.expiration.unwrap().timestamp_subsec_nanos(), 999);
    assert!(!result.has_permission());
    assert_eq!(result.missing_context, vec!["x"]);
    let debug = result.debug_trace.unwrap();
    assert_eq!(debug.schema_used, "definition user {}");
    let root = debug.check.unwrap();
    assert_eq!(root.resource.unwrap().object_id(), "one");
    assert_eq!(root.subject.unwrap().object().object_id(), "alice");
    assert_eq!(root.permission_type, PermissionType::Permission);
    assert_eq!(root.operation_id, "operation");
    assert_eq!(root.source, "spicedb:node");
    assert_eq!(
        root.duration.unwrap(),
        chrono::TimeDelta::seconds(1) + chrono::TimeDelta::nanoseconds(25)
    );
    assert_eq!(root.expiration.unwrap().timestamp_subsec_nanos(), 123);
    let caveat = root.caveat_evaluation.unwrap();
    assert_eq!(caveat.context.unwrap()["nested"], serde_json::json!([null]));
    assert_eq!(caveat.result, CaveatResult::MissingContext);
    assert_eq!(caveat.missing_context, vec!["x"]);
    let TraceResolution::Subproblems(children) = root.resolution.unwrap() else {
        panic!("expected children")
    };
    assert_eq!(children[0].resolution, Some(TraceResolution::Cached(false)));
    assert_eq!(children[0].result, Permissionship::Unspecified);
    assert_eq!(children[0].permission_type, PermissionType::Unspecified);
    assert!(client
        .check(question("two"))
        .await
        .unwrap()
        .debug_trace
        .is_none());
    let requests = captured.lock().unwrap();
    assert!(requests[0].with_tracing);
    assert!(!requests[1].with_tracing);
    assert_eq!(bulk_calls.load(Ordering::SeqCst), 0);
}
#[tokio::test]
async fn bulk_trace_is_per_item_and_flags_reach_every_chunk() {
    let mock = MockPermissionsService::new();
    let captured = mock.check_bulk_permissions_requests();
    mock.push_check_bulk_permissions_response(bulk_response(1000, true));
    mock.push_check_bulk_permissions_response(bulk_response(1, false));
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let results = client
        .check_bulk(
            BulkCheckRequest::new((0..1001).map(|i| question(&i.to_string())).collect())
                .with_options(CheckOptions::new().with_tracing(spicedb::TracingMode::Enabled)),
        )
        .await
        .unwrap();
    assert_eq!(results.len(), 1001);
    assert!(results[0].debug_trace.is_some());
    assert!(results[1000].debug_trace.is_none());
    assert!(captured.lock().unwrap().iter().all(|r| r.with_tracing));
}
#[tokio::test]
async fn rejects_mixed_consistency_embedded_options_empty_tokens_and_lossy_context_before_rpc() {
    let mock = MockPermissionsService::new();
    let calls = mock.check_bulk_permissions_calls();
    let singular = mock.check_permission_requests();
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let mixed = PermissionCheck::new(
        ObjectRef::new("document", "two").unwrap(),
        "view",
        question("one").subject().clone(),
        consistency::min_latency(),
    );
    assert!(matches!(
        client
            .check_bulk(BulkCheckRequest::new(vec![question("one"), mixed]))
            .await,
        Err(SpiceDBError::InvalidArgument(_))
    ));
    assert!(matches!(
        client
            .check_bulk(BulkCheckRequest::new(vec![
                question("one").with_options(CheckOptions::new())
            ]))
            .await,
        Err(SpiceDBError::InvalidArgument(_))
    ));
    assert!(matches!(
        client
            .check_bulk(
                BulkCheckRequest::new(vec![])
                    .with_options(CheckOptions::new().with_timeout(Duration::ZERO))
            )
            .await,
        Err(SpiceDBError::InvalidArgument(_))
    ));
    let invalid = PermissionCheck::new(
        question("one").resource().clone(),
        "view",
        question("one").subject().clone(),
        consistency::snapshot(("").into()),
    );
    assert!(matches!(
        client.check(invalid).await,
        Err(SpiceDBError::InvalidArgument(_))
    ));
    let context = [("bad".into(), serde_json::json!(u64::MAX))]
        .into_iter()
        .collect();
    let error = client
        .check(question("one").with_context(context))
        .await
        .unwrap_err();
    assert!(error.message().contains("bad"));
    assert_eq!(calls.load(Ordering::SeqCst), 0);
    assert!(singular.lock().unwrap().is_empty());
}
#[tokio::test]
async fn bulk_chunks_share_one_total_budget() {
    let mock = MockPermissionsService::new();
    let calls = mock.check_bulk_permissions_calls();
    mock.stall_check_bulk_permissions(Duration::from_millis(250));
    mock.push_check_bulk_permissions_response(bulk_response(1000, false));
    mock.push_check_bulk_permissions_response(bulk_response(1, false));
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let start = tokio::time::Instant::now();
    let error = client
        .check_bulk(
            BulkCheckRequest::new((0..1001).map(|i| question(&i.to_string())).collect())
                .with_options(CheckOptions::new().with_timeout(Duration::from_millis(400))),
        )
        .await
        .unwrap_err();
    assert!(matches!(error, SpiceDBError::DeadlineExceeded(_)));
    assert!(start.elapsed() < Duration::from_millis(650));
    // The budget may expire after the first response, before RPC 2 starts.
    assert!((1..=2).contains(&calls.load(Ordering::SeqCst)));
}
#[tokio::test]
async fn bounded_delete_does_not_loop_and_delete_all_preserves_acknowledged_progress() {
    let mock = MockPermissionsService::new();
    let calls = mock.delete_relationships_calls();
    for _ in 0..2 {
        mock.push_delete_relationships_response(proto::DeleteRelationshipsResponse {
            deleted_at: Some(proto::ZedToken {
                token: "ack".into(),
            }),
            deletion_progress: 2,
            relationships_deleted_count: 3,
            after_result_cursor: Some(proto::Cursor {
                token: "resume".into(),
            }),
        });
    }
    mock.push_delete_relationships_response(proto::DeleteRelationshipsResponse {
        deletion_progress: 2,
        ..Default::default()
    });
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let bounded = client
        .delete_relationships(DeleteRequest::new(
            Filter::new("document"),
            std::num::NonZeroU32::new(3).unwrap(),
        ))
        .await
        .unwrap();
    assert_eq!(bounded.deleted_count, 3);
    assert!(!bounded.complete);
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    let failure = client
        .delete_all_relationships_non_atomic(DeleteAllRequest::new(Filter::new("document")))
        .await
        .unwrap_err();
    let SpiceDBError::DeleteProgress { progress, .. } = failure else {
        panic!("missing progress")
    };
    assert_eq!(progress.deleted_count, 3);
    assert_eq!(calls.load(Ordering::SeqCst), 3);
}

#[tokio::test]
async fn dropping_pending_stream_releases_server_and_clone_remains_usable() {
    use futures::StreamExt;
    let mut mock = MockPermissionsService::new();
    let active = mock.hold_read_stream();
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let clone = client.clone();
    let mut stream = Box::pin(client.read_relationships(ReadRelationshipsRequest::new(
        Filter::new("document"),
        consistency::full(),
    )));
    let pending = tokio::time::timeout(Duration::from_millis(100), stream.next()).await;
    assert!(pending.is_err());
    assert_eq!(active.load(Ordering::SeqCst), 1);
    assert!(tokio::time::timeout(
        Duration::from_millis(100),
        clone.check(question("concurrent"))
    )
    .await
    .unwrap()
    .unwrap()
    .has_permission());
    drop(stream);
    drop(client);
    tokio::time::timeout(Duration::from_secs(2), async {
        while active.load(Ordering::SeqCst) != 0 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("server stream must terminate after Drop");
    assert!(clone.check(question("one")).await.unwrap().has_permission());
}

#[tokio::test]
async fn retries_share_deadline_and_task_abort_stops_new_attempts() {
    let mock = MockPermissionsService::new();
    let captured = mock.check_permission_requests();
    mock.stall_check_bulk_permissions(Duration::from_millis(80));
    mock.queue_check_bulk_permissions_failure(tonic::Status::unavailable("retry"));
    let address = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        address.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let error = client
        .check(
            question("one")
                .with_options(CheckOptions::new().with_timeout(Duration::from_millis(125))),
        )
        .await
        .unwrap_err();
    assert!(matches!(error, SpiceDBError::DeadlineExceeded(_)));
    assert!(captured.lock().unwrap().len() <= 2);
    let mock = MockPermissionsService::new();
    let captured = mock.check_permission_requests();
    mock.stall_check_bulk_permissions(Duration::from_millis(100));
    for _ in 0..4 {
        mock.queue_check_bulk_permissions_failure(tonic::Status::unavailable("retry"));
    }
    let address = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        address.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let task = tokio::spawn(async move { client.check(question("one")).await });
    tokio::time::timeout(Duration::from_secs(1), async {
        while captured.lock().unwrap().is_empty() {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    task.abort();
    assert!(task.await.unwrap_err().is_cancelled());
    let attempts = captured.lock().unwrap().len();
    tokio::time::sleep(Duration::from_millis(200)).await;
    assert_eq!(captured.lock().unwrap().len(), attempts);
}

#[tokio::test]
async fn delete_pages_share_budget_and_timeout_retains_only_acknowledged_progress() {
    let mock = MockPermissionsService::new();
    let calls = mock.delete_relationships_calls();
    mock.stall_delete_relationships(Duration::from_millis(250));
    for _ in 0..2 {
        mock.push_delete_relationships_response(proto::DeleteRelationshipsResponse {
            deleted_at: Some(proto::ZedToken {
                token: "ack".into(),
            }),
            deletion_progress: 2,
            relationships_deleted_count: 3,
            after_result_cursor: Some(proto::Cursor {
                token: "resume".into(),
            }),
        });
    }
    let address = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        address.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let error = client
        .delete_all_relationships_non_atomic(
            DeleteAllRequest::new(Filter::new("document"))
                .with_options(DeleteAllOptions::new().with_timeout(Duration::from_millis(400))),
        )
        .await
        .unwrap_err();
    let SpiceDBError::DeleteProgress { progress, source } = error else {
        panic!("missing acknowledged progress")
    };
    assert_eq!(progress.deleted_count, 3);
    assert_eq!(progress.revision.as_ref(), "ack");
    assert_eq!(progress.cursor.as_deref(), Some("resume"));
    assert!(matches!(*source, SpiceDBError::DeadlineExceeded(_)));
    // The budget may expire after the first response, before RPC 2 starts.
    assert!((1..=2).contains(&calls.load(Ordering::SeqCst)));
}

fn echoed_item(id: &str) -> proto::CheckBulkPermissionsRequestItem {
    proto::CheckBulkPermissionsRequestItem {
        resource: Some(proto::ObjectReference {
            object_type: "document".into(),
            object_id: id.into(),
        }),
        permission: "view".into(),
        subject: Some(proto::SubjectReference {
            object: Some(proto::ObjectReference {
                object_type: "user".into(),
                object_id: "alice".into(),
            }),
            optional_relation: String::new(),
        }),
        context: None,
    }
}
#[tokio::test]
async fn bulk_rejects_missing_reordered_duplicated_and_wrong_context_echoes() {
    for (echoes, bad_index) in [
        (vec![None, Some(echoed_item("two"))], 0),
        (vec![Some(echoed_item("two")), Some(echoed_item("one"))], 0),
        (vec![Some(echoed_item("one")), Some(echoed_item("one"))], 1),
        (
            vec![
                Some(proto::CheckBulkPermissionsRequestItem {
                    context: Some(prost_types::Struct::default()),
                    ..echoed_item("one")
                }),
                Some(echoed_item("two")),
            ],
            0,
        ),
    ] {
        let mut mock = MockPermissionsService::new();
        mock.preserve_bulk_response_echoes();
        let mut response = bulk_response(2, false);
        for (pair, echo) in response.pairs.iter_mut().zip(echoes) {
            pair.request = echo;
        }
        mock.push_check_bulk_permissions_response(response);
        let address = spawn_permissions_server(mock).await;
        let client = SpiceDBClient::new_plaintext(
            address.to_string(),
            "token",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        .unwrap();
        let error = client
            .check_bulk(BulkCheckRequest::new(vec![
                question("one"),
                question("two"),
            ]))
            .await
            .unwrap_err();
        assert!(matches!(error, SpiceDBError::Status { code: 13, .. }));
        assert!(error
            .message()
            .contains(&format!("check item {bad_index}:")));
    }
}
#[tokio::test]
async fn malformed_bulk_echo_reports_absolute_index_after_chunk_boundary() {
    let mut mock = MockPermissionsService::new();
    mock.preserve_bulk_response_echoes();
    let mut first = bulk_response(1000, false);
    for (i, pair) in first.pairs.iter_mut().enumerate() {
        pair.request = Some(echoed_item(&i.to_string()));
    }
    mock.push_check_bulk_permissions_response(first);
    let mut second = bulk_response(1, false);
    second.pairs[0].request = Some(echoed_item("wrong"));
    mock.push_check_bulk_permissions_response(second);
    let address = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        address.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let error = client
        .check_bulk(BulkCheckRequest::new(
            (0..1001).map(|i| question(&i.to_string())).collect(),
        ))
        .await
        .unwrap_err();
    assert!(error.message().contains("check item 1000:"));
}

#[tokio::test]
async fn unknown_delete_progress_stops_mutations_and_preserves_every_acknowledged_count() {
    for wire_progress in [0, 999] {
        for prior_page in [false, true] {
            let mock = MockPermissionsService::new();
            let calls = mock.delete_relationships_calls();
            if prior_page {
                mock.push_delete_relationships_response(proto::DeleteRelationshipsResponse {
                    deleted_at: Some(proto::ZedToken {
                        token: "first".into(),
                    }),
                    deletion_progress: 2,
                    relationships_deleted_count: 3,
                    after_result_cursor: None,
                });
            }
            mock.push_delete_relationships_response(proto::DeleteRelationshipsResponse {
                deleted_at: Some(proto::ZedToken {
                    token: "unknown-ack".into(),
                }),
                deletion_progress: wire_progress,
                relationships_deleted_count: 2,
                after_result_cursor: Some(proto::Cursor {
                    token: "unknown-cursor".into(),
                }),
            });
            let address = spawn_permissions_server(mock).await;
            let client = SpiceDBClient::new_plaintext(
                address.to_string(),
                "token",
                spicedb::RemoteCredentialsPolicy::LoopbackOnly,
            )
            .await
            .unwrap();
            let error = client
                .delete_all_relationships_non_atomic(DeleteAllRequest::new(Filter::new("document")))
                .await
                .unwrap_err();
            let SpiceDBError::DeleteProgress { progress, source } = error else {
                panic!("missing acknowledged progress")
            };
            assert_eq!(progress.deleted_count, if prior_page { 5 } else { 2 });
            assert_eq!(progress.revision.as_ref(), "unknown-ack");
            assert_eq!(progress.cursor.as_deref(), Some("unknown-cursor"));
            assert!(!progress.complete);
            assert!(matches!(*source, SpiceDBError::Status { code: 13, .. }));
            assert!(source.message().contains("unknown deletion progress"));
            assert_eq!(calls.load(Ordering::SeqCst), if prior_page { 2 } else { 1 });
        }
        let mock = MockPermissionsService::new();
        let calls = mock.delete_relationships_calls();
        mock.push_delete_relationships_response(proto::DeleteRelationshipsResponse {
            deleted_at: Some(proto::ZedToken {
                token: "bounded-ack".into(),
            }),
            deletion_progress: wire_progress,
            relationships_deleted_count: 2,
            after_result_cursor: None,
        });
        let address = spawn_permissions_server(mock).await;
        let client = SpiceDBClient::new_plaintext(
            address.to_string(),
            "token",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        .unwrap();
        let error = client
            .delete_relationships(DeleteRequest::new(
                Filter::new("document"),
                std::num::NonZeroU32::new(2).unwrap(),
            ))
            .await
            .unwrap_err();
        let SpiceDBError::DeleteProgress { progress, .. } = error else {
            panic!("missing bounded progress")
        };
        assert_eq!(progress.deleted_count, 2);
        assert_eq!(progress.revision.as_ref(), "bounded-ack");
        assert_eq!(calls.load(Ordering::SeqCst), 1);
    }
}
