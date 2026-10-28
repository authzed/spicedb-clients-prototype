//! Adoption items for tooling consumers (zed and friends): caller-owned
//! channels, retry/deadline policy, per-call metadata, paging state and the
//! paging-argument bug, import abort status and batch size, bulk item errors,
//! all-or-nothing delete and delete-all progress, error interop, per-call
//! retry policies, single-request paging and protocol round trips.
//!
//! The mock below emulates SpiceDB's cursor rule: a cursor is only accepted
//! with the same consistency and limit as the request that produced it.

#![allow(clippy::result_large_err, clippy::type_complexity)]

mod support;

use std::num::NonZeroU32;
use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures::{Stream, StreamExt};
use spicedb::error::code_of;
use spicedb::{
    consistency, BulkCheckRequest, BulkItemErrors, CallOptions, CheckOptions, DefaultDeadline,
    DeleteAllOptions, DeleteAllRequest, DeleteOptions, DeletePartial, DeleteRequest,
    ExportRelationshipsOptions, ExportRelationshipsRequest, Filter, ImportRelationshipsOptions,
    ImportRelationshipsRequest, LookupResourcesOptions, LookupResourcesRequest,
    LookupSubjectsRequest, ObjectRef, Paging, PermissionCheck, ReadRelationshipsOptions,
    ReadRelationshipsRequest, Relationship, ResponseMetadata, RetryMode, RetryPolicy,
    SpiceDBClient, SpiceDBError, StreamRetryMode, SubjectRef, Transaction, UpdatesRequest,
    WatchOptions, WriteOptions, WriteRequest, WriteSchemaOptions, WriteSchemaRequest,
};
use spicedb_proto::authzed::api::v1 as proto;
use spicedb_proto::authzed::api::v1::permissions_service_server::{
    PermissionsService, PermissionsServiceServer,
};
use spicedb_proto::authzed::api::v1::schema_service_server::{SchemaService, SchemaServiceServer};
use spicedb_proto::authzed::api::v1::watch_service_server::{WatchService, WatchServiceServer};
use tonic::transport::{Endpoint, Server};
use tonic::{Code, Request, Response, Status};

type BoxStream<T> = Pin<Box<dyn Stream<Item = Result<T, Status>> + Send>>;

/// What the mock saw.
#[derive(Default)]
struct Seen {
    calls: AtomicUsize,
    /// (consistency, limit, cursor) of every paged request.
    paged: Mutex<Vec<(Option<proto::Consistency>, u32, Option<String>)>>,
    authorization: Mutex<Vec<Option<String>>>,
    grpc_timeout: Mutex<Vec<bool>>,
    custom_header: Mutex<Vec<Option<String>>>,
    import_messages: Mutex<Vec<usize>>,
    deletes: Mutex<Vec<proto::DeleteRelationshipsRequest>>,
    writes: AtomicUsize,
    schema_writes: AtomicUsize,
}

#[derive(Clone, Default)]
struct Mock {
    seen: Arc<Seen>,
    /// Number of rows each paged RPC serves.
    rows: usize,
    /// Fail every unary check with this code, when set.
    check_failure: Option<Code>,
    /// Abort imports with ALREADY_EXISTS after the first message.
    abort_import: bool,
    /// Delay every unary check by this long.
    check_stall: Option<Duration>,
    /// Fail the first relationship write with this code, when set.
    write_failure: Option<Code>,
    /// Fail the second delete page with UNAVAILABLE.
    fail_second_delete: bool,
}

impl Mock {
    fn record<T>(&self, request: &Request<T>) {
        self.seen.calls.fetch_add(1, Ordering::SeqCst);
        let header = |k: &str| {
            request
                .metadata()
                .get(k)
                .map(|v| v.to_str().unwrap().to_string())
        };
        self.seen
            .authorization
            .lock()
            .unwrap()
            .push(header("authorization"));
        self.seen
            .grpc_timeout
            .lock()
            .unwrap()
            .push(request.metadata().get("grpc-timeout").is_some());
        self.seen
            .custom_header
            .lock()
            .unwrap()
            .push(header("x-request-id"));
    }

    /// SpiceDB's cursor rule: a cursor is bound to the consistency and limit of
    /// the call that produced it.
    fn page(
        &self,
        consistency: Option<proto::Consistency>,
        limit: u32,
        cursor: Option<proto::Cursor>,
    ) -> Result<std::ops::Range<usize>, Status> {
        let mut paged = self.seen.paged.lock().unwrap();
        let cursor = cursor.map(|c| c.token);
        if let Some(c) = &cursor {
            let (first_consistency, first_limit, _) = paged.first().cloned().unwrap_or_default();
            if paged.is_empty() || first_consistency != consistency || first_limit != limit {
                return Err(Status::invalid_argument(format!(
                    "the cursor provided does not have the same arguments as the original API call: {c}"
                )));
            }
        }
        paged.push((consistency, limit, cursor.clone()));
        let start: usize = cursor.map_or(0, |c| c.parse().unwrap());
        // A zero limit is unbounded, as in SpiceDB.
        let end = match limit {
            0 => self.rows,
            n => (start + n as usize).min(self.rows),
        };
        Ok(start..end)
    }
}

fn token(s: &str) -> Option<proto::ZedToken> {
    Some(proto::ZedToken { token: s.into() })
}

fn proto_rel(i: usize) -> proto::Relationship {
    Relationship::from_tuple(&format!("document:d{i}#viewer@user:alice"))
        .map(|r| spicedb::protoconv::relationship_to_proto(&r))
        .unwrap()
}

