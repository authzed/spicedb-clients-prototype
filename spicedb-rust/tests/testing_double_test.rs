//! spicedb::testing::InMemorySpiceDB: a real SpiceDBClient over an in-memory
//! channel, with SpiceDB's write/read/delete/check semantics.

use std::num::NonZeroU32;

use futures::StreamExt;
use spicedb::testing::{
    cursor_after, server_error, server_error_with_reason, FaultStage, InMemorySpiceDB,
    SchemaRevision,
};
use spicedb::tonic::Code;
use spicedb::{
    consistency, BulkCheckRequest, CallOptions, DeleteOptions, DeletePartial, DeleteRequest,
    ExportRelationshipsOptions, ExportRelationshipsRequest, Filter, ImportRelationshipsOptions,
    ImportRelationshipsRequest, Operation, Paging, PermissionCheck, Permissionship,
    ReadRelationshipsOptions, ReadRelationshipsRequest, ReadSchemaRequest, ReflectSchemaRequest,
    ReflectSchemaResult, Relationship, ResponseMetadata, SchemaDefinition, SpiceDBError,
    Transaction, WriteRequest, WriteSchemaOptions, WriteSchemaRequest,
};

fn rel(s: &str) -> Relationship {
    s.parse().expect("valid tuple")
}

#[tokio::test]
async fn writes_apply_preconditions_and_create_conflicts_atomically() {
    let fake = InMemorySpiceDB::with_relationships([rel("document:a#viewer@user:alice")]);
    let client = fake.client().unwrap();

    // CREATE of an existing relationship fails and applies nothing else.
    let mut txn = Transaction::new();
    txn.touch(&rel("document:b#viewer@user:bob"));
    txn.create(&rel("document:a#viewer@user:alice"));
    let err = client.write(WriteRequest::new(txn)).await.unwrap_err();
    assert!(matches!(err, SpiceDBError::AlreadyExists(_)), "{err:?}");
    assert_eq!(
        err.reason(),
        Some("ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP")
    );
    assert_eq!(fake.relationships().len(), 1);

    // A failing precondition fails the write.
    let mut txn = Transaction::new();
    txn.must_not_match(Filter::new("document").with_resource_id("a"));
    txn.touch(&rel("document:b#viewer@user:bob"));
    let err = client.write(WriteRequest::new(txn)).await.unwrap_err();
    assert!(
        matches!(err, SpiceDBError::FailedPrecondition(_)),
        "{err:?}"
    );

    // A satisfied precondition applies the whole transaction.
    let mut txn = Transaction::new();
    txn.must_match(Filter::new("document").with_resource_id("a"));
    txn.touch(&rel("document:b#viewer@user:bob"));
    txn.delete(&rel("document:a#viewer@user:alice"));
    let written = client.write(WriteRequest::new(txn)).await.unwrap();
    assert!(!written.revision.as_ref().is_empty());
    assert_eq!(
        fake.relationships(),
        vec![rel("document:b#viewer@user:bob")]
    );
}

#[tokio::test]
async fn reads_filter_and_page_with_cursors() {
    let fake = InMemorySpiceDB::with_relationships(
        (0..5)
            .map(|i| rel(&format!("document:d{i}#viewer@user:alice")))
            .chain([rel("document:x#viewer@team:eng#member")]),
    );
    let client = fake.client().unwrap();
    let request = ReadRelationshipsRequest::new(
        Filter::new("document").with_subject_type("user"),
        consistency::full(),
    )
    .with_options(ReadRelationshipsOptions::new().with_page_size(NonZeroU32::new(2).unwrap()));
    let items: Vec<_> = client
        .read_relationships(request.clone())
        .map(|r| r.unwrap())
        .collect()
        .await;
    assert_eq!(items.len(), 5, "three pages, team subject filtered out");
    assert!(items.iter().all(|i| !i.read_at.as_ref().is_empty()));

    // Resume after the second item.
    let resumed: Vec<_> = client
        .read_relationships(
            request.with_options(
                ReadRelationshipsOptions::new()
                    .with_page_size(NonZeroU32::new(2).unwrap())
                    .with_cursor(items[1].after_result_cursor.clone().unwrap()),
            ),
        )
        .map(|r| r.unwrap().relationship.resource_id().to_string())
        .collect()
        .await;
    assert_eq!(resumed, vec!["d2", "d3", "d4"]);
}

