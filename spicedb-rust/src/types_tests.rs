use super::*;
#[test]
fn test_check_permissionship_from_proto() {
    assert_eq!(
        check_permissionship_from_proto(
            proto::check_permission_response::Permissionship::Unspecified as i32
        ),
        Permissionship::Unspecified
    );
    assert_eq!(
        check_permissionship_from_proto(
            proto::check_permission_response::Permissionship::NoPermission as i32
        ),
        Permissionship::NoPermission
    );
    assert_eq!(
        check_permissionship_from_proto(
            proto::check_permission_response::Permissionship::HasPermission as i32
        ),
        Permissionship::HasPermission
    );
    assert_eq!(
        check_permissionship_from_proto(
            proto::check_permission_response::Permissionship::ConditionalPermission as i32
        ),
        Permissionship::ConditionalPermission
    );
    // Unrecognized values fail safe to Unspecified rather than panicking.
    assert_eq!(
        check_permissionship_from_proto(99),
        Permissionship::Unspecified
    );
}

#[test]
fn test_check_result_from_bulk_item_missing_context_by_value() {
    let item = proto::CheckBulkPermissionsResponseItem {
        permissionship: proto::check_permission_response::Permissionship::ConditionalPermission
            as i32,
        partial_caveat_info: Some(proto::PartialCaveatInfo {
            missing_required_context: vec!["ip_address".to_string(), "now".to_string()],
        }),
        debug_trace: None,
    };
    let result = check_result_from_bulk_item(&item, "rev-1");
    assert_eq!(
        result.missing_context,
        vec!["ip_address".to_string(), "now".to_string()]
    );
    assert_eq!(result.permissionship, Permissionship::ConditionalPermission);
}

#[test]
fn test_check_result_from_bulk_item_no_partial_caveat_info_yields_empty_missing_context() {
    let item = proto::CheckBulkPermissionsResponseItem {
        permissionship: proto::check_permission_response::Permissionship::HasPermission as i32,
        partial_caveat_info: None,
        debug_trace: None,
    };
    let result = check_result_from_bulk_item(&item, "rev-1");
    assert!(result.missing_context.is_empty());
}

#[test]
fn test_check_result_from_bulk_item_checked_at_populated() {
    let item = proto::CheckBulkPermissionsResponseItem {
        permissionship: proto::check_permission_response::Permissionship::HasPermission as i32,
        partial_caveat_info: None,
        debug_trace: None,
    };
    let result = check_result_from_bulk_item(&item, "zedtoken-abc123");
    assert_eq!(result.checked_at.as_ref(), "zedtoken-abc123");
}

#[test]
fn test_permissionship_from_proto() {
    assert_eq!(
        permissionship_from_proto(proto::LookupPermissionship::Unspecified as i32),
        Permissionship::Unspecified
    );
    assert_eq!(
        permissionship_from_proto(proto::LookupPermissionship::HasPermission as i32),
        Permissionship::HasPermission
    );
    assert_eq!(
        permissionship_from_proto(proto::LookupPermissionship::ConditionalPermission as i32),
        Permissionship::ConditionalPermission
    );
    // Unrecognized values fail safe to Unspecified rather than panicking.
    assert_eq!(permissionship_from_proto(99), Permissionship::Unspecified);
}

#[test]
fn test_partial_caveat_from_proto_none() {
    assert_eq!(partial_caveat_from_proto(None), None);
}

#[test]
fn test_partial_caveat_from_proto_some() {
    let proto_caveat = proto::PartialCaveatInfo {
        missing_required_context: vec!["ip_address".to_string()],
    };
    assert_eq!(
        partial_caveat_from_proto(Some(&proto_caveat)),
        Some(PartialCaveatInfo {
            missing_required_context: vec!["ip_address".to_string()],
        })
    );
}

#[test]
fn test_resolved_subject_from_proto_none_yields_zero_value() {
    let subject = resolved_subject_from_proto(None);
    assert_eq!(subject.subject_id, "");
    assert_eq!(subject.permissionship, Permissionship::Unspecified);
    assert_eq!(subject.partial_caveat, None);
}

