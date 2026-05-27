use spicedb::types::*;
use std::collections::HashMap;

#[test]
fn test_relationship_new_valid() {
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "").unwrap();
    assert_eq!(r.resource_type, "document");
    assert_eq!(r.resource_id, "doc1");
    assert_eq!(r.resource_relation, "viewer");
    assert_eq!(r.subject_type, "user");
    assert_eq!(r.subject_id, "alice");
    assert_eq!(r.subject_relation, "");
}

#[test]
fn test_relationship_new_invalid_resource_type() {
    let r = Relationship::new("", "doc1", "viewer", "user", "alice", "");
    assert_eq!(r.unwrap_err(), RelationshipError::InvalidResource);
}

#[test]
fn test_relationship_new_invalid_resource_id() {
    let r = Relationship::new("document", "", "viewer", "user", "alice", "");
    assert_eq!(r.unwrap_err(), RelationshipError::InvalidResource);
}

#[test]
fn test_relationship_new_invalid_resource_relation() {
    let r = Relationship::new("document", "doc1", "", "user", "alice", "");
    assert_eq!(r.unwrap_err(), RelationshipError::InvalidResource);
}

#[test]
fn test_relationship_new_invalid_subject_type() {
    let r = Relationship::new("document", "doc1", "viewer", "", "alice", "");
    assert_eq!(r.unwrap_err(), RelationshipError::InvalidSubject);
}

#[test]
fn test_relationship_new_invalid_subject_id() {
    let r = Relationship::new("document", "doc1", "viewer", "user", "", "");
    assert_eq!(r.unwrap_err(), RelationshipError::InvalidSubject);
}

#[test]
fn test_relationship_from_objects() {
    let r = Relationship::from_objects("document", "doc1", "viewer", "user", "alice").unwrap();
    assert_eq!(r.resource_relation, "viewer");
    assert_eq!(r.subject_relation, "");
}

#[test]
fn test_relationship_from_tuple_basic() {
    let r = Relationship::from_tuple("document:doc1#viewer@user:alice").unwrap();
    assert_eq!(r.resource_type, "document");
    assert_eq!(r.resource_id, "doc1");
    assert_eq!(r.resource_relation, "viewer");
    assert_eq!(r.subject_type, "user");
    assert_eq!(r.subject_id, "alice");
    assert_eq!(r.subject_relation, "");
}

#[test]
fn test_relationship_from_tuple_with_subject_relation() {
    let r = Relationship::from_tuple("document:doc1#viewer@group:eng#member").unwrap();
    assert_eq!(r.subject_type, "group");
    assert_eq!(r.subject_id, "eng");
    assert_eq!(r.subject_relation, "member");
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
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "").unwrap();
    assert_eq!(r.to_string(), "document:doc1#viewer@user:alice");
}

#[test]
fn test_relationship_display_with_subject_relation() {
    let r = Relationship::new("document", "doc1", "viewer", "group", "eng", "member").unwrap();
    assert_eq!(r.to_string(), "document:doc1#viewer@group:eng#member");
}

#[test]
fn test_relationship_roundtrip_via_tuple() {
    let original = Relationship::new("doc", "1", "viewer", "user", "alice", "member").unwrap();
    let tuple_str = original.to_string();
    let parsed = Relationship::from_tuple(&tuple_str).unwrap();
    assert_eq!(original, parsed);
}

#[test]
fn test_relationship_with_caveat() {
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
        .unwrap()
        .with_caveat("is_active", None);
    assert_eq!(r.caveat_name, "is_active");
    assert!(r.caveat_context.is_none());
}

#[test]
fn test_relationship_with_caveat_context() {
    let mut ctx = HashMap::new();
    ctx.insert("active".to_string(), serde_json::Value::Bool(true));
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
        .unwrap()
        .with_caveat("is_active", Some(ctx));
    assert_eq!(r.caveat_name, "is_active");
    let ctx = r.caveat_context.unwrap();
    assert_eq!(ctx["active"], serde_json::Value::Bool(true));
}