#[tokio::test]
async fn deletes_are_bounded_or_all_or_nothing() {
    let fake = InMemorySpiceDB::with_relationships(
        (0..3).map(|i| rel(&format!("document:d{i}#viewer@user:alice"))),
    );
    let client = fake.client().unwrap();
    let filter = Filter::new("document");

    let err = client
        .delete_relationships(
            DeleteRequest::new(filter.clone(), NonZeroU32::new(2).unwrap())
                .with_options(DeleteOptions::new().with_partial(DeletePartial::Disallowed)),
        )
        .await
        .unwrap_err();
    assert!(matches!(err, SpiceDBError::InvalidArgument(_)), "{err:?}");
    assert_eq!(
        err.reason(),
        Some("ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE")
    );
    assert_eq!(
        fake.relationships().len(),
        3,
        "all-or-nothing deleted nothing"
    );

    let partial = client
        .delete_relationships(DeleteRequest::new(
            filter.clone(),
            NonZeroU32::new(2).unwrap(),
        ))
        .await
        .unwrap();
    assert_eq!(partial.deleted_count, 2);
    assert!(!partial.complete);

    let rest = client
        .delete_relationships(
            DeleteRequest::server_default_limit(filter)
                .with_options(DeleteOptions::new().with_partial(DeletePartial::Disallowed)),
        )
        .await
        .unwrap();
    assert_eq!(rest.deleted_count, 1);
    assert!(rest.complete);
    assert!(fake.relationships().is_empty());
}

#[tokio::test]
async fn checks_return_registered_results() {
    let fake = InMemorySpiceDB::new();
    let client = fake.client().unwrap();
    let r = rel("document:a#viewer@user:alice");
    fake.set_check_result(
        r.resource(),
        "view",
        r.subject(),
        Permissionship::HasPermission,
    );
    let question = |subject: &str| {
        PermissionCheck::new(
            r.resource().clone(),
            "view",
            subject.parse().unwrap(),
            consistency::full(),
        )
    };
    assert!(client
        .check(question("user:alice"))
        .await
        .unwrap()
        .has_permission());
    let results = client
        .check_bulk(BulkCheckRequest::new(vec![
            question("user:bob"),
            question("user:alice"),
        ]))
        .await
        .unwrap();
    assert_eq!(results[0].permissionship, Permissionship::NoPermission);
    assert_eq!(results[1].permissionship, Permissionship::HasPermission);
}

#[tokio::test]
async fn errors_have_the_real_clients_shape() {
    let fake = InMemorySpiceDB::with_relationships([rel("org:acme#admin@user:alice")]);
    let client = fake.client().unwrap();

    // A server failure is mapped by the real client: server message kept.
    let mut txn = Transaction::new();
    txn.must_match(Filter::new("org").with_relation("owner"));
    let err = client.write(WriteRequest::new(txn)).await.unwrap_err();
    assert!(
        matches!(err, SpiceDBError::FailedPrecondition(_)),
        "{err:?}"
    );
    let message = err.server_message().expect("a server error");
    assert!(message.starts_with("unable to satisfy write precondition `"));
    assert_eq!(err.to_string(), format!("failed precondition: {message}"));

    // Local validation fails in the client, before any request.
    let err = Box::pin(client.read_relationships(ReadRelationshipsRequest::new(
        Filter::new(""),
        consistency::full(),
    )))
    .next()
    .await
    .unwrap()
    .unwrap_err();
    assert!(matches!(err, SpiceDBError::InvalidArgument(_)), "{err:?}");
    assert_eq!(err.server_message(), None);

    // An empty write checks its preconditions and returns the revision.
    let before = fake.relationships();
    let written = client
        .write(WriteRequest::new(Transaction::new()))
        .await
        .unwrap();
    assert!(!written.revision.as_ref().is_empty());
    assert_eq!(fake.relationships(), before);
}

fn relationships(tuples: &[&str]) -> ImportRelationshipsRequest {
    let items: Vec<Result<Relationship, SpiceDBError>> =
        tuples.iter().map(|t| Ok(rel(t))).collect();
    ImportRelationshipsRequest::new(futures::stream::iter(items))
}