#[test]
fn test_resolved_subject_from_proto_some() {
    let proto_subject = proto::ResolvedSubject {
        subject_object_id: "alice".to_string(),
        permissionship: proto::LookupPermissionship::HasPermission as i32,
        partial_caveat_info: None,
    };
    let subject = resolved_subject_from_proto(Some(&proto_subject));
    assert_eq!(subject.subject_id, "alice");
    assert_eq!(subject.permissionship, Permissionship::HasPermission);
    assert_eq!(subject.partial_caveat, None);
}

#[test]
fn test_tree_operation_from_proto() {
    assert_eq!(
        tree_operation_from_proto(proto::algebraic_subject_set::Operation::Unspecified as i32),
        TreeOperation::Unspecified
    );
    assert_eq!(
        tree_operation_from_proto(proto::algebraic_subject_set::Operation::Union as i32),
        TreeOperation::Union
    );
    assert_eq!(
        tree_operation_from_proto(proto::algebraic_subject_set::Operation::Intersection as i32),
        TreeOperation::Intersection
    );
    assert_eq!(
        tree_operation_from_proto(proto::algebraic_subject_set::Operation::Exclusion as i32),
        TreeOperation::Exclusion
    );
    // Unrecognized values fail safe to Unspecified rather than panicking.
    assert_eq!(tree_operation_from_proto(99), TreeOperation::Unspecified);
}

#[test]
fn test_merge_check_context_neither_supplied_is_none() {
    assert_eq!(merge_check_context(None, None), None);
}

#[test]
fn test_merge_check_context_call_level_only() {
    let mut call_level = HashMap::new();
    call_level.insert("now".to_string(), serde_json::json!(42.0));
    assert_eq!(
        merge_check_context(Some(&call_level), None),
        Some(call_level)
    );
}

#[test]
fn test_merge_check_context_item_only() {
    let mut item = HashMap::new();
    item.insert("now".to_string(), serde_json::json!(42.0));
    assert_eq!(merge_check_context(None, Some(&item)), Some(item));
}

#[test]
fn test_merge_check_context_merge_rule() {
    let mut call_level = HashMap::new();
    call_level.insert("now".to_string(), serde_json::json!(42.0));
    call_level.insert("region".to_string(), serde_json::json!("us"));

    let mut item = HashMap::new();
    item.insert("region".to_string(), serde_json::json!("eu"));

    let merged = merge_check_context(Some(&call_level), Some(&item)).unwrap();

    let mut want = HashMap::new();
    want.insert("now".to_string(), serde_json::json!(42.0));
    want.insert("region".to_string(), serde_json::json!("eu"));
    assert_eq!(merged, want);
}

#[test]
fn test_check_context_to_proto_round_trips() {
    let mut ctx = HashMap::new();
    // Protobuf numbers are doubles; integral values come back as JSON
    // integers and fractional ones as floats.
    ctx.insert("now".to_string(), serde_json::json!(42));
    ctx.insert("ratio".to_string(), serde_json::json!(0.5));
    ctx.insert("region".to_string(), serde_json::json!("us"));

    let proto_struct = check_context_to_proto(&ctx);
    let round_tripped: HashMap<String, serde_json::Value> = proto_struct
        .fields
        .iter()
        .map(|(k, v)| (k.clone(), prost_value_to_json(v)))
        .collect();
    assert_eq!(round_tripped, ctx);
}