#[tonic::async_trait]
impl PermissionsService for Mock {
    type ReadRelationshipsStream = BoxStream<proto::ReadRelationshipsResponse>;
    type LookupResourcesStream = BoxStream<proto::LookupResourcesResponse>;
    type LookupSubjectsStream = BoxStream<proto::LookupSubjectsResponse>;
    type ExportBulkRelationshipsStream = BoxStream<proto::ExportBulkRelationshipsResponse>;

    async fn read_relationships(
        &self,
        request: Request<proto::ReadRelationshipsRequest>,
    ) -> Result<Response<Self::ReadRelationshipsStream>, Status> {
        self.record(&request);
        let r = request.into_inner();
        let range = self.page(r.consistency, r.optional_limit, r.optional_cursor)?;
        let items: Vec<_> = range
            .map(|i| {
                Ok(proto::ReadRelationshipsResponse {
                    read_at: token("rev-1"),
                    relationship: Some(proto_rel(i)),
                    after_result_cursor: Some(proto::Cursor {
                        token: (i + 1).to_string(),
                    }),
                })
            })
            .collect();
        Ok(Response::new(Box::pin(futures::stream::iter(items))))
    }
    async fn lookup_resources(
        &self,
        request: Request<proto::LookupResourcesRequest>,
    ) -> Result<Response<Self::LookupResourcesStream>, Status> {
        self.record(&request);
        let r = request.into_inner();
        let range = self.page(r.consistency, r.optional_limit, r.optional_cursor)?;
        let items: Vec<_> = range
            .map(|i| {
                Ok(proto::LookupResourcesResponse {
                    looked_up_at: token("rev-1"),
                    resource_object_id: format!("d{i}"),
                    permissionship: proto::LookupPermissionship::HasPermission as i32,
                    partial_caveat_info: None,
                    after_result_cursor: Some(proto::Cursor {
                        token: (i + 1).to_string(),
                    }),
                })
            })
            .collect();
        Ok(Response::new(Box::pin(futures::stream::iter(items))))
    }
    async fn export_bulk_relationships(
        &self,
        request: Request<proto::ExportBulkRelationshipsRequest>,
    ) -> Result<Response<Self::ExportBulkRelationshipsStream>, Status> {
        self.record(&request);
        let r = request.into_inner();
        let range = self.page(r.consistency, r.optional_limit, r.optional_cursor)?;
        let end = range.end;
        let batch = proto::ExportBulkRelationshipsResponse {
            after_result_cursor: Some(proto::Cursor {
                token: end.to_string(),
            }),
            relationships: range.map(proto_rel).collect(),
        };
        Ok(Response::new(Box::pin(futures::stream::iter([Ok(batch)]))))
    }
    async fn check_permission(
        &self,
        request: Request<proto::CheckPermissionRequest>,
    ) -> Result<Response<proto::CheckPermissionResponse>, Status> {
        self.record(&request);
        if let Some(d) = self.check_stall {
            tokio::time::sleep(d).await;
        }
        if let Some(code) = self.check_failure {
            return Err(Status::new(code, "injected"));
        }
        let mut response = Response::new(proto::CheckPermissionResponse {
            checked_at: token("rev-1"),
            permissionship: 2,
            ..Default::default()
        });
        response
            .metadata_mut()
            .insert("io.spicedb.debug.version", "v1.99.0".parse().unwrap());
        Ok(response)
    }
    async fn check_bulk_permissions(
        &self,
        request: Request<proto::CheckBulkPermissionsRequest>,
    ) -> Result<Response<proto::CheckBulkPermissionsResponse>, Status> {
        self.record(&request);
        let r = request.into_inner();
        let pairs = r
            .items
            .into_iter()
            .enumerate()
            .map(|(i, item)| proto::CheckBulkPermissionsPair {
                request: Some(item),
                response: Some(if i == 1 {
                    proto::check_bulk_permissions_pair::Response::Error(
                        spicedb_proto::google::rpc::Status {
                            code: Code::NotFound as i32,
                            message: "object definition `nope` not found".into(),
                            details: vec![],
                        },
                    )
                } else {
                    proto::check_bulk_permissions_pair::Response::Item(
                        proto::CheckBulkPermissionsResponseItem {
                            permissionship: 2,
                            ..Default::default()
                        },
                    )
                }),
            })
            .collect();
        Ok(Response::new(proto::CheckBulkPermissionsResponse {
            checked_at: token("rev-1"),
            pairs,
        }))
    }
    async fn delete_relationships(
        &self,
        request: Request<proto::DeleteRelationshipsRequest>,
    ) -> Result<Response<proto::DeleteRelationshipsResponse>, Status> {
        self.record(&request);
        let r = request.into_inner();
        let mut deletes = self.seen.deletes.lock().unwrap();
        deletes.push(r);
        let n = deletes.len();
        if self.fail_second_delete && n == 2 {
            return Err(Status::unavailable("datastore gone"));
        }
        Ok(Response::new(proto::DeleteRelationshipsResponse {
            deleted_at: token(&format!("rev-{n}")),
            deletion_progress: if n < 3 { 2 } else { 1 },
            relationships_deleted_count: 10,
            after_result_cursor: Some(proto::Cursor {
                token: format!("c{n}"),
            }),
        }))
    }
    async fn import_bulk_relationships(
        &self,
        request: Request<tonic::Streaming<proto::ImportBulkRelationshipsRequest>>,
    ) -> Result<Response<proto::ImportBulkRelationshipsResponse>, Status> {
        self.record(&request);
        let mut stream = request.into_inner();
        let mut loaded = 0;
        while let Some(message) = stream.message().await? {
            self.seen
                .import_messages
                .lock()
                .unwrap()
                .push(message.relationships.len());
            loaded += message.relationships.len() as u64;
            if self.abort_import {
                return Err(Status::already_exists(
                    "could not CREATE one or more relationships, as they already existed",
                ));
            }
        }
        Ok(Response::new(proto::ImportBulkRelationshipsResponse {
            num_loaded: loaded,
        }))
    }
    async fn write_relationships(
        &self,
        request: Request<proto::WriteRelationshipsRequest>,
    ) -> Result<Response<proto::WriteRelationshipsResponse>, Status> {
        self.record(&request);
        let n = self.seen.writes.fetch_add(1, Ordering::SeqCst) + 1;
        if let (1, Some(code)) = (n, self.write_failure) {
            return Err(Status::new(code, "transient"));
        }
        Ok(Response::new(proto::WriteRelationshipsResponse {
            written_at: token("w"),
        }))
    }
    async fn expand_permission_tree(
        &self,
        _: Request<proto::ExpandPermissionTreeRequest>,
    ) -> Result<Response<proto::ExpandPermissionTreeResponse>, Status> {
        Err(Status::unimplemented(""))
    }
    async fn lookup_subjects(
        &self,
        request: Request<proto::LookupSubjectsRequest>,
    ) -> Result<Response<Self::LookupSubjectsStream>, Status> {
        self.record(&request);
        Ok(Response::new(Box::pin(futures::stream::iter(
            lookup_subjects_responses().into_iter().map(Ok),
        ))))
    }
}