#[tokio::test]
async fn imports_exports_and_schema_operations() {
    let fake = InMemorySpiceDB::new();
    let client = fake.client().unwrap();
    let imported = client
        .import_relationships(relationships(&[
            "doc:a#viewer@user:x",
            "doc:b#viewer@user:x",
            "doc:c#viewer@user:x",
            "folder:f#viewer@user:x",
        ]))
        .await
        .unwrap();
    assert_eq!(imported.imported_count, 4);

    // An import is one transaction.
    let err = client
        .import_relationships(relationships(&[
            "doc:z#viewer@user:x",
            "doc:a#viewer@user:x",
        ]))
        .await
        .unwrap_err();
    assert!(matches!(err, SpiceDBError::AlreadyExists(_)), "{err:?}");
    assert_eq!(
        fake.relationships().len(),
        4,
        "a failed import adds nothing"
    );

    // Export honors the filter, limits and cursors.
    let export = |options: ExportRelationshipsOptions| {
        client
            .export_relationships(
                ExportRelationshipsRequest::new(consistency::full()).with_options(options),
            )
            .map(|r| r.unwrap())
            .collect::<Vec<_>>()
    };
    let folders =
        export(ExportRelationshipsOptions::new().with_filter(Filter::new("folder"))).await;
    assert_eq!(folders.len(), 1);
    assert_eq!(folders[0].relationship, rel("folder:f#viewer@user:x"));
    let first = export(
        ExportRelationshipsOptions::new()
            .with_filter(Filter::new("doc"))
            .with_limit(2),
    )
    .await;
    assert_eq!(first.len(), 2);
    let rest = export(
        ExportRelationshipsOptions::new()
            .with_filter(Filter::new("doc"))
            .with_cursor(first[1].after_result_cursor.clone().unwrap()),
    )
    .await;
    assert_eq!(rest.len(), 1);
    assert_eq!(rest[0].relationship.resource_id(), "c");

    // Schema reads and writes.
    let err = client
        .read_schema(ReadSchemaRequest::new())
        .await
        .unwrap_err();
    assert!(matches!(err, SpiceDBError::NotFound(_)), "{err:?}");
    client
        .write_schema(WriteSchemaRequest::new("definition user {}"))
        .await
        .unwrap();
    let schema = client.read_schema(ReadSchemaRequest::new()).await.unwrap();
    assert_eq!(schema.schema_text, "definition user {}");
    assert_eq!(fake.schema().as_deref(), Some("definition user {}"));

    // Reflection returns the registered result.
    let reflect = ReflectSchemaRequest::new(consistency::min_latency());
    let err = client.reflect_schema(reflect.clone()).await.unwrap_err();
    assert!(matches!(err, SpiceDBError::NotFound(_)), "{err:?}");
    let mut user = SchemaDefinition::default();
    user.name = "user".into();
    let mut registered = ReflectSchemaResult::default();
    registered.definitions.push(user);
    fake.set_reflect_schema_result(registered);
    let reflected = client.reflect_schema(reflect).await.unwrap();
    assert_eq!(reflected.definitions[0].name, "user");
    assert!(!reflected.revision.as_ref().is_empty());
}

#[tokio::test]
async fn response_metadata_reaches_the_clients_capture() {
    let fake = InMemorySpiceDB::new();
    let mut headers = spicedb::tonic::metadata::MetadataMap::new();
    headers.insert("io.spicedb.respmeta.version", "v1.99".parse().unwrap());
    fake.set_response_metadata(headers);
    let client = fake.client().unwrap();
    let captured = ResponseMetadata::new();
    client
        .write_schema(
            WriteSchemaRequest::new("definition user {}")
                .with_options(WriteSchemaOptions::new().with_call_options(
                    CallOptions::new().with_response_metadata(captured.clone()),
                )),
        )
        .await
        .unwrap();
    assert_eq!(
        captured
            .headers()
            .get("io.spicedb.respmeta.version")
            .unwrap(),
        "v1.99"
    );
}

fn tuples(n: usize) -> ImportRelationshipsRequest {
    ImportRelationshipsRequest::new(futures::stream::iter(
        (0..n).map(|i| Ok(rel(&format!("doc:d{i}#viewer@user:x")))),
    ))
}

#[tokio::test]
async fn an_import_batch_fault_applies_nothing() {
    let fake = InMemorySpiceDB::new();
    let client = fake.client().unwrap();
    // A restore test: the third batch fails with AlreadyExists.
    fake.set_fault_hook(|f| match (f.call.operation(), f.stage, f.item) {
        (Operation::ImportRelationships, FaultStage::ImportBatch, 2) => {
            Some(server_error(Code::AlreadyExists, "duplicate relationship"))
        }
        _ => None,
    });
    let err = client
        .import_relationships(tuples(10).with_options(
            ImportRelationshipsOptions::new().with_batch_size(NonZeroU32::new(3).unwrap()),
        ))
        .await
        .unwrap_err();
    assert!(matches!(err, SpiceDBError::AlreadyExists(_)), "{err:?}");
    assert_eq!(err.server_message(), Some("duplicate relationship"));
    assert!(fake.relationships().is_empty());

    // The commit itself can fail.
    fake.set_fault_hook(|f| match f.stage {
        FaultStage::ImportCommit => Some(server_error_with_reason(
            Code::Unavailable,
            "lost",
            "ERROR_REASON_UNSPECIFIED",
        )),
        _ => None,
    });
    let err = client.import_relationships(tuples(4)).await.unwrap_err();
    assert_eq!(err.code(), Code::Unavailable);
    assert_eq!(err.reason(), Some("ERROR_REASON_UNSPECIFIED"));
    assert!(fake.relationships().is_empty());

    fake.clear_fault_hook();
    client.import_relationships(tuples(4)).await.unwrap();
    assert_eq!(fake.relationships().len(), 4);
}

