use spicedb::types::*;
use std::collections::HashMap;

#[test]
fn test_relationship_new_valid() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    assert_eq!(r.resource().object_type(), "document");
    assert_eq!(r.resource().object_id(), "doc1");
    assert_eq!(r.relation(), "viewer");
    assert_eq!(r.subject().object().object_type(), "user");
    assert_eq!(r.subject().object().object_id(), "alice");
    assert_eq!(r.subject().relation().unwrap_or_default(), "");
}

#[test]
fn test_relationship_new_invalid_resource_type() {
    let r = (|| -> Result<spicedb::Relationship, spicedb::SpiceDBError> {
        spicedb::Relationship::new(
            spicedb::ObjectRef::new("", "doc1")?,
            "viewer",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice")?, None)?,
        )
    })();
    assert!(matches!(
        r.unwrap_err(),
        spicedb::SpiceDBError::InvalidArgument(_)
    ));
}

#[test]
fn test_relationship_new_invalid_resource_id() {
    let r = (|| -> Result<spicedb::Relationship, spicedb::SpiceDBError> {
        spicedb::Relationship::new(
            spicedb::ObjectRef::new("document", "")?,
            "viewer",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice")?, None)?,
        )
    })();
    assert!(matches!(
        r.unwrap_err(),
        spicedb::SpiceDBError::InvalidArgument(_)
    ));
}

#[test]
fn test_relationship_new_invalid_resource_relation() {
    let r = (|| -> Result<spicedb::Relationship, spicedb::SpiceDBError> {
        spicedb::Relationship::new(
            spicedb::ObjectRef::new("document", "doc1")?,
            "",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice")?, None)?,
        )
    })();
    assert!(matches!(
        r.unwrap_err(),
        spicedb::SpiceDBError::InvalidArgument(_)
    ));
}

#[test]
fn test_relationship_new_invalid_subject_type() {
    let r = (|| -> Result<spicedb::Relationship, spicedb::SpiceDBError> {
        spicedb::Relationship::new(
            spicedb::ObjectRef::new("document", "doc1")?,
            "viewer",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("", "alice")?, None)?,
        )
    })();
    assert!(matches!(
        r.unwrap_err(),
        spicedb::SpiceDBError::InvalidArgument(_)
    ));
}

#[test]
fn test_relationship_new_invalid_subject_id() {
    let r = (|| -> Result<spicedb::Relationship, spicedb::SpiceDBError> {
        spicedb::Relationship::new(
            spicedb::ObjectRef::new("document", "doc1")?,
            "viewer",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "")?, None)?,
        )
    })();
    assert!(matches!(
        r.unwrap_err(),
        spicedb::SpiceDBError::InvalidArgument(_)
    ));
}

#[test]
fn test_relationship_from_objects() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    assert_eq!(r.relation(), "viewer");
    assert_eq!(r.subject().relation().unwrap_or_default(), "");
}

#[test]
fn test_relationship_from_tuple_basic() {
    let r = Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    assert_eq!(r.resource().object_type(), "document");
    assert_eq!(r.resource().object_id(), "doc1");
    assert_eq!(r.relation(), "viewer");
    assert_eq!(r.subject().object().object_type(), "user");
    assert_eq!(r.subject().object().object_id(), "alice");
    assert_eq!(r.subject().relation().unwrap_or_default(), "");
}

#[test]
fn test_relationship_from_tuple_with_subject_relation() {
    let r = Relationship::from_tuple("document:doc1#viewer@group:eng#member").unwrap();
    assert_eq!(r.subject().object().object_type(), "group");
    assert_eq!(r.subject().object().object_id(), "eng");
    assert_eq!(r.subject().relation().unwrap_or_default(), "member");
}

#[test]
fn test_relationship_from_tuple_missing_at() {
    assert!(Relationship::from_tuple("no-at-sign").is_err());
}

#[test]
fn test_relationship_from_tuple_missing_hash() {
    assert!(Relationship::from_tuple("nohash@user:alice").is_err());
}

#[test]
fn test_relationship_from_tuple_missing_colon() {
    assert!(Relationship::from_tuple("nocolon#rel@user:alice").is_err());
}

#[test]
fn test_relationship_display_without_subject_relation() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    assert_eq!(r.to_string(), "document:doc1#viewer@user:alice");
}

#[test]
fn test_relationship_display_with_subject_relation() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@group:eng#member").unwrap();
    assert_eq!(r.to_string(), "document:doc1#viewer@group:eng#member");
}

#[test]
fn test_relationship_roundtrip_via_tuple() {
    let original = spicedb::Relationship::from_tuple("doc:1#viewer@user:alice#member").unwrap();
    let tuple_str = original.to_string();
    let parsed = Relationship::from_tuple(&tuple_str).unwrap();
    assert_eq!(original, parsed);
}