/// LookupSubjects responses as SpiceDB sends them: the deprecated top-level
/// fields mirror `subject` and `excluded_subjects`.
#[allow(deprecated)]
fn lookup_subjects_responses() -> Vec<proto::LookupSubjectsResponse> {
    let has = proto::LookupPermissionship::HasPermission as i32;
    let conditional = proto::LookupPermissionship::ConditionalPermission as i32;
    let caveat = Some(proto::PartialCaveatInfo {
        missing_required_context: vec!["k".into()],
    });
    vec![
        proto::LookupSubjectsResponse {
            looked_up_at: token("rev-3"),
            subject: Some(proto::ResolvedSubject {
                subject_object_id: "*".into(),
                permissionship: has,
                partial_caveat_info: None,
            }),
            subject_object_id: "*".into(),
            permissionship: has,
            excluded_subjects: vec![proto::ResolvedSubject {
                subject_object_id: "bob".into(),
                permissionship: conditional,
                partial_caveat_info: caveat.clone(),
            }],
            excluded_subject_ids: vec!["bob".into()],
            ..Default::default()
        },
        proto::LookupSubjectsResponse {
            looked_up_at: token("rev-3"),
            subject: Some(proto::ResolvedSubject {
                subject_object_id: "carol".into(),
                permissionship: conditional,
                partial_caveat_info: caveat.clone(),
            }),
            subject_object_id: "carol".into(),
            permissionship: conditional,
            partial_caveat_info: caveat,
            ..Default::default()
        },
    ]
}

#[tonic::async_trait]
impl SchemaService for Mock {
    async fn read_schema(
        &self,
        _: Request<proto::ReadSchemaRequest>,
    ) -> Result<Response<proto::ReadSchemaResponse>, Status> {
        Err(Status::unimplemented(""))
    }
    /// Fails the first write with UNAVAILABLE.
    async fn write_schema(
        &self,
        request: Request<proto::WriteSchemaRequest>,
    ) -> Result<Response<proto::WriteSchemaResponse>, Status> {
        self.record(&request);
        if self.seen.schema_writes.fetch_add(1, Ordering::SeqCst) == 0 {
            return Err(Status::unavailable("transient"));
        }
        Ok(Response::new(proto::WriteSchemaResponse {
            written_at: token("s"),
        }))
    }
    async fn reflect_schema(
        &self,
        _: Request<proto::ReflectSchemaRequest>,
    ) -> Result<Response<proto::ReflectSchemaResponse>, Status> {
        Err(Status::unimplemented(""))
    }
    async fn computable_permissions(
        &self,
        _: Request<proto::ComputablePermissionsRequest>,
    ) -> Result<Response<proto::ComputablePermissionsResponse>, Status> {
        Err(Status::unimplemented(""))
    }
    async fn dependent_relations(
        &self,
        _: Request<proto::DependentRelationsRequest>,
    ) -> Result<Response<proto::DependentRelationsResponse>, Status> {
        Err(Status::unimplemented(""))
    }
    async fn diff_schema(
        &self,
        _: Request<proto::DiffSchemaRequest>,
    ) -> Result<Response<proto::DiffSchemaResponse>, Status> {
        Err(Status::unimplemented(""))
    }
}

#[tonic::async_trait]
impl WatchService for Mock {
    type WatchStream = BoxStream<proto::WatchResponse>;
    async fn watch(
        &self,
        request: Request<proto::WatchRequest>,
    ) -> Result<Response<Self::WatchStream>, Status> {
        self.record(&request);
        Err(Status::unavailable("watch down"))
    }
}

/// Serves `mock` and returns a caller-owned channel to it.
async fn serve(mock: Mock) -> tonic::transport::Channel {
    let addr = support::spawn_server(
        Server::builder()
            .add_service(PermissionsServiceServer::new(mock.clone()))
            .add_service(SchemaServiceServer::new(mock.clone()))
            .add_service(WatchServiceServer::new(mock)),
    )
    .await;
    Endpoint::from_shared(format!("http://{addr}"))
        .unwrap()
        .connect_lazy()
}

async fn client(mock: Mock) -> SpiceDBClient {
    SpiceDBClient::from_channel(serve(mock).await)
        .token("t0ken")
        .build()
        .unwrap()
}

fn question() -> PermissionCheck {
    PermissionCheck::new(
        ObjectRef::new("document", "d0").unwrap(),
        "view",
        SubjectRef::new(ObjectRef::new("user", "alice").unwrap(), None).unwrap(),
        consistency::full(),
    )
}

// ---------------------------------------------------------------------------
// Paging: every page repeats consistency and page size (the page-two bug).
// ---------------------------------------------------------------------------