#[tokio::test]
async fn an_export_fails_after_k_items() {
    let fake = InMemorySpiceDB::new();
    let client = fake.client().unwrap();
    client.import_relationships(tuples(5)).await.unwrap();
    fake.set_fault_hook(|f| match (f.call.operation(), f.stage, f.item) {
        (Operation::ExportRelationships, FaultStage::Item, 3) => {
            Some(server_error(Code::Unavailable, "stream reset"))
        }
        _ => None,
    });
    let mut got = 0;
    let mut stream_err = None;
    let mut stream =
        Box::pin(client.export_relationships(ExportRelationshipsRequest::new(consistency::full())));
    while let Some(item) = stream.next().await {
        match item {
            Ok(_) => got += 1,
            Err(e) => {
                stream_err = Some(e);
                break;
            }
        }
    }
    assert_eq!(got, 3);
    assert_eq!(stream_err.unwrap().code(), Code::Unavailable);
    assert_eq!(
        fake.calls_of(Operation::ExportRelationships).len(),
        1,
        "a stream that exposed data is never reopened"
    );
}

#[tokio::test]
async fn fail_next_affects_exactly_one_call() {
    let fake = InMemorySpiceDB::new();
    let client = fake.client().unwrap();
    let check = || {
        PermissionCheck::new(
            "doc:d0".parse().unwrap(),
            "view",
            "user:x".parse().unwrap(),
            consistency::full(),
        )
    };
    fake.fail_next(
        Operation::Check,
        server_error(Code::PermissionDenied, "denied"),
    );
    let err = client.check(check()).await.unwrap_err();
    assert!(matches!(err, SpiceDBError::PermissionDenied(_)), "{err:?}");
    let _ = client.check(check()).await.unwrap();
    assert_eq!(fake.calls_of(Operation::Check).len(), 2);

    // An Unavailable fault is retried by the client, as SpiceDB's would be.
    fake.fail_next(Operation::Check, server_error(Code::Unavailable, "down"));
    let _ = client.check(check()).await.unwrap();
    let checks = fake.calls_of(Operation::Check);
    assert_eq!(checks.len(), 4);
    assert_eq!(checks[3].index(), 3);

    // A failed write applies nothing.
    fake.fail_next(Operation::Write, server_error(Code::Unavailable, "down"));
    let mut txn = Transaction::new();
    txn.create(&rel("doc:d0#viewer@user:x"));
    client
        .write(WriteRequest::new(txn.clone()))
        .await
        .unwrap_err();
    assert!(fake.relationships().is_empty());
    client.write(WriteRequest::new(txn)).await.unwrap();
    assert_eq!(fake.relationships().len(), 1);
}

#[tokio::test]
async fn recorded_calls_expose_options() {
    use spicedb::spicedb_proto::authzed::api::v1 as proto;
    let fake = InMemorySpiceDB::new();
    let client = fake.client().unwrap();
    let mut metadata = spicedb::tonic::metadata::MetadataMap::new();
    metadata.insert("x-request-id", "abc".parse().unwrap());
    let options = ReadRelationshipsOptions::new()
        .with_cursor("2")
        .with_limit(5)
        .with_paging(Paging::SingleRequest)
        .with_call_options(CallOptions::new().with_request_metadata(metadata));
    let _: Vec<_> = client
        .read_relationships(
            ReadRelationshipsRequest::new(Filter::new("doc"), consistency::full())
                .with_options(options),
        )
        .collect()
        .await;
    let _: Vec<_> = client
        .read_relationships(ReadRelationshipsRequest::new(
            Filter::new("folder"),
            consistency::full(),
        ))
        .collect()
        .await;

    let reads = fake.calls_of(Operation::ReadRelationships);
    assert_eq!(reads.len(), 2);
    assert_eq!((reads[0].index(), reads[1].index()), (0, 1));
    let first = reads[0]
        .request::<proto::ReadRelationshipsRequest>()
        .unwrap();
    assert_eq!(first.optional_cursor.as_ref().unwrap().token, "2");
    assert_eq!(first.optional_limit, 5);
    assert_eq!(reads[0].metadata().get("x-request-id").unwrap(), "abc");
    let second = reads[1]
        .request::<proto::ReadRelationshipsRequest>()
        .unwrap();
    assert_eq!(
        second.relationship_filter.as_ref().unwrap().resource_type,
        "folder"
    );
    assert!(reads[1]
        .request::<proto::WriteRelationshipsRequest>()
        .is_none());
    assert_eq!(fake.calls().len(), 2);
}

