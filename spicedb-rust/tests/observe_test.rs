//! Observable native values: tuple syntax checked against SpiceDB's own
//! formatter, filter matching, string forms, short accessors, copy helpers,
//! serde output and protocol conversions.

use chrono::{TimeZone, Utc};
use spicedb::protoconv;
use spicedb::{
    consistency, Filter, ObjectRef, Precondition, PreconditionOperation, Relationship, SubjectRef,
    SubjectRelationFilter, Transaction, Update, UpdateOperation,
};

/// (input, SpiceDB v1.56.2 `tuple.String(tuple.Parse(input))`). Generated with
/// `github.com/authzed/spicedb/pkg/tuple`; our formatter must produce the same
/// canonical text, and our parser must read both forms.
const SPICEDB_GOLDENS: &[(&str, &str)] = &[
    (
        "document:readme#viewer@user:alice",
        "document:readme#viewer@user:alice",
    ),
    (
        "document:readme#viewer@team:eng#member",
        "document:readme#viewer@team:eng#member",
    ),
    (
        "document:readme#viewer@user:*",
        "document:readme#viewer@user:*",
    ),
    (
        "document:readme#viewer@user:alice[only_name]",
        "document:readme#viewer@user:alice[only_name]",
    ),
    (
        r#"document:readme#viewer@user:alice[ip_range:{"cidr":"10.0.0.0/8","n":1.5,"list":["a","]"],"nested":{"b":true}}]"#,
        r#"document:readme#viewer@user:alice[ip_range:{"cidr":"10.0.0.0/8","list":["a","]"],"n":1.5,"nested":{"b":true}}]"#,
    ),
    (
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]",
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]",
    ),
    (
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.123456789Z]",
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.123456789Z]",
    ),
    (
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.5Z]",
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.5Z]",
    ),
    (
        r#"document:readme#viewer@team:eng#member[ip_range:{"cidr":"x"}][expiration:2030-01-02T03:04:05Z]"#,
        r#"document:readme#viewer@team:eng#member[ip_range:{"cidr":"x"}][expiration:2030-01-02T03:04:05Z]"#,
    ),
    (
        "document:readme#viewer@team:eng#...",
        "document:readme#viewer@team:eng",
    ),
];

#[test]
fn tuple_syntax_matches_spicedbs_formatter() {
    for (input, spicedb) in SPICEDB_GOLDENS {
        let parsed = Relationship::from_tuple(input).unwrap_or_else(|e| panic!("{input}: {e}"));
        assert_eq!(&parsed.to_string(), spicedb, "formatting {input}");
        let reparsed: Relationship = spicedb.parse().unwrap();
        assert_eq!(reparsed, parsed, "round trip of {spicedb}");
    }
}

#[test]
fn tuple_parts_are_preserved() {
    let r = Relationship::from_tuple(
        r#"document:readme#viewer@team:eng#member[ip:{"cidr":"10.0.0.0/8"}][expiration:2030-01-02T03:04:05.5Z]"#,
    )
    .unwrap();
    assert_eq!(r.resource_type(), "document");
    assert_eq!(r.resource_id(), "readme");
    assert_eq!(r.subject_type(), "team");
    assert_eq!(r.subject_id(), "eng");
    assert_eq!(r.subject_relation(), Some("member"));
    assert_eq!(r.caveat_name(), "ip");
    assert_eq!(
        r.caveat_context().unwrap()["cidr"],
        serde_json::json!("10.0.0.0/8")
    );
    assert_eq!(
        r.expiration(),
        Some(
            Utc.with_ymd_and_hms(2030, 1, 2, 3, 4, 5).unwrap()
                + chrono::Duration::milliseconds(500)
        )
    );
    for bad in [
        "document:readme#viewer",
        "document:readme#viewer@user:alice[",
        "document:readme#viewer@user:alice[ip:{nope}]",
        "document:readme#viewer@user:alice[ip:[1]]",
        "document:readme#viewer@user:alice[expiration:yesterday]",
        "document:readme#viewer@user:alice[a][b]",
    ] {
        assert!(
            Relationship::from_tuple(bad).is_err(),
            "{bad} must not parse"
        );
    }
}

#[test]
fn copy_helpers_replace_one_part() {
    let r: Relationship = "document:a#viewer@user:alice[c]".parse().unwrap();
    let moved = r
        .clone()
        .with_resource("folder:f".parse().unwrap())
        .with_subject("team:eng#member".parse().unwrap())
        .with_relation("parent")
        .unwrap();
    assert_eq!(moved.to_string(), "folder:f#parent@team:eng#member[c]");
    assert!(r.clone().with_relation("").is_err());
    assert_eq!(r.to_string(), "document:a#viewer@user:alice[c]");
}