#[tokio::test]
async fn read_relationships_repeats_consistency_and_page_size_on_every_page() {
    let mock = Mock {
        rows: 5,
        ..Default::default()
    };
    let seen = mock.seen.clone();
    let client = client(mock).await;
    let request = ReadRelationshipsRequest::new(Filter::new("document"), consistency::full())
        .with_options(
            ReadRelationshipsOptions::new()
                .with_page_size(NonZeroU32::new(2).unwrap())
                .with_limit(5),
        );
    let items: Vec<_> = client
        .read_relationships(request.clone())
        .map(|r| r.expect("every page must be accepted"))
        .collect()
        .await;
    assert_eq!(items.len(), 5);
    let paged = seen.paged.lock().unwrap().clone();
    assert_eq!(paged.len(), 3);
    for (consistency, limit, _) in &paged {
        assert_eq!(consistency, &paged[0].0, "consistency must not change");
        assert_eq!(*limit, 2, "the page size must not shrink");
    }
    assert_eq!(items[2].after_result_cursor.as_deref(), Some("3"));
    assert_eq!(items[2].read_at.as_ref(), "rev-1");

    // Resume from an item's cursor with the same request and page size.
    let resumed: Vec<_> = client
        .read_relationships(
            request.with_options(
                ReadRelationshipsOptions::new()
                    .with_page_size(NonZeroU32::new(2).unwrap())
                    .with_cursor(items[2].after_result_cursor.clone().unwrap()),
            ),
        )
        .map(|r| r.unwrap().relationship.resource_id().to_string())
        .collect()
        .await;
    assert_eq!(resumed, vec!["d3", "d4"]);
}

#[tokio::test]
async fn total_limit_is_enforced_client_side() {
    let mock = Mock {
        rows: 10,
        ..Default::default()
    };
    let seen = mock.seen.clone();
    let client = client(mock).await;
    let items: Vec<_> = client
        .lookup_resources(
            LookupResourcesRequest::new(
                "document",
                "view",
                question().subject().clone(),
                consistency::min_latency(),
            )
            .with_options(
                LookupResourcesOptions::new()
                    .with_page_size(NonZeroU32::new(3).unwrap())
                    .with_limit(4),
            ),
        )
        .map(|r| r.expect("every page must be accepted"))
        .collect()
        .await;
    assert_eq!(
        items
            .iter()
            .map(|i| i.resource_id.as_str())
            .collect::<Vec<_>>(),
        vec!["d0", "d1", "d2", "d3"]
    );
    assert_eq!(items[3].after_result_cursor.as_deref(), Some("4"));
    let paged = seen.paged.lock().unwrap().clone();
    assert!(paged.iter().all(|(c, l, _)| *l == 3 && c == &paged[0].0));
}

#[tokio::test]
async fn export_items_carry_their_response_cursor() {
    let mock = Mock {
        rows: 5,
        ..Default::default()
    };
    let client = client(mock).await;
    let items: Vec<_> = client
        .export_relationships(
            ExportRelationshipsRequest::new(consistency::full()).with_options(
                ExportRelationshipsOptions::new().with_page_size(NonZeroU32::new(2).unwrap()),
            ),
        )
        .map(|r| r.expect("every page must be accepted"))
        .collect()
        .await;
    assert_eq!(items.len(), 5);
    assert_eq!(items[0].after_result_cursor.as_deref(), Some("2"));
    assert_eq!(items[1].after_result_cursor.as_deref(), Some("2"));
    assert_eq!(items[4].after_result_cursor.as_deref(), Some("5"));
}

// ---------------------------------------------------------------------------
// Caller-owned channels.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn from_channel_leaves_the_channel_usable_and_controls_the_token() {
    let mock = Mock::default();
    let seen = mock.seen.clone();
    let channel = serve(mock).await;

    let with_token = SpiceDBClient::from_channel(channel.clone())
        .token("t0ken")
        .build()
        .unwrap();
    let _ = with_token.check(question()).await.unwrap();
    drop(with_token);

    // The caller's channel still works after the client is gone.
    let without_token = SpiceDBClient::from_channel(channel).build().unwrap();
    let _ = without_token.check(question()).await.unwrap();

    assert_eq!(
        *seen.authorization.lock().unwrap(),
        vec![Some("Bearer t0ken".to_string()), None]
    );
}

// ---------------------------------------------------------------------------
// Default deadline and retry policy.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn default_deadline_can_be_disabled() {
    let mock = Mock::default();
    let seen = mock.seen.clone();
    let channel = serve(mock).await;
    let default = SpiceDBClient::from_channel(channel.clone())
        .build()
        .unwrap();
    let _ = default.check(question()).await.unwrap();
    let unbounded = SpiceDBClient::from_channel(channel.clone())
        .default_deadline(DefaultDeadline::Disabled)
        .build()
        .unwrap();
    let _ = unbounded.check(question()).await.unwrap();
    // A per-call timeout still applies with the default disabled.
    let _ = unbounded
        .check(question().with_options(CheckOptions::new().with_timeout(Duration::from_secs(5))))
        .await
        .unwrap();
    assert_eq!(*seen.grpc_timeout.lock().unwrap(), vec![true, false, true]);
}