#[tokio::test]
async fn an_import_batch_fault_sees_the_batch() {
    let fake = InMemorySpiceDB::new();
    let client = fake.client().unwrap();
    let seen = std::sync::Arc::new(std::sync::Mutex::new(Vec::new()));
    let record = seen.clone();
    fake.set_fault_hook(move |f| {
        if f.stage == FaultStage::ImportBatch {
            record.lock().unwrap().push(f.batch.clone());
        } else {
            assert!(f.batch.is_empty(), "{:?} carries no batch", f.stage);
        }
        None
    });
    client
        .import_relationships(tuples(5).with_options(
            ImportRelationshipsOptions::new().with_batch_size(NonZeroU32::new(2).unwrap()),
        ))
        .await
        .unwrap();
    let all: Vec<_> = (0..5)
        .map(|i| rel(&format!("doc:d{i}#viewer@user:x")))
        .collect();
    assert_eq!(
        *seen.lock().unwrap(),
        vec![all[0..2].to_vec(), all[2..4].to_vec(), all[4..].to_vec()]
    );
}

#[tokio::test]
async fn cursor_after_matches_the_doubles_cursors() {
    // Matching relationships interleave with others, so the cursor counts
    // matches, not stored positions.
    let fake = InMemorySpiceDB::with_relationships([
        rel("document:a#viewer@user:x"),
        rel("folder:f#viewer@user:x"),
        rel("document:b#viewer@user:x"),
        rel("folder:g#viewer@user:x"),
        rel("document:c#viewer@user:x"),
    ]);
    let client = fake.client().unwrap();
    let request = ReadRelationshipsRequest::new(Filter::new("document"), consistency::full());
    let items: Vec<_> = client
        .read_relationships(request.clone())
        .map(|r| r.unwrap())
        .collect()
        .await;
    assert_eq!(items.len(), 3);
    for (i, item) in items.iter().enumerate() {
        assert_eq!(item.after_result_cursor, Some(cursor_after(i + 1)));
    }
    let resumed: Vec<_> = client
        .read_relationships(
            request.with_options(ReadRelationshipsOptions::new().with_cursor(cursor_after(1))),
        )
        .map(|r| r.unwrap().relationship.resource_id().to_string())
        .collect()
        .await;
    assert_eq!(resumed, vec!["b", "c"]);

    let exported: Vec<_> = client
        .export_relationships(
            ExportRelationshipsRequest::new(consistency::full()).with_options(
                ExportRelationshipsOptions::new()
                    .with_filter(Filter::new("folder"))
                    .with_cursor(cursor_after(1)),
            ),
        )
        .map(|r| r.unwrap())
        .collect()
        .await;
    assert_eq!(exported.len(), 1);
    assert_eq!(exported[0].relationship, rel("folder:g#viewer@user:x"));
    assert_eq!(exported[0].after_result_cursor, Some(cursor_after(2)));
}

#[tokio::test]
async fn schema_reads_can_omit_the_revision() {
    let fake = InMemorySpiceDB::new();
    let client = fake.client().unwrap();
    client
        .write_schema(WriteSchemaRequest::new("definition user {}"))
        .await
        .unwrap();
    let read = client.read_schema(ReadSchemaRequest::new()).await.unwrap();
    assert!(!read.revision.as_ref().is_empty());

    fake.set_read_schema_revision(SchemaRevision::Omitted);
    let read = client.read_schema(ReadSchemaRequest::new()).await.unwrap();
    assert_eq!(read.schema_text, "definition user {}");
    assert!(read.revision.as_ref().is_empty());

    fake.set_read_schema_revision(SchemaRevision::Included);
    let read = client.read_schema(ReadSchemaRequest::new()).await.unwrap();
    assert!(!read.revision.as_ref().is_empty());
}

mod resolution_and_lookups {
    use super::*;
    use futures::TryStreamExt;
    use spicedb::testing::{grant_all, Resolution};
    use spicedb::{
        CheckOptions, LookupPermissionshipFilter, LookupResourcesOptions, LookupResourcesRequest,
        LookupSubjectsOptions, LookupSubjectsRequest, ObjectRef, SubjectRef, WildcardOption,
    };
    use std::sync::{Arc, Mutex};

    fn obj(t: &str, id: &str) -> ObjectRef {
        ObjectRef::new(t, id).unwrap()
    }

