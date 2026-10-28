//! spicedb::testing::InMemorySpiceDB: a real SpiceDBClient over an in-memory
//! channel, with SpiceDB's write/read/delete/check semantics.

use std::num::NonZeroU32;

use futures::StreamExt;
use spicedb::testing::InMemorySpiceDB;
use spicedb::{
    consistency, BulkCheckRequest, DeleteOptions, DeletePartial, DeleteRequest, Filter,
    PermissionCheck, Permissionship, ReadRelationshipsOptions, ReadRelationshipsRequest,
    Relationship, SpiceDBError, Transaction, WriteRequest,
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