#[tokio::test]
async fn retries_follow_the_configured_policy() {
    let mock = Mock {
        check_failure: Some(Code::Unavailable),
        ..Default::default()
    };
    let seen = mock.seen.clone();
    let channel = serve(mock).await;

    let disabled = SpiceDBClient::from_channel(channel.clone())
        .retries(RetryMode::Disabled)
        .build()
        .unwrap();
    let err = disabled.check(question()).await.unwrap_err();
    assert_eq!(err.code(), Code::Unavailable);
    assert_eq!(seen.calls.swap(0, Ordering::SeqCst), 1);

    let retried = Arc::new(AtomicUsize::new(0));
    let observed = retried.clone();
    let policy = RetryPolicy::new()
        .with_max_retries(2)
        .with_initial_backoff(Duration::from_millis(1))
        .with_on_retry(move |event| {
            assert_eq!(event.error.code(), Code::Unavailable);
            observed.fetch_add(1, Ordering::SeqCst);
        });
    let custom = SpiceDBClient::from_channel(channel.clone())
        .retry_policy(policy)
        .build()
        .unwrap();
    custom.check(question()).await.unwrap_err();
    assert_eq!(seen.calls.swap(0, Ordering::SeqCst), 3);
    assert_eq!(retried.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn a_policy_may_opt_into_resource_exhausted_for_reads() {
    let mock = Mock {
        check_failure: Some(Code::ResourceExhausted),
        ..Default::default()
    };
    let seen = mock.seen.clone();
    let channel = serve(mock).await;
    let default = SpiceDBClient::from_channel(channel.clone())
        .build()
        .unwrap();
    default.check(question()).await.unwrap_err();
    assert_eq!(
        seen.calls.swap(0, Ordering::SeqCst),
        1,
        "not retried by default"
    );

    let opted_in = SpiceDBClient::from_channel(channel)
        .retry_policy(
            RetryPolicy::new()
                .with_max_retries(1)
                .with_initial_backoff(Duration::from_millis(1))
                .with_retryable_codes(vec![Code::ResourceExhausted]),
        )
        .build()
        .unwrap();
    opted_in.check(question()).await.unwrap_err();
    assert_eq!(seen.calls.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn stream_establishment_retries_can_be_disabled_for_watch() {
    let mock = Mock::default();
    let seen = mock.seen.clone();
    let channel = serve(mock).await;
    let fast = RetryPolicy::new().with_initial_backoff(Duration::from_millis(1));

    let default = SpiceDBClient::from_channel(channel.clone())
        .retry_policy(fast.clone())
        .build()
        .unwrap();
    let mut stream = Box::pin(default.updates(UpdatesRequest::new(vec![])));
    assert!(stream.next().await.unwrap().is_err());
    assert_eq!(
        seen.calls.swap(0, Ordering::SeqCst),
        4,
        "1 attempt + 3 retries"
    );

    let supervised = SpiceDBClient::from_channel(channel)
        .retry_policy(fast)
        .stream_retries(StreamRetryMode::Disabled)
        .build()
        .unwrap();
    let mut stream =
        Box::pin(supervised.updates(UpdatesRequest::new(vec![]).with_options(WatchOptions::new())));
    assert!(stream.next().await.unwrap().is_err());
    assert_eq!(seen.calls.load(Ordering::SeqCst), 1);
}

// ---------------------------------------------------------------------------
// Per-call transport escape hatch.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn call_options_send_metadata_and_capture_the_response() {
    let mock = Mock::default();
    let seen = mock.seen.clone();
    let client = client(mock).await;
    let mut metadata = tonic::metadata::MetadataMap::new();
    metadata.insert("x-request-id", "req-42".parse().unwrap());
    let captured = ResponseMetadata::new();
    let _ = client
        .check(
            question().with_options(
                CheckOptions::new().with_call_options(
                    CallOptions::new()
                        .with_request_metadata(metadata)
                        .with_response_metadata(captured.clone()),
                ),
            ),
        )
        .await
        .unwrap();
    assert_eq!(
        *seen.custom_header.lock().unwrap(),
        vec![Some("req-42".to_string())]
    );
    assert_eq!(
        *seen.authorization.lock().unwrap(),
        vec![Some("Bearer t0ken".to_string())],
        "the client's credentials still apply"
    );
    assert_eq!(
        captured.headers().get("io.spicedb.debug.version").unwrap(),
        "v1.99.0"
    );
}

#[tokio::test]
async fn response_metadata_is_captured_on_failure_too() {
    let mock = Mock {
        check_failure: Some(Code::PermissionDenied),
        ..Default::default()
    };
    let client = client(mock).await;
    let captured = ResponseMetadata::new();
    client
        .check(
            question()
                .with_options(CheckOptions::new().with_call_options(
                    CallOptions::new().with_response_metadata(captured.clone()),
                )),
        )
        .await
        .unwrap_err();
    assert!(
        !captured.headers().is_empty(),
        "status metadata is captured"
    );
}

// ---------------------------------------------------------------------------
// Import: server abort status and batch size.
// ---------------------------------------------------------------------------

fn rels(n: usize) -> ImportRelationshipsRequest {
    ImportRelationshipsRequest::new(futures::stream::iter(
        (0..n).map(|i| Relationship::from_tuple(&format!("document:d{i}#viewer@user:alice"))),
    ))
}

#[tokio::test]
async fn import_reports_the_servers_abort_status() {
    let mock = Mock {
        abort_import: true,
        ..Default::default()
    };
    let client = client(mock).await;
    let err = client
        .import_relationships(rels(10).with_options(
            ImportRelationshipsOptions::new().with_batch_size(NonZeroU32::new(2).unwrap()),
        ))
        .await
        .unwrap_err();
    assert!(matches!(err, SpiceDBError::AlreadyExists(_)), "{err:?}");
    assert_eq!(
        err.server_message(),
        Some("could not CREATE one or more relationships, as they already existed")
    );
}

#[tokio::test]
async fn import_batch_size_sets_relationships_per_message() {
    let mock = Mock::default();
    let seen = mock.seen.clone();
    let client = client(mock).await;
    let result = client
        .import_relationships(rels(5).with_options(
            ImportRelationshipsOptions::new().with_batch_size(NonZeroU32::new(2).unwrap()),
        ))
        .await
        .unwrap();
    assert_eq!(result.imported_count, 5);
    assert_eq!(*seen.import_messages.lock().unwrap(), vec![2, 2, 1]);
    assert_eq!(seen.calls.load(Ordering::SeqCst), 1, "one call, one stream");
}

// ---------------------------------------------------------------------------
// Bulk check per-item errors.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn bulk_item_errors_can_be_returned_per_item() {
    let client = client(Mock::default()).await;
    let request = BulkCheckRequest::new(vec![question(), question(), question()]);
    let err = client.check_bulk(request.clone()).await.unwrap_err();
    assert!(
        matches!(err, SpiceDBError::BulkItem { index: 1, .. }),
        "{err:?}"
    );

    let results = client
        .check_bulk(
            request
                .with_options(CheckOptions::new().with_bulk_item_errors(BulkItemErrors::Returned)),
        )
        .await
        .unwrap();
    assert!(results[0].has_permission() && results[2].has_permission());
    assert!(!results[1].has_permission());
    let item_err = results[1].error.as_ref().expect("item 1 carries its error");
    assert!(matches!(item_err, SpiceDBError::BulkItem { index: 1, .. }));
    assert_eq!(item_err.code(), Code::NotFound);
}

// ---------------------------------------------------------------------------
// All-or-nothing delete and delete-all progress.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn all_or_nothing_delete_may_use_the_server_default_limit() {
    let mock = Mock::default();
    let seen = mock.seen.clone();
    let client = client(mock).await;

    let err = client
        .delete_relationships(DeleteRequest::server_default_limit(Filter::new("document")))
        .await
        .unwrap_err();
    assert!(matches!(err, SpiceDBError::InvalidArgument(_)));
    assert!(
        seen.deletes.lock().unwrap().is_empty(),
        "rejected before any RPC"
    );

    client
        .delete_relationships(
            DeleteRequest::server_default_limit(Filter::new("document"))
                .with_options(DeleteOptions::new().with_partial(DeletePartial::Disallowed)),
        )
        .await
        .unwrap();
    let sent = seen.deletes.lock().unwrap()[0].clone();
    assert_eq!(sent.optional_limit, 0);
    assert!(!sent.optional_allow_partial_deletions);
}

#[tokio::test]
async fn delete_all_reports_cumulative_progress_per_batch() {
    let client = client(Mock::default()).await;
    let progress = Arc::new(Mutex::new(Vec::new()));
    let sink = progress.clone();
    let result = client
        .delete_all_relationships_non_atomic(
            DeleteAllRequest::new(Filter::new("document")).with_options(
                DeleteAllOptions::new().with_progress(move |p| {
                    sink.lock().unwrap().push((
                        p.deleted_count,
                        p.revision.as_ref().to_string(),
                        p.cursor.clone(),
                    ))
                }),
            ),
        )
        .await
        .unwrap();
    assert_eq!(result.deleted_count, 30);
    assert_eq!(
        *progress.lock().unwrap(),
        vec![
            (10, "rev-1".into(), Some("c1".into())),
            (20, "rev-2".into(), Some("c2".into())),
            (30, "rev-3".into(), Some("c3".into())),
        ]
    );
}

// ---------------------------------------------------------------------------
// Error interop with tonic::Status.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn local_errors_convert_to_matching_statuses() {
    let client = client(Mock {
        check_stall: Some(Duration::from_millis(200)),
        ..Default::default()
    })
    .await;
    // Local validation.
    let err = client
        .check(PermissionCheck::new(
            ObjectRef::new("document", "d0").unwrap(),
            "",
            question().subject().clone(),
            consistency::full(),
        ))
        .await
        .unwrap_err();
    assert_eq!(tonic::Status::from(&err).code(), Code::InvalidArgument);
    assert_eq!(code_of(&err), Code::InvalidArgument);

    // Locally elapsed deadline.
    let err = client
        .check(question().with_options(CheckOptions::new().with_timeout(Duration::from_millis(20))))
        .await
        .unwrap_err();
    assert_eq!(err.code(), Code::DeadlineExceeded, "{err:?}");
    assert_eq!(tonic::Status::from(err).code(), Code::DeadlineExceeded);
}