    fn check(resource: &str, permission: &str, subject: &str) -> PermissionCheck {
        PermissionCheck::new(
            resource.parse().unwrap(),
            permission,
            subject.parse().unwrap(),
            consistency::full(),
        )
    }

    async fn permissionship(fake: &InMemorySpiceDB, c: PermissionCheck) -> Permissionship {
        fake.client()
            .unwrap()
            .check(c)
            .await
            .unwrap()
            .permissionship
    }

    async fn lookup(
        fake: &InMemorySpiceDB,
        options: LookupResourcesOptions,
    ) -> Vec<spicedb::LookupResource> {
        let request = LookupResourcesRequest::new(
            "document",
            "view",
            obj("user", "alice").into(),
            consistency::full(),
        )
        .with_options(options);
        fake.client()
            .unwrap()
            .lookup_resources(request)
            .try_collect()
            .await
            .unwrap()
    }

    /// A registered result, then the resolver, then the stored
    /// relationships, then no permission.
    #[tokio::test]
    async fn checks_resolve_in_the_documented_order() {
        let fake = InMemorySpiceDB::with_relationships([rel("document:stored#view@user:alice")]);
        fake.set_check_result(
            &obj("document", "registered"),
            "view",
            &obj("user", "alice").into(),
            Permissionship::NoPermission,
        );
        fake.set_check_resolver(|c| {
            (c.resource().object_id() != "stored").then(Resolution::has_permission)
        });
        // Registered wins over the resolver.
        assert_eq!(
            permissionship(&fake, check("document:registered", "view", "user:alice")).await,
            Permissionship::NoPermission
        );
        // The resolver answers what is not registered.
        assert_eq!(
            permissionship(&fake, check("document:generated-9f2", "view", "user:alice")).await,
            Permissionship::HasPermission
        );
        // Declining falls through to the stored relationships.
        assert_eq!(
            permissionship(&fake, check("document:stored", "view", "user:alice")).await,
            Permissionship::HasPermission
        );
        fake.clear_check_resolver();
        assert_eq!(
            permissionship(&fake, check("document:generated-9f2", "view", "user:alice")).await,
            Permissionship::NoPermission
        );
    }

    /// Grant-all is one line and covers checks, bulk checks and lookups.
    #[tokio::test]
    async fn grant_all_grants_every_check() {
        let fake = InMemorySpiceDB::with_relationships([rel("document:d1#owner@user:bob")]);
        fake.set_check_resolver(grant_all);
        let client = fake.client().unwrap();
        assert!(client
            .check(check("document:any", "edit", "user:carol"))
            .await
            .unwrap()
            .has_permission());
        let bulk = client
            .check_bulk(BulkCheckRequest::new(vec![
                check("document:a", "view", "user:x"),
                check("document:b", "view", "user:y"),
            ]))
            .await
            .unwrap();
        assert!(bulk.iter().all(|r| r.has_permission()));
        let ids: Vec<_> = lookup(&fake, LookupResourcesOptions::new())
            .await
            .into_iter()
            .map(|r| r.resource_id)
            .collect();
        assert_eq!(ids, ["d1"]);
    }

    /// The resolver receives the check with the call's context merged in (the
    /// check's own keys win), and a conditional answer reaches the caller with
    /// its missing context.
    #[tokio::test]
    async fn the_resolver_sees_the_context_and_can_answer_conditionally() {
        let fake = InMemorySpiceDB::new();
        let seen = Arc::new(Mutex::new(None));
        let record = seen.clone();
        fake.set_check_resolver(move |c| {
            *record.lock().unwrap() = c.context().cloned();
            Some(Resolution::conditional(["ip"]))
        });
        let mut call = spicedb::types::Context::new();
        call.insert("tier".into(), serde_json::json!("gold"));
        call.insert("day".into(), serde_json::json!("mon"));
        let mut own = spicedb::types::Context::new();
        own.insert("day".into(), serde_json::json!("tue"));
        let result = fake
            .client()
            .unwrap()
            .check(
                check("document:1", "view", "user:alice")
                    .with_context(own)
                    .with_options(CheckOptions::new().with_context(call)),
            )
            .await
            .unwrap();
        assert_eq!(result.permissionship, Permissionship::ConditionalPermission);
        assert_eq!(result.missing_context, ["ip"]);
        assert!(!result.has_permission());
        let seen = seen
            .lock()
            .unwrap()
            .clone()
            .expect("context reached the resolver");
        assert_eq!(seen["tier"], serde_json::json!("gold"));
        assert_eq!(seen["day"], serde_json::json!("tue"));
    }