#[test]
fn exact_filters_preserve_direct_named_and_any_without_widening() {
    let direct = Relationship::from_tuple("document:one#viewer@user:alice")
        .unwrap()
        .filter()
        .to_proto()
        .unwrap();
    assert_eq!(
        direct
            .optional_subject_filter
            .unwrap()
            .optional_relation
            .unwrap()
            .relation,
        ""
    );
    let named = Relationship::from_tuple("document:one#viewer@group:eng#member")
        .unwrap()
        .filter()
        .to_proto()
        .unwrap();
    assert_eq!(
        named
            .optional_subject_filter
            .unwrap()
            .optional_relation
            .unwrap()
            .relation,
        "member"
    );
    let any = Filter::new("document")
        .with_subject_type("user")
        .to_proto()
        .unwrap();
    assert!(any
        .optional_subject_filter
        .unwrap()
        .optional_relation
        .is_none());
    assert!(Filter::new("document")
        .with_subject_id("alice")
        .to_proto()
        .is_err());
    assert!(Filter::new("document")
        .with_subject_relation(SubjectRelationFilter::Direct)
        .to_proto()
        .is_err());
    assert!(Filter::new("document")
        .with_resource_id("id")
        .with_resource_id_prefix("prefix")
        .to_proto()
        .is_err());
    for name in ["", "..."] {
        assert!(Filter::new("document")
            .with_subject_type("user")
            .with_subject_relation(SubjectRelationFilter::Named(name.into()))
            .to_proto()
            .is_err());
    }
}
#[test]
fn recursive_tree_variants_are_exclusive_and_unknown_is_safe() {
    let leaf = proto::PermissionRelationshipTree {
        expanded_object: Some(proto::ObjectReference {
            object_type: "document".into(),
            object_id: "one".into(),
        }),
        expanded_relation: "viewer".into(),
        tree_type: Some(proto::permission_relationship_tree::TreeType::Leaf(
            proto::DirectSubjectSet {
                subjects: vec![proto::SubjectReference {
                    object: Some(proto::ObjectReference {
                        object_type: "user".into(),
                        object_id: "alice".into(),
                    }),
                    optional_relation: "...".into(),
                }],
            },
        )),
    };
    let root = proto::PermissionRelationshipTree {
        tree_type: Some(proto::permission_relationship_tree::TreeType::Intermediate(
            proto::AlgebraicSubjectSet {
                operation: 999,
                children: vec![leaf],
            },
        )),
        ..Default::default()
    };
    let PermissionTree::Intermediate {
        operation,
        children,
        ..
    } = permission_tree_from_proto(Some(&root))
    else {
        panic!()
    };
    assert_eq!(operation, TreeOperation::Unspecified);
    let PermissionTree::Leaf { subjects, .. } = &children[0] else {
        panic!()
    };
    assert_eq!(subjects[0].relation(), None);
    assert_eq!(permission_tree_from_proto(None), PermissionTree::Unknown);
}
#[test]
fn check_context_cannot_enter_persisted_relationships() {
    let context = [(
        "nested".into(),
        serde_json::json!({"a":[null,true,"x",3.25]}),
    )]
    .into_iter()
    .collect();
    let relationship = Relationship::from_tuple("document:one#viewer@user:alice")
        .unwrap()
        .with_caveat("stored", Some(context));
    let question = PermissionCheck::new(
        relationship.resource().clone(),
        "view",
        relationship.subject().clone(),
        crate::consistency::full(),
    )
    .with_context(
        [("check_only".into(), serde_json::json!(true))]
            .into_iter()
            .collect(),
    );
    let roundtrip = Relationship::from_proto(&relationship.to_proto());
    assert_eq!(roundtrip, relationship);
    assert!(!roundtrip
        .caveat_context()
        .unwrap()
        .contains_key("check_only"));
    assert!(question.context().unwrap().contains_key("check_only"));
}

#[test]
fn explicitly_empty_filter_constraints_never_widen() {
    for filter in [
        Filter::new("document").with_resource_id(""),
        Filter::new("document").with_resource_id_prefix(""),
        Filter::new("document").with_relation(""),
        Filter::new("document")
            .with_subject_type("user")
            .with_subject_id(""),
    ] {
        assert!(filter.to_proto().is_err());
    }
    assert!(Relationship::from_tuple("document:one#viewer@user:alice")
        .unwrap()
        .with_caveat("", None)
        .validate()
        .is_err());
}