#[test]
fn constructed_errors_behave_like_server_errors() {
    for code in [
        Code::NotFound,
        Code::AlreadyExists,
        Code::FailedPrecondition,
        Code::PermissionDenied,
        Code::Unauthenticated,
        Code::Unavailable,
        Code::Aborted,
        Code::Internal,
    ] {
        let err = SpiceDBError::new(code, "boom");
        assert_eq!(err.code(), code);
        assert_eq!(err.server_message(), Some("boom"));
        let status = tonic::Status::from(err.clone());
        assert_eq!(status.code(), code);
        assert_eq!(status.message(), "boom");
        // And back: a raw status classifies and converts identically.
        assert_eq!(code_of(&status), code);
        let round: SpiceDBError = status.into();
        assert_eq!(round.code(), code);
    }
    // code_of walks source chains.
    let wrapped = SpiceDBError::BulkItem {
        index: 3,
        source: Box::new(SpiceDBError::new(Code::NotFound, "x")),
    };
    assert_eq!(code_of(&wrapped), Code::NotFound);
    assert_eq!(code_of(&std::io::Error::other("x")), Code::Unknown);
}

// ---------------------------------------------------------------------------
// Per-call retry policy.
// ---------------------------------------------------------------------------

fn retry_once() -> CallOptions {
    CallOptions::new().with_retry_policy(
        RetryPolicy::new()
            .with_max_retries(1)
            .with_retryable_codes(vec![Code::Unavailable])
            .with_initial_backoff(Duration::from_millis(1)),
    )
}