    /// Stored relationships: exact subjects and wildcards over direct subjects
    /// grant; a caveated match is conditional and an uncaveated one wins; an
    /// expired relationship never grants.
    #[tokio::test]
    async fn stored_relationships_grant_by_exact_or_wildcard_subject() {
        let past = chrono::Utc::now() - chrono::Duration::hours(1);
        let fake = InMemorySpiceDB::with_relationships([
            rel("document:pub#view@user:*"),
            rel("document:c#view@user:alice[only_weekdays]"),
            rel("document:both#view@user:alice[only_weekdays]"),
            rel("document:both#view@user:alice"),
            rel("document:old#view@user:alice").with_expiration(past),
            rel("document:set#view@group:eng#member"),
        ]);
        for (c, expected) in [
            (
                check("document:pub", "view", "user:bob"),
                Permissionship::HasPermission,
            ),
            // A wildcard matches only direct subjects of its type.
            (
                check("document:pub", "view", "user:bob#member"),
                Permissionship::NoPermission,
            ),
            (
                check("document:pub", "view", "team:bob"),
                Permissionship::NoPermission,
            ),
            (
                check("document:c", "view", "user:alice"),
                Permissionship::ConditionalPermission,
            ),
            (
                check("document:both", "view", "user:alice"),
                Permissionship::HasPermission,
            ),
            (
                check("document:old", "view", "user:alice"),
                Permissionship::NoPermission,
            ),
            (
                check("document:set", "view", "group:eng#member"),
                Permissionship::HasPermission,
            ),
            (
                check("document:set", "view", "group:eng"),
                Permissionship::NoPermission,
            ),
            (
                check("document:pub", "edit", "user:bob"),
                Permissionship::NoPermission,
            ),
        ] {
            let label = format!("{c:?}");
            assert_eq!(permissionship(&fake, c).await, expected, "{label}");
        }
    }

    /// LookupResources enumerates objects of the type in first-appearance
    /// order (as resource or subject), returns those check resolution grants,
    /// reports conditional results as conditional (with missing context), and
    /// honors the permissionship filter, limit and cursor.
    #[tokio::test]
    async fn lookup_resources_follows_check_resolution() {
        let fake = InMemorySpiceDB::with_relationships([
            rel("document:d3#view@user:alice"),
            rel("folder:f1#parent@document:d1"),
            rel("document:d2#view@user:alice[only_weekdays]"),
            rel("document:d1#view@user:bob"),
            rel("document:d4#view@user:alice"),
        ]);
        fake.set_check_resolver(|c| {
            (c.resource().object_id() == "d1").then(|| Resolution::conditional(["now"]))
        });
        let all = lookup(&fake, LookupResourcesOptions::new()).await;
        let summary: Vec<_> = all
            .iter()
            .map(|r| (r.resource_id.as_str(), r.permissionship.clone()))
            .collect();
        assert_eq!(
            summary,
            [
                ("d3", Permissionship::HasPermission),
                ("d1", Permissionship::ConditionalPermission),
                ("d2", Permissionship::ConditionalPermission),
                ("d4", Permissionship::HasPermission),
            ]
        );
        assert_eq!(
            all[1]
                .partial_caveat
                .as_ref()
                .unwrap()
                .missing_required_context,
            ["now"]
        );
        assert!(all[2].partial_caveat.is_some());
        assert!(all[0].partial_caveat.is_none());
        let cursors: Vec<_> = all.iter().map(|r| r.after_result_cursor.clone()).collect();
        assert_eq!(
            cursors,
            (1..=4).map(|n| Some(cursor_after(n))).collect::<Vec<_>>()
        );

        // The filter drops conditional results; cursors still count them.
        let granted = lookup(
            &fake,
            LookupResourcesOptions::new()
                .with_permissionship_filter(LookupPermissionshipFilter::HasPermissionOnly),
        )
        .await;
        let ids: Vec<_> = granted.iter().map(|r| r.resource_id.as_str()).collect();
        assert_eq!(ids, ["d3", "d4"]);
        assert_eq!(granted[1].after_result_cursor, Some(cursor_after(4)));

        // Resume after the second granted result; limit and page size hold.
        let resumed = lookup(
            &fake,
            LookupResourcesOptions::new().with_cursor(cursor_after(2)),
        )
        .await;
        let ids: Vec<_> = resumed.iter().map(|r| r.resource_id.as_str()).collect();
        assert_eq!(ids, ["d2", "d4"]);
        let before = fake.calls_of(Operation::LookupResources).len();
        let limited = lookup(
            &fake,
            LookupResourcesOptions::new()
                .with_limit(3)
                .with_page_size(NonZeroU32::new(2).unwrap()),
        )
        .await;
        let ids: Vec<_> = limited.iter().map(|r| r.resource_id.as_str()).collect();
        assert_eq!(ids, ["d3", "d1", "d2"]);
        // Each page is its own recorded RPC.
        assert_eq!(fake.calls_of(Operation::LookupResources).len() - before, 2);
    }