#[test]
fn test_relationship_with_caveat() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice")
        .unwrap()
        .with_caveat("is_active", None);
    assert_eq!(r.caveat_name(), "is_active");
    assert!(r.caveat_context().is_none());
}

#[test]
fn test_relationship_with_caveat_context() {
    let mut ctx = HashMap::new();
    ctx.insert("active".to_string(), serde_json::Value::Bool(true));
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice")
        .unwrap()
        .with_caveat("is_active", Some(ctx));
    assert_eq!(r.caveat_name(), "is_active");
    let ctx = r.caveat_context().unwrap();
    assert_eq!(ctx["active"], serde_json::Value::Bool(true));
}

#[test]
fn test_relationship_with_expiration() {
    let exp = chrono::Utc::now();
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice")
        .unwrap()
        .with_expiration(exp);
    assert_eq!(r.expiration(), Some(exp));
}

#[test]
fn test_relationship_immutable_modifiers() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    let r2 = r.clone().with_caveat("caveat", None);
    assert_eq!(r.caveat_name(), "");
    assert_eq!(r2.caveat_name(), "caveat");
}

#[test]
fn test_relationship_filter() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    let f = r.filter();
    assert_eq!(f.resource_type(), "document");
    assert_eq!(f.resource_id(), Some("doc1"));
    assert_eq!(f.relation(), Some("viewer"));
    assert_eq!(f.subject_type(), Some("user"));
    assert_eq!(f.subject_id(), Some("alice"));
}

#[test]
fn test_filter_builder() {
    let f = Filter::new("document")
        .with_resource_id("doc1")
        .with_relation("viewer")
        .with_subject_type("user")
        .with_subject_id("alice")
        .with_subject_relation(spicedb::SubjectRelationFilter::Named("member".into()));
    assert_eq!(f.resource_type(), "document");
    assert_eq!(f.resource_id(), Some("doc1"));
    assert_eq!(f.relation(), Some("viewer"));
    assert_eq!(f.subject_type(), Some("user"));
    assert_eq!(f.subject_id(), Some("alice"));
    assert_eq!(
        f.subject_relation(),
        &SubjectRelationFilter::Named("member".into())
    );
}

#[test]
fn test_filter_with_prefix() {
    let f = Filter::new("document").with_resource_id_prefix("doc-");
    assert_eq!(f.resource_id_prefix(), Some("doc-"));
}

#[test]
fn test_filter_minimal() {
    let f = Filter::new("document");
    assert_eq!(f.resource_type(), "document");
    assert!(f.resource_id().is_none());
    assert!(f.relation().is_none());
    assert!(f.subject_type().is_none());
}

#[test]
fn test_transaction_create_touch_delete() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    let mut txn = Transaction::new();
    txn.create(&r);
    txn.touch(&r);
    txn.delete(&r);
    assert_eq!(txn.len(), 3);
    assert!(!txn.is_empty());
    assert_eq!(txn.updates()[0].0, UpdateOperation::Create);
    assert_eq!(txn.updates()[1].0, UpdateOperation::Touch);
    assert_eq!(txn.updates()[2].0, UpdateOperation::Delete);
}

#[test]
fn test_transaction_preconditions() {
    let mut txn = Transaction::new();
    let f = Filter::new("document").with_resource_id("doc1");
    txn.must_not_match(f.clone());
    txn.must_match(f);
    assert_eq!(txn.preconditions().len(), 2);
    assert_eq!(
        txn.preconditions()[0].operation,
        PreconditionOperation::MustNotMatch
    );
    assert_eq!(
        txn.preconditions()[1].operation,
        PreconditionOperation::MustMatch
    );
}

#[test]
fn test_transaction_empty() {
    let txn = Transaction::new();
    assert!(txn.is_empty());
    assert_eq!(txn.len(), 0);
}

#[test]
fn test_transaction_is_idempotent_only_when_touch_only_without_preconditions() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    assert!(Transaction::new().is_idempotent(), "empty");
    let mut touch = Transaction::new();
    touch.touch(&r);
    assert!(touch.is_idempotent());
    let mut create = touch.clone();
    create.create(&r);
    assert!(!create.is_idempotent());
    let mut delete = Transaction::new();
    delete.delete(&r);
    assert!(!delete.is_idempotent());
    let mut guarded = touch.clone();
    guarded.must_match(Filter::new("document"));
    assert!(!guarded.is_idempotent());
}

#[test]
fn test_transaction_borrows_relationship() {
    let r = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    let mut txn = Transaction::new();
    txn.create(&r);
    // r is still usable after passing by reference
    assert_eq!(r.resource().object_type(), "document");
}
