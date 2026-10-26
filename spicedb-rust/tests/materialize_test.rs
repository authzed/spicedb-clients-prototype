#![allow(clippy::result_large_err)] // tonic Status is the generated service error type.
use futures::{Stream, StreamExt};
use spicedb::*;
use spicedb_proto::authzed::api::materialize::v0 as p;
use spicedb_proto::authzed::api::v1 as v;
use std::pin::Pin;
use std::sync::{Arc, Mutex};
use tonic::{Request, Response, Status};
type Events<T> = Pin<Box<dyn Stream<Item = Result<T, Status>> + Send>>;
#[derive(Clone, Default)]
struct Service(Arc<Mutex<Vec<String>>>);
impl Service {
    fn capture<T>(&self, r: &Request<T>, method: &str) {
        assert_eq!(r.metadata().get("authorization").unwrap(), "Bearer token");
        self.0.lock().unwrap().push(method.into());
    }
}
fn token() -> Option<v::ZedToken> {
    Some(v::ZedToken {
        token: "revision".into(),
    })
}
fn change() -> p::PermissionSetChange {
    p::PermissionSetChange {
        at_revision: token(),
        operation: 999,
        parent_set: Some(p::SetReference {
            object_type: "document".into(),
            object_id: "one".into(),
            permission_or_relation: "view".into(),
        }),
        child: Some(p::permission_set_change::Child::ChildMember(
            p::MemberReference {
                object_type: "user".into(),
                object_id: "alice".into(),
                optional_permission_or_relation: "member".into(),
            },
        )),
    }
}
#[tonic::async_trait]
impl p::relationships_service_server::RelationshipsService for Service {
    async fn experimental_count_relationships_by_filter(
        &self,
        r: Request<p::ExperimentalCountRelationshipsByFilterRequest>,
    ) -> Result<Response<p::ExperimentalCountRelationshipsByFilterResponse>, Status> {
        self.capture(&r, "count");
        assert!(r
            .get_ref()
            .relationship_filter
            .as_ref()
            .unwrap()
            .optional_subject_filter
            .as_ref()
            .unwrap()
            .optional_relation
            .is_some());
        Ok(Response::new(
            p::ExperimentalCountRelationshipsByFilterResponse {
                relationship_count: 7,
                read_at: token(),
            },
        ))
    }
}
#[tonic::async_trait]
impl p::watch_permission_sets_service_server::WatchPermissionSetsService for Service {
    type WatchPermissionSetsStream = Events<p::WatchPermissionSetsResponse>;
    async fn watch_permission_sets(
        &self,
        r: Request<p::WatchPermissionSetsRequest>,
    ) -> Result<Response<Self::WatchPermissionSetsStream>, Status> {
        self.capture(&r, "watch");
        assert_eq!(
            r.get_ref().optional_starting_after.as_ref().unwrap().token,
            "resume"
        );
        use p::watch_permission_sets_response::Response::*;
        let events = vec![
            Change(change()),
            CompletedRevision(token().unwrap()),
            LookupPermissionSetsRequired(p::LookupPermissionSetsRequired {
                required_lookup_at: token(),
            }),
            BreakingSchemaChange(p::BreakingSchemaChange {
                change_at: token(),
                affected_permissions: vec![p::WatchedPermission {
                    resource_type: "document".into(),
                    permission: "view".into(),
                    subject_type: "user".into(),
                    optional_subject_relation: "member".into(),
                }],
            }),
        ];
        Ok(Response::new(Box::pin(futures::stream::iter(
            events.into_iter().map(|response| {
                Ok(p::WatchPermissionSetsResponse {
                    response: Some(response),
                })
            }),
        ))))
    }
    type LookupPermissionSetsStream = Events<p::LookupPermissionSetsResponse>;
    async fn lookup_permission_sets(
        &self,
        r: Request<p::LookupPermissionSetsRequest>,
    ) -> Result<Response<Self::LookupPermissionSetsStream>, Status> {
        self.capture(&r, "lookup");
        assert_eq!(r.get_ref().limit, 10);
        Ok(Response::new(Box::pin(futures::stream::iter(vec![Ok(
            p::LookupPermissionSetsResponse {
                change: Some(change()),
                cursor: Some(p::Cursor {
                    limit: 10,
                    token: token(),
                    starting_index: 4,
                    completed_members: true,
                    starting_key: "key".into(),
                    cursor: "opaque".into(),
                }),
            },
        )]))))
    }
    async fn download_permission_sets(
        &self,
        r: Request<p::DownloadPermissionSetsRequest>,
    ) -> Result<Response<p::DownloadPermissionSetsResponse>, Status> {
        self.capture(&r, "download");
        assert_eq!(
            r.get_ref().optional_at_revision.as_ref().unwrap().token,
            "wanted"
        );
        Ok(Response::new(p::DownloadPermissionSetsResponse {
            files: vec![p::File {
                name: "file.avro".into(),
                url: "https://example.test/file".into(),
            }],
            timestamp: Some(prost_types::Timestamp {
                seconds: 1_700_000_000,
                nanos: 456,
            }),
            at_revision: token(),
        }))
    }
}
#[tokio::test]
async fn every_additional_materialize_rpc_uses_authenticated_channel_and_native_values() {
    let service = Service::default();
    let calls = service.0.clone();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let task = tokio::spawn(
        tonic::transport::Server::builder()
            .add_service(
                p::relationships_service_server::RelationshipsServiceServer::new(service.clone()),
            )
            .add_service(
                p::watch_permission_sets_service_server::WatchPermissionSetsServiceServer::new(
                    service,
                ),
            )
            .serve_with_incoming(tokio_stream::wrappers::TcpListenerStream::new(listener)),
    );
    let client = SpiceDBClient::new_plaintext(
        address.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .unwrap();
    let count = client
        .experimental_count_relationships_by_filter(
            ExperimentalCountRelationshipsByFilterRequest::new(
                Filter::new("document")
                    .with_subject_type("user")
                    .with_subject_relation(SubjectRelationFilter::Direct),
            ),
        )
        .await
        .unwrap();
    assert_eq!(count.relationship_count, 7);
    assert_eq!(count.revision.as_ref(), "revision");
    let events: Vec<_> =
        client
            .watch_permission_sets(WatchPermissionSetsRequest::new().with_options(
                WatchPermissionSetsOptions::new().with_start_revision("resume".into()),
            ))
            .collect()
            .await;
    assert_eq!(events.len(), 4);
    let PermissionSetEvent::Change(change) = events[0].as_ref().unwrap() else {
        panic!()
    };
    assert_eq!(change.operation, SetOperation::Unspecified);
    assert_eq!(
        change.parent_set.as_ref().unwrap().object.object_id(),
        "one"
    );
    let Some(PermissionSetChild::Member(member)) = &change.child else {
        panic!()
    };
    assert_eq!(member.permission_or_relation.as_deref(), Some("member"));
    assert!(matches!(
        events[1],
        Ok(PermissionSetEvent::CompletedRevision(_))
    ));
    assert!(matches!(
        events[2],
        Ok(PermissionSetEvent::LookupRequired { .. })
    ));
    assert!(matches!(
        events[3],
        Ok(PermissionSetEvent::BreakingSchemaChange { .. })
    ));
    let page: Vec<_> = client
        .lookup_permission_sets(LookupPermissionSetsRequest::new(
            std::num::NonZeroU32::new(10).unwrap(),
        ))
        .collect()
        .await;
    let cursor = page[0].as_ref().unwrap().cursor.as_ref().unwrap();
    assert!(cursor.completed_members);
    assert_eq!(cursor.starting_index, 4);
    assert_eq!(cursor.starting_key, "key");
    assert_eq!(cursor.cursor, "opaque");
    assert_eq!(cursor.revision.as_ref().unwrap().as_ref(), "revision");
    let download =
        client
            .download_permission_sets(DownloadPermissionSetsRequest::new().with_options(
                DownloadPermissionSetsOptions::new().with_at_revision("wanted".into()),
            ))
            .await
            .unwrap();
    assert_eq!(download.files[0].name, "file.avro");
    assert_eq!(download.timestamp.unwrap().timestamp_subsec_nanos(), 456);
    assert_eq!(download.revision.as_ref(), "revision");
    assert_eq!(
        *calls.lock().unwrap(),
        vec!["count", "watch", "lookup", "download"]
    );
    task.abort();
}