    /// The item fault hook runs before each lookup result.
    #[tokio::test]
    async fn lookup_results_consult_the_item_fault_hook() {
        let fake = InMemorySpiceDB::with_relationships([
            rel("document:d1#view@user:alice"),
            rel("document:d2#view@user:alice"),
        ]);
        fake.set_fault_hook(|f| {
            (f.call.operation() == Operation::LookupResources
                && f.stage == FaultStage::Item
                && f.item == 1)
                .then(|| server_error(Code::Internal, "boom"))
        });
        let request = LookupResourcesRequest::new(
            "document",
            "view",
            obj("user", "alice").into(),
            consistency::full(),
        );
        let results: Vec<_> = fake
            .client()
            .unwrap()
            .lookup_resources(request)
            .collect()
            .await;
        assert_eq!(results.len(), 2);
        assert_eq!(results[0].as_ref().unwrap().resource_id, "d1");
        assert_eq!(results[1].as_ref().unwrap_err().code(), Code::Internal);
    }

    async fn subjects(
        fake: &InMemorySpiceDB,
        options: LookupSubjectsOptions,
        subject_type: &str,
    ) -> Vec<(String, Permissionship)> {
        let request = LookupSubjectsRequest::new(
            obj("document", "1"),
            "view",
            subject_type,
            consistency::full(),
        )
        .with_options(options);
        let found: Vec<spicedb::LookupSubject> = fake
            .client()
            .unwrap()
            .lookup_subjects(request)
            .try_collect()
            .await
            .unwrap();
        found
            .into_iter()
            .map(|s| (s.subject.subject_id, s.subject.permissionship))
            .collect()
    }

    /// LookupSubjects returns granted subjects of the type in first-appearance
    /// order, a stored wildcard as `*` (unless excluded), conditional results
    /// as conditional, and honors the subject relation and limit.
    #[tokio::test]
    async fn lookup_subjects_follows_check_resolution() {
        let fake = InMemorySpiceDB::with_relationships([
            rel("document:1#view@user:carol[only_weekdays]"),
            rel("group:eng#member@user:dave"),
            rel("document:1#view@user:alice"),
            rel("document:1#view@user:*"),
            rel("document:1#view@group:eng#member"),
        ]);
        let p = |id: &str, p: Permissionship| (id.to_string(), p);
        assert_eq!(
            subjects(&fake, LookupSubjectsOptions::new(), "user").await,
            [
                // carol: her caveated grant would be conditional, but the
                // wildcard grants her outright, and an uncaveated match wins.
                p("carol", Permissionship::HasPermission),
                // dave is granted through the wildcard (no schema is
                // evaluated, so group membership does not count).
                p("dave", Permissionship::HasPermission),
                p("alice", Permissionship::HasPermission),
                p("*", Permissionship::HasPermission),
            ]
        );
        let no_wildcards = subjects(
            &fake,
            LookupSubjectsOptions::new().with_wildcard_option(WildcardOption::Exclude),
            "user",
        )
        .await;
        assert_eq!(no_wildcards.len(), 3);
        assert!(no_wildcards.iter().all(|(id, _)| id != "*"));
        assert_eq!(
            subjects(&fake, LookupSubjectsOptions::new().with_limit(2), "user")
                .await
                .len(),
            2
        );
        assert_eq!(
            subjects(
                &fake,
                LookupSubjectsOptions::new().with_subject_relation("member"),
                "group"
            )
            .await,
            [p("eng", Permissionship::HasPermission)]
        );
        // `...` is a direct subject: group:eng itself has no direct grant.
        assert!(subjects(
            &fake,
            LookupSubjectsOptions::new().with_subject_relation("..."),
            "group"
        )
        .await
        .is_empty());

        // Without the wildcard, a caveated-only subject is conditional.
        let caveated =
            InMemorySpiceDB::with_relationships([rel("document:1#view@user:carol[only_weekdays]")]);
        assert_eq!(
            subjects(&caveated, LookupSubjectsOptions::new(), "user").await,
            [p("carol", Permissionship::ConditionalPermission)]
        );
    }

    /// `SubjectRef::from(object)` builds the direct subject used everywhere.
    #[test]
    fn a_direct_subject_is_one_call() {
        let s: SubjectRef = obj("user", "alice").into();
        assert_eq!(s, SubjectRef::direct(obj("user", "alice")));
        assert_eq!(s.relation(), None);
    }
}
