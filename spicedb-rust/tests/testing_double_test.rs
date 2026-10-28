//! spicedb::testing::InMemorySpiceDB: a real SpiceDBClient over an in-memory
//! channel, with SpiceDB's write/read/delete/check semantics.

use std::num::NonZeroU32;

use futures::StreamExt;
use spicedb::testing::InMemorySpiceDB;
use spicedb::{
    consistency, BulkCheckRequest, CallOptions, DeleteOptions, DeletePartial, DeleteRequest,
    ExportRelationshipsOptions, ExportRelationshipsRequest, Filter, ImportRelationshipsRequest,
    PermissionCheck, Permissionship, ReadRelationshipsOptions, ReadRelationshipsRequest,
    ReadSchemaRequest, ReflectSchemaRequest, ReflectSchemaResult, Relationship, ResponseMetadata,
    SchemaDefinition, SpiceDBError, Transaction, WriteRequest, WriteSchemaOptions,
    WriteSchemaRequest,
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