#[test]
fn test_relationship_with_expiration() {
    let exp = chrono::Utc::now();
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
        .unwrap()
        .with_expiration(exp);
    assert_eq!(r.expiration, Some(exp));
}

#[test]
fn test_relationship_immutable_modifiers() {
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "").unwrap();
    let r2 = r.clone().with_caveat("caveat", None);
    assert_eq!(r.caveat_name, "");
    assert_eq!(r2.caveat_name, "caveat");
}

#[test]
fn test_relationship_filter() {
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "").unwrap();
    let f = r.filter();
    assert_eq!(f.resource_type, "document");
    assert_eq!(f.resource_id, Some("doc1".to_string()));
    assert_eq!(f.relation, Some("viewer".to_string()));
    assert_eq!(f.subject_type, Some("user".to_string()));
    assert_eq!(f.subject_id, Some("alice".to_string()));
}

#[test]
fn test_filter_builder() {
    let f = Filter::new("document")
        .with_resource_id("doc1")
        .with_relation("viewer")
        .with_subject_type("user")
        .with_subject_id("alice")
        .with_subject_relation("member");
    assert_eq!(f.resource_type, "document");
    assert_eq!(f.resource_id, Some("doc1".to_string()));
    assert_eq!(f.relation, Some("viewer".to_string()));
    assert_eq!(f.subject_type, Some("user".to_string()));
    assert_eq!(f.subject_id, Some("alice".to_string()));
    assert_eq!(f.subject_relation, Some("member".to_string()));
}

#[test]
fn test_filter_with_prefix() {
    let f = Filter::new("document").with_resource_id_prefix("doc-");
    assert_eq!(f.resource_id_prefix, Some("doc-".to_string()));
}

#[test]
fn test_filter_minimal() {
    let f = Filter::new("document");
    assert_eq!(f.resource_type, "document");
    assert!(f.resource_id.is_none());
    assert!(f.relation.is_none());
    assert!(f.subject_type.is_none());
}

#[test]
fn test_transaction_create_touch_delete() {
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "").unwrap();
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
fn test_transaction_borrows_relationship() {
    let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "").unwrap();
    let mut txn = Transaction::new();
    txn.create(&r);
    // r is still usable after passing by reference
    assert_eq!(r.resource_type, "document");
}

#[test]
fn test_check_result_must_use() {
    let result = CheckResult {
        has_permission: true,
    };
    assert!(result.has_permission);
}

#[test]
fn test_update_operation_equality() {
    assert_ne!(UpdateOperation::Create, UpdateOperation::Touch);
    assert_ne!(UpdateOperation::Touch, UpdateOperation::Delete);
    assert_eq!(UpdateOperation::Create, UpdateOperation::Create);
}

#[test]
fn test_count_result() {
    let cr = CountResult {
        relationship_count: 42,
        revision: "rev-1".into(),
    };
    assert_eq!(cr.relationship_count, 42);
    assert_eq!(cr.revision, "rev-1");
}

#[test]
fn test_schema_types_derive_traits() {
    let def = SchemaDefinition {
        name: "document".into(),
        comment: "A document".into(),
        relations: vec![SchemaRelation {
            name: "viewer".into(),
            comment: "".into(),
            parent_definition_name: "document".into(),
        }],
        permissions: vec![SchemaPermission {
            name: "view".into(),
            comment: "".into(),
            parent_definition_name: "document".into(),
        }],
    };
    let def2 = def.clone();
    assert_eq!(def, def2);
    let _ = format!("{:?}", def);
}

#[test]
fn test_relation_reference() {
    let rr = RelationReference {
        definition_name: "document".into(),
        relation_name: "viewer".into(),
        is_permission: false,
    };
    let rr2 = rr.clone();
    assert_eq!(rr, rr2);
}

#[test]
fn test_schema_diff() {
    let sd = SchemaDiff {
        kind: "definition_added".into(),
        definition_name: "document".into(),
        relation_name: String::new(),
        permission_name: String::new(),
        caveat_name: String::new(),
    };
    let sd2 = sd.clone();
    assert_eq!(sd, sd2);
}