#[test]
fn filters_match_with_spicedb_semantics() {
    let direct: Relationship = "document:doc-1#viewer@user:alice".parse().unwrap();
    let userset: Relationship = "document:doc-1#viewer@team:eng#member".parse().unwrap();
    let cases = [
        (Filter::new("document"), true, true),
        (Filter::new("folder"), false, false),
        (
            Filter::new("document").with_resource_id("doc-1"),
            true,
            true,
        ),
        (
            Filter::new("document").with_resource_id("doc-2"),
            false,
            false,
        ),
        (
            Filter::new("document").with_resource_id_prefix("doc-"),
            true,
            true,
        ),
        (
            Filter::new("document").with_resource_id_prefix("x"),
            false,
            false,
        ),
        (Filter::new("document").with_relation("viewer"), true, true),
        (Filter::new("document").with_relation("owner"), false, false),
        (
            Filter::new("document").with_subject_type("user"),
            true,
            false,
        ),
        (
            Filter::new("document")
                .with_subject_type("user")
                .with_subject_id("alice"),
            true,
            false,
        ),
        (
            Filter::new("document")
                .with_subject_type("team")
                .with_subject_relation(SubjectRelationFilter::Any),
            false,
            true,
        ),
        (
            Filter::new("document")
                .with_subject_type("team")
                .with_subject_relation(SubjectRelationFilter::Direct),
            false,
            false,
        ),
        (
            Filter::new("document")
                .with_subject_type("user")
                .with_subject_relation(SubjectRelationFilter::Direct),
            true,
            false,
        ),
        (
            Filter::new("document")
                .with_subject_type("team")
                .with_subject_relation(SubjectRelationFilter::Named("member".into())),
            false,
            true,
        ),
        // Unrepresentable: a subject ID without a subject type matches nothing.
        (
            Filter::new("document").with_subject_id("alice"),
            false,
            false,
        ),
    ];
    for (filter, want_direct, want_userset) in cases {
        assert_eq!(filter.matches(&direct), want_direct, "{filter} vs {direct}");
        assert_eq!(
            filter.matches(&userset),
            want_userset,
            "{filter} vs {userset}"
        );
    }
    assert!(direct.filter().matches(&direct));
    assert!(!direct.filter().matches(&userset));
}

#[test]
fn values_have_readable_string_forms() {
    assert_eq!(
        ObjectRef::new("user", "alice").unwrap().to_string(),
        "user:alice"
    );
    let subject: SubjectRef = "team:eng#member".parse().unwrap();
    assert_eq!(subject.to_string(), "team:eng#member");
    assert_eq!(
        Filter::new("document").with_relation("viewer").to_string(),
        "document:*#viewer@*"
    );
    assert_eq!(
        Filter::new("document")
            .with_resource_id_prefix("doc-")
            .with_subject_type("user")
            .with_subject_id("alice")
            .with_subject_relation(SubjectRelationFilter::Direct)
            .to_string(),
        "document:doc-%*#*@user:alice#..."
    );
    let r: Relationship = "document:a#viewer@user:alice".parse().unwrap();
    let mut txn = Transaction::new();
    txn.must_not_match(Filter::new("document").with_relation("owner"));
    txn.touch(&r);
    txn.delete(&r);
    assert_eq!(txn.len(), 2);
    assert_eq!(
        txn.to_string(),
        "[MUST_NOT_MATCH(document:*#owner@*), TOUCH(document:a#viewer@user:alice), DELETE(document:a#viewer@user:alice)]"
    );
    assert_eq!(
        txn.preconditions()[0].to_string(),
        "MUST_NOT_MATCH(document:*#owner@*)"
    );
    assert!(
        format!("{txn:?}").contains("alice"),
        "Debug is structured, not empty"
    );
}

#[cfg(feature = "serde")]
#[test]
fn serde_output_is_structured() {
    let r: Relationship = r#"document:a#viewer@user:alice[c:{"k":1}]"#.parse().unwrap();
    let json = serde_json::to_value(&r).unwrap();
    assert_eq!(json["resource"]["object_id"], "a");
    assert_eq!(json["caveat_context"]["k"], 1);
    let filter = serde_json::to_value(Filter::new("document").with_relation("viewer")).unwrap();
    assert_eq!(filter["relation"], "viewer");
    let mut txn = Transaction::new();
    txn.touch(&r);
    assert_ne!(serde_json::to_string(&txn).unwrap(), "{}");
}

#[test]
fn protoconv_round_trips_native_values() {
    let r: Relationship =
        r#"document:a#viewer@team:eng#member[c:{"k":[1.5,"x"]}][expiration:2030-01-02T03:04:05Z]"#
            .parse()
            .unwrap();
    let wire = protoconv::relationship_to_proto(&r);
    assert_eq!(protoconv::relationship_from_proto(&wire).unwrap(), r);
    assert!(protoconv::relationship_from_proto(&Default::default()).is_err());

    let filter = Filter::new("document")
        .with_resource_id("a")
        .with_subject_type("user")
        .with_subject_relation(SubjectRelationFilter::Direct);
    let wire = protoconv::filter_to_proto(&filter).unwrap();
    assert_eq!(protoconv::filter_from_proto(&wire), filter);

    let precondition = Precondition::new(PreconditionOperation::MustMatch, filter.clone());
    let wire = protoconv::precondition_to_proto(&precondition).unwrap();
    assert_eq!(
        protoconv::precondition_from_proto(&wire).unwrap(),
        precondition
    );

    let update = Update::new(UpdateOperation::Touch, r.clone());
    assert_eq!(
        protoconv::update_from_proto(&protoconv::update_to_proto(&update)).unwrap(),
        update
    );

    for strategy in [
        consistency::full(),
        consistency::min_latency(),
        consistency::at_least("t1".into()),
        consistency::snapshot("t2".into()),
    ] {
        let wire = protoconv::consistency_to_proto(&strategy);
        assert_eq!(protoconv::consistency_from_proto(&wire).unwrap(), strategy);
    }

    let mut txn = Transaction::new();
    txn.create(&r);
    txn.must_not_match(filter);
    let request = protoconv::transaction_to_proto(&txn).unwrap();
    assert_eq!(request.updates.len(), 1);
    assert_eq!(request.optional_preconditions.len(), 1);
}
