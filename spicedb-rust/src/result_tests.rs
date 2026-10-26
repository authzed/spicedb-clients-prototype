use super::*;
#[test]
fn test_check_result_must_use() {
    let result = CheckResult {
        permissionship: Permissionship::HasPermission,
        missing_context: Vec::new(),
        checked_at: "rev-1".into(),
        debug_trace: None,
        expiration: None,
    };
    assert!(result.has_permission());
}

#[test]
fn test_check_result_conditional_is_not_has_permission() {
    let result = CheckResult {
        permissionship: Permissionship::ConditionalPermission,
        missing_context: vec!["now".to_string()],
        checked_at: "rev-1".into(),
        debug_trace: None,
        expiration: None,
    };
    assert!(!result.has_permission());
    assert_eq!(result.missing_context, vec!["now".to_string()]);
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
    assert_eq!(cr.revision.as_ref(), "rev-1");
}

#[test]
fn test_schema_types_derive_traits() {
    let def = SchemaDefinition {
        name: "document".into(),
        comment: "A document".into(),
        relations: vec![SchemaRelation {
            subject_types: Vec::new(),
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
    let sd = SchemaDiff::Unknown;
    let sd2 = sd.clone();
    assert_eq!(sd, sd2);
}