fn touch() -> Transaction {
    let mut txn = Transaction::new();
    txn.touch(&Relationship::from_tuple("document:a#viewer@user:alice").unwrap());
    txn
}

#[tokio::test]
async fn mutations_are_not_retried_unless_an_idempotent_call_opts_in() {
    // Never retried by default.
    let mock = Mock {
        write_failure: Some(Code::Unavailable),
        ..Default::default()
    };
    let seen = mock.seen.clone();
    let c = client(mock).await;
    let err = c.write(WriteRequest::new(touch())).await.unwrap_err();
    assert_eq!(err.code(), Code::Unavailable);
    assert_eq!(seen.writes.load(Ordering::SeqCst), 1);

    // A touch-only write may opt in for one call.
    let mock = Mock {
        write_failure: Some(Code::Unavailable),
        ..Default::default()
    };
    let seen = mock.seen.clone();
    let c = client(mock).await;
    let written = c
        .write(
            WriteRequest::new(touch())
                .with_options(WriteOptions::new().with_call_options(retry_once())),
        )
        .await
        .unwrap();
    assert_eq!(written.revision.as_ref(), "w");
    assert_eq!(seen.writes.load(Ordering::SeqCst), 2);

    // Creates and preconditioned writes refuse the opt-in before any request.
    let mut create = Transaction::new();
    create.create(&Relationship::from_tuple("document:a#viewer@user:alice").unwrap());
    let mut guarded = touch();
    guarded.must_match(Filter::new("document"));
    for txn in [create, guarded] {
        let before = seen.calls.load(Ordering::SeqCst);
        let err = c
            .write(
                WriteRequest::new(txn)
                    .with_options(WriteOptions::new().with_call_options(retry_once())),
            )
            .await
            .unwrap_err();
        assert!(matches!(err, SpiceDBError::InvalidArgument(_)), "{err:?}");
        assert_eq!(seen.calls.load(Ordering::SeqCst), before, "no request sent");
    }

    // Every other mutation refuses it too, before any request.
    let before = seen.calls.load(Ordering::SeqCst);
    let filter = Filter::new("document");
    let delete = c
        .delete_relationships(
            DeleteRequest::new(filter.clone(), NonZeroU32::new(1).unwrap())
                .with_options(DeleteOptions::new().with_call_options(retry_once())),
        )
        .await
        .unwrap_err();
    let delete_all = c
        .delete_all_relationships_non_atomic(
            DeleteAllRequest::new(filter)
                .with_options(DeleteAllOptions::new().with_call_options(retry_once())),
        )
        .await
        .unwrap_err();
    let import = c
        .import_relationships(
            rels(3).with_options(ImportRelationshipsOptions::new().with_call_options(retry_once())),
        )
        .await
        .unwrap_err();
    for err in [delete, delete_all, import] {
        assert!(matches!(err, SpiceDBError::InvalidArgument(_)), "{err:?}");
    }
    assert_eq!(seen.calls.load(Ordering::SeqCst), before, "no request sent");

    // A zero-retry policy is not an opt-in, so mutations accept it.
    let zero = CallOptions::new().with_retry_policy(RetryPolicy::new().with_max_retries(0));
    c.delete_relationships(
        DeleteRequest::new(Filter::new("document"), NonZeroU32::new(1).unwrap())
            .with_options(DeleteOptions::new().with_call_options(zero)),
    )
    .await
    .unwrap();

    // Schema writes are idempotent and may opt in.
    let schema = c
        .write_schema(
            WriteSchemaRequest::new("definition user {}")
                .with_options(WriteSchemaOptions::new().with_call_options(retry_once())),
        )
        .await
        .unwrap();
    assert_eq!(schema.revision.as_ref(), "s");
    assert_eq!(seen.schema_writes.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn a_per_call_policy_overrides_the_clients_including_streams() {
    let mock = Mock::default();
    let seen = mock.seen.clone();
    let channel = serve(mock).await;
    let fast = RetryPolicy::new().with_initial_backoff(Duration::from_millis(1));

    // One watch is exempted while the client keeps retrying everything else.
    let c = SpiceDBClient::from_channel(channel.clone())
        .retry_policy(fast.clone())
        .build()
        .unwrap();
    let once = CallOptions::new().with_retry_policy(RetryPolicy::new().with_max_retries(0));
    let mut stream = Box::pin(c.updates(
        UpdatesRequest::new(vec![]).with_options(WatchOptions::new().with_call_options(once)),
    ));
    assert!(stream.next().await.unwrap().is_err());
    assert_eq!(seen.calls.swap(0, Ordering::SeqCst), 1);

    // And a client with retries and stream retries disabled can retry one call.
    let c = SpiceDBClient::from_channel(channel)
        .retries(RetryMode::Disabled)
        .stream_retries(StreamRetryMode::Disabled)
        .build()
        .unwrap();
    let twice = CallOptions::new().with_retry_policy(fast.with_max_retries(1));
    let mut stream = Box::pin(c.updates(
        UpdatesRequest::new(vec![]).with_options(WatchOptions::new().with_call_options(twice)),
    ));
    assert!(stream.next().await.unwrap().is_err());
    assert_eq!(seen.calls.load(Ordering::SeqCst), 2);

    // An invalid per-call policy fails before any request.
    let bad = CallOptions::new().with_retry_policy(
        RetryPolicy::new()
            .with_max_retries(1)
            .with_initial_backoff(Duration::ZERO),
    );
    let err = c
        .check(question().with_options(CheckOptions::new().with_call_options(bad)))
        .await
        .unwrap_err();
    assert!(matches!(err, SpiceDBError::InvalidArgument(_)), "{err:?}");
    assert_eq!(seen.calls.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn an_empty_write_is_sent_to_the_server() {
    let mock = Mock::default();
    let seen = mock.seen.clone();
    let written = client(mock)
        .await
        .write(WriteRequest::new(Transaction::new()))
        .await
        .unwrap();
    assert_eq!(written.revision.as_ref(), "w");
    assert_eq!(seen.writes.load(Ordering::SeqCst), 1);
}

// ---------------------------------------------------------------------------
// Single-request paging.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn single_request_paging_sends_exactly_one_request() {
    let mock = Mock {
        rows: 5,
        ..Default::default()
    };
    let seen = mock.seen.clone();
    let c = client(mock).await;
    let subject = question().subject().clone();
    let lookup = |options: LookupResourcesOptions| {
        c.lookup_resources(
            LookupResourcesRequest::new("document", "view", subject.clone(), consistency::full())
                .with_options(options),
        )
        .map(|r| r.unwrap().after_result_cursor.unwrap())
        .collect::<Vec<_>>()
    };
    let cursors = lookup(
        LookupResourcesOptions::new()
            .with_paging(Paging::SingleRequest)
            .with_page_size(NonZeroU32::new(2).unwrap()),
    )
    .await;
    assert_eq!(cursors, vec!["1", "2", "3", "4", "5"]);
    {
        let paged = seen.paged.lock().unwrap();
        assert_eq!(paged.len(), 1, "exactly one request");
        assert_eq!(paged[0].1, 0, "no limit means unbounded; page size ignored");
    }
    seen.paged.lock().unwrap().clear();

    // With a limit, the request carries it and nothing more is fetched.
    let read: Vec<_> = c
        .read_relationships(
            ReadRelationshipsRequest::new(Filter::new("document"), consistency::full())
                .with_options(
                    ReadRelationshipsOptions::new()
                        .with_paging(Paging::SingleRequest)
                        .with_limit(3),
                ),
        )
        .map(|r| r.unwrap())
        .collect()
        .await;
    assert_eq!(read.len(), 3);
    let paged = seen.paged.lock().unwrap();
    assert_eq!(paged.len(), 1);
    assert_eq!(paged[0].1, 3);
}

// ---------------------------------------------------------------------------
// Wrapper errors report the server's status once.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn wrapper_errors_report_the_server_message_once() {
    let c = client(Mock {
        fail_second_delete: true,
        ..Default::default()
    })
    .await;
    let results = c
        .check_bulk(
            BulkCheckRequest::new(vec![question(), question()])
                .with_options(CheckOptions::new().with_bulk_item_errors(BulkItemErrors::Returned)),
        )
        .await
        .unwrap();
    let item = results[1].error.clone().unwrap();
    assert_eq!(
        item.to_string(),
        "check item 1: not found: object definition `nope` not found",
        "Display keeps full context"
    );
    let status = tonic::Status::from(&item);
    assert_eq!(status.code(), Code::NotFound);
    assert_eq!(status.message(), "object definition `nope` not found");
    assert_eq!(
        item.server_message(),
        Some("object definition `nope` not found")
    );

    let err = c
        .delete_all_relationships_non_atomic(DeleteAllRequest::new(Filter::new("document")))
        .await
        .unwrap_err();
    assert!(
        matches!(err, SpiceDBError::DeleteProgress { .. }),
        "{err:?}"
    );
    let status = err.to_status();
    assert_eq!(status.code(), Code::Unavailable);
    assert_eq!(status.message(), "datastore gone");
}

// ---------------------------------------------------------------------------
// Protocol conversions reproduce the server's responses.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn check_bulk_round_trips_the_servers_response() {
    let c = client(Mock::default()).await;
    let mut context = std::collections::HashMap::new();
    context.insert("cidr".to_string(), serde_json::json!("10.0.0.0/8"));
    let request = BulkCheckRequest::new(vec![question(), question(), question()]).with_options(
        CheckOptions::new()
            .with_bulk_item_errors(BulkItemErrors::Returned)
            .with_context(context),
    );
    let results = c.check_bulk(request.clone()).await.unwrap();
    let got = spicedb::protoconv::check_bulk_to_proto(&request, &results).unwrap();

    // What the mock sent: each request item echoed, item 1 an error.
    let mut want = proto::CheckBulkPermissionsResponse {
        checked_at: token("rev-1"),
        pairs: vec![],
    };
    for i in 0..3 {
        let mut item = spicedb::protoconv::check_request_to_proto(&question()).unwrap();
        item.context = got.pairs[i].request.as_ref().unwrap().context.clone();
        assert!(item.context.is_some(), "call context is merged into items");
        want.pairs.push(proto::CheckBulkPermissionsPair {
            request: Some(item),
            response: Some(if i == 1 {
                proto::check_bulk_permissions_pair::Response::Error(
                    spicedb_proto::google::rpc::Status {
                        code: Code::NotFound as i32,
                        message: "object definition `nope` not found".into(),
                        details: vec![],
                    },
                )
            } else {
                proto::check_bulk_permissions_pair::Response::Item(
                    proto::CheckBulkPermissionsResponseItem {
                        permissionship: 2,
                        ..Default::default()
                    },
                )
            }),
        });
    }
    assert_eq!(got, want);

    let err = spicedb::protoconv::check_bulk_to_proto(&request, &results[..1]).unwrap_err();
    assert!(matches!(err, SpiceDBError::InvalidArgument(_)), "{err:?}");
}

#[tokio::test]
async fn lookup_subjects_round_trips_the_servers_response() {
    let c = client(Mock::default()).await;
    let got: Vec<_> = c
        .lookup_subjects(LookupSubjectsRequest::new(
            ObjectRef::new("document", "d0").unwrap(),
            "view",
            "user",
            consistency::full(),
        ))
        .map(|s| spicedb::protoconv::lookup_subject_to_proto(&s.unwrap()))
        .collect()
        .await;
    assert_eq!(got, lookup_subjects_responses());
}
