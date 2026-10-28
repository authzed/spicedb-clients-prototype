//! Native <-> protocol conversions, for tools that print or persist
//! protocol-shaped data (JSON output, backup files, debug traces).
//!
//! This is a secondary namespace: ordinary operations never need it. The
//! conversions are the ones the client itself uses on the wire, so output
//! stays identical to what SpiceDB sends and receives. Conversions from the
//! protocol validate what a native value requires and fail with
//! `InvalidArgument` instead of inventing data.

use chrono::{DateTime, Utc};
use spicedb_proto::authzed::api::v1 as proto;

use crate::consistency::Strategy;
use crate::error::SpiceDBError;
use crate::types::*;

fn timestamp(t: &DateTime<Utc>) -> prost_types::Timestamp {
    prost_types::Timestamp {
        seconds: t.timestamp(),
        nanos: t.timestamp_subsec_nanos() as i32,
    }
}

fn token(t: &ZedToken) -> Option<proto::ZedToken> {
    (!t.as_ref().is_empty()).then(|| t.to_proto())
}

/// `ZedToken` to its protocol message.
pub fn zed_token_to_proto(t: &ZedToken) -> proto::ZedToken {
    t.to_proto()
}
/// A protocol token to `ZedToken`, preserving the string exactly.
pub fn zed_token_from_proto(t: &proto::ZedToken) -> ZedToken {
    t.token.clone().into()
}

pub fn object_to_proto(o: &ObjectRef) -> proto::ObjectReference {
    o.to_proto()
}
pub fn object_from_proto(o: &proto::ObjectReference) -> Result<ObjectRef, SpiceDBError> {
    ObjectRef::new(o.object_type.clone(), o.object_id.clone())
}
pub fn subject_to_proto(s: &SubjectRef) -> proto::SubjectReference {
    s.to_proto()
}
/// `...` and an empty relation both mean a direct subject.
pub fn subject_from_proto(s: &proto::SubjectReference) -> Result<SubjectRef, SpiceDBError> {
    let object = s
        .object
        .as_ref()
        .ok_or_else(|| invalid("subject.object", "required"))?;
    let relation = match s.optional_relation.as_str() {
        "" | "..." => None,
        r => Some(r.to_string()),
    };
    SubjectRef::new(object_from_proto(object)?, relation)
}

pub fn relationship_to_proto(r: &Relationship) -> proto::Relationship {
    r.to_proto()
}
pub fn relationship_from_proto(r: &proto::Relationship) -> Result<Relationship, SpiceDBError> {
    let resource = r
        .resource
        .as_ref()
        .ok_or_else(|| invalid("resource", "required"))?;
    let subject = r
        .subject
        .as_ref()
        .ok_or_else(|| invalid("subject", "required"))?;
    let mut native = Relationship::new(
        object_from_proto(resource)?,
        r.relation.clone(),
        subject_from_proto(subject)?,
    )?;
    if let Some(c) = &r.optional_caveat {
        native = native.with_caveat(
            c.caveat_name.clone(),
            c.context.as_ref().map(context_from_proto),
        );
    }
    if let Some(t) = r.optional_expires_at.as_ref() {
        native = native.with_expiration(
            timestamp_from_proto(t).ok_or_else(|| invalid("expiration", "out of range"))?,
        );
    }
    native.validate()?;
    Ok(native)
}

/// Fails for a filter the protocol cannot represent (see `Filter`).
pub fn filter_to_proto(f: &Filter) -> Result<proto::RelationshipFilter, SpiceDBError> {
    f.to_proto()
}
pub fn filter_from_proto(f: &proto::RelationshipFilter) -> Filter {
    let opt = |s: &str| (!s.is_empty()).then(|| s.to_string());
    let mut filter = Filter::new(f.resource_type.clone());
    filter.resource_id = opt(&f.optional_resource_id);
    filter.resource_id_prefix = opt(&f.optional_resource_id_prefix);
    filter.relation = opt(&f.optional_relation);
    if let Some(s) = &f.optional_subject_filter {
        filter.subject_type = Some(s.subject_type.clone());
        filter.subject_id = opt(&s.optional_subject_id);
        filter.subject_relation = match &s.optional_relation {
            None => SubjectRelationFilter::Any,
            Some(r) if r.relation.is_empty() || r.relation == "..." => {
                SubjectRelationFilter::Direct
            }
            Some(r) => SubjectRelationFilter::Named(r.relation.clone()),
        };
    }
    filter
}

pub fn precondition_to_proto(p: &Precondition) -> Result<proto::Precondition, SpiceDBError> {
    Ok(proto::Precondition {
        operation: match p.operation {
            PreconditionOperation::MustMatch => proto::precondition::Operation::MustMatch as i32,
            PreconditionOperation::MustNotMatch => {
                proto::precondition::Operation::MustNotMatch as i32
            }
        },
        filter: Some(p.filter.to_proto()?),
    })
}
pub fn precondition_from_proto(p: &proto::Precondition) -> Result<Precondition, SpiceDBError> {
    let operation = match p.operation {
        x if x == proto::precondition::Operation::MustMatch as i32 => {
            PreconditionOperation::MustMatch
        }
        x if x == proto::precondition::Operation::MustNotMatch as i32 => {
            PreconditionOperation::MustNotMatch
        }
        _ => return Err(invalid("precondition.operation", "unknown operation")),
    };
    let filter = p
        .filter
        .as_ref()
        .ok_or_else(|| invalid("precondition.filter", "required"))?;
    Ok(Precondition {
        operation,
        filter: filter_from_proto(filter),
    })
}

fn operation_to_proto(op: UpdateOperation) -> i32 {
    match op {
        UpdateOperation::Create => proto::relationship_update::Operation::Create as i32,
        UpdateOperation::Touch => proto::relationship_update::Operation::Touch as i32,
        UpdateOperation::Delete => proto::relationship_update::Operation::Delete as i32,
        UpdateOperation::Unspecified => proto::relationship_update::Operation::Unspecified as i32,
    }
}

pub fn update_to_proto(u: &Update) -> proto::RelationshipUpdate {
    proto::RelationshipUpdate {
        operation: operation_to_proto(u.operation),
        relationship: Some(u.relationship.to_proto()),
    }
}
/// An unknown operation becomes `UpdateOperation::Unspecified`, never a write.
pub fn update_from_proto(u: &proto::RelationshipUpdate) -> Result<Update, SpiceDBError> {
    let relationship = u
        .relationship
        .as_ref()
        .ok_or_else(|| invalid("update.relationship", "required"))?;
    Ok(Update {
        operation: match u.operation {
            1 => UpdateOperation::Create,
            2 => UpdateOperation::Touch,
            3 => UpdateOperation::Delete,
            _ => UpdateOperation::Unspecified,
        },
        relationship: relationship_from_proto(relationship)?,
    })
}

/// The `WriteRelationshipsRequest` a `write` of this transaction sends.
pub fn transaction_to_proto(
    t: &Transaction,
) -> Result<proto::WriteRelationshipsRequest, SpiceDBError> {
    Ok(proto::WriteRelationshipsRequest {
        updates: t
            .updates()
            .iter()
            .map(|(op, r)| proto::RelationshipUpdate {
                operation: operation_to_proto(*op),
                relationship: Some(r.to_proto()),
            })
            .collect(),
        optional_preconditions: t
            .preconditions()
            .iter()
            .map(precondition_to_proto)
            .collect::<Result<_, _>>()?,
        optional_transaction_metadata: None,
    })
}

pub fn consistency_to_proto(s: &Strategy) -> proto::Consistency {
    s.to_proto()
}
pub fn consistency_from_proto(c: &proto::Consistency) -> Result<Strategy, SpiceDBError> {
    use proto::consistency::Requirement;
    let strategy = match &c.requirement {
        Some(Requirement::FullyConsistent(true)) => Strategy::Full,
        Some(Requirement::MinimizeLatency(true)) => Strategy::MinLatency,
        Some(Requirement::AtLeastAsFresh(t)) => Strategy::AtLeast(t.token.clone().into()),
        Some(Requirement::AtExactSnapshot(t)) => Strategy::Snapshot(t.token.clone().into()),
        _ => return Err(invalid("consistency", "no requirement selected")),
    };
    strategy.validate()?;
    Ok(strategy)
}

fn check_permissionship_to_proto(p: &Permissionship) -> i32 {
    use proto::check_permission_response::Permissionship as P;
    match p {
        Permissionship::NoPermission => P::NoPermission as i32,
        Permissionship::HasPermission => P::HasPermission as i32,
        Permissionship::ConditionalPermission => P::ConditionalPermission as i32,
        Permissionship::Unspecified => P::Unspecified as i32,
    }
}

fn lookup_permissionship_to_proto(p: &Permissionship) -> i32 {
    match p {
        Permissionship::HasPermission => proto::LookupPermissionship::HasPermission as i32,
        Permissionship::ConditionalPermission => {
            proto::LookupPermissionship::ConditionalPermission as i32
        }
        _ => proto::LookupPermissionship::Unspecified as i32,
    }
}

fn partial_caveat_to_proto(missing: &[String]) -> Option<proto::PartialCaveatInfo> {
    (!missing.is_empty()).then(|| proto::PartialCaveatInfo {
        missing_required_context: missing.to_vec(),
    })
}

/// A check result as the `CheckPermissionResponse` SpiceDB would send,
/// including its debug trace.
pub fn check_result_to_proto(r: &CheckResult) -> proto::CheckPermissionResponse {
    proto::CheckPermissionResponse {
        checked_at: token(&r.checked_at),
        permissionship: check_permissionship_to_proto(&r.permissionship),
        partial_caveat_info: partial_caveat_to_proto(&r.missing_context),
        debug_trace: r.debug_trace.as_ref().map(debug_information_to_proto),
        optional_expires_at: r.expiration.as_ref().map(timestamp),
    }
}

pub fn debug_information_to_proto(d: &DebugInformation) -> proto::DebugInformation {
    proto::DebugInformation {
        check: d.check.as_ref().map(check_trace_to_proto),
        schema_used: d.schema_used.clone(),
    }
}

fn check_trace_to_proto(t: &CheckDebugTrace) -> proto::CheckDebugTrace {
    use proto::check_debug_trace::Resolution;
    proto::CheckDebugTrace {
        resource: t.resource.as_ref().map(ObjectRef::to_proto),
        permission: t.permission.clone(),
        permission_type: match t.permission_type {
            PermissionType::Relation => 1,
            PermissionType::Permission => 2,
            _ => 0,
        },
        subject: t.subject.as_ref().map(SubjectRef::to_proto),
        result: match t.result {
            Permissionship::NoPermission => 1,
            Permissionship::HasPermission => 2,
            Permissionship::ConditionalPermission => 3,
            _ => 0,
        },
        caveat_evaluation_info: t.caveat_evaluation.as_ref().map(|c| proto::CaveatEvalInfo {
            expression: c.expression.clone(),
            result: match c.result {
                CaveatResult::Unevaluated => 1,
                CaveatResult::False => 2,
                CaveatResult::True => 3,
                CaveatResult::MissingContext => 4,
                _ => 0,
            },
            context: c.context.as_ref().map(check_context_to_proto),
            partial_caveat_info: partial_caveat_to_proto(&c.missing_context),
            caveat_name: c.name.clone(),
        }),
        duration: t.duration.map(|d| prost_types::Duration {
            seconds: d.num_seconds(),
            nanos: d.subsec_nanos(),
        }),
        resolution: t.resolution.as_ref().map(|r| match r {
            TraceResolution::Cached(c) => Resolution::WasCachedResult(*c),
            TraceResolution::Subproblems(s) => {
                Resolution::SubProblems(proto::check_debug_trace::SubProblems {
                    traces: s.iter().map(check_trace_to_proto).collect(),
                })
            }
        }),
        optional_expires_at: t.expiration.as_ref().map(timestamp),
        trace_operation_id: t.operation_id.clone(),
        source: t.source.clone(),
    }
}

pub fn lookup_resource_to_proto(r: &LookupResource) -> proto::LookupResourcesResponse {
    proto::LookupResourcesResponse {
        looked_up_at: token(&r.looked_up_at),
        resource_object_id: r.resource_id.clone(),
        permissionship: lookup_permissionship_to_proto(&r.permissionship),
        partial_caveat_info: r
            .partial_caveat
            .as_ref()
            .and_then(|c| partial_caveat_to_proto(&c.missing_required_context)),
        after_result_cursor: r
            .after_result_cursor
            .clone()
            .map(|token| proto::Cursor { token }),
    }
}

fn resolved_subject_to_proto(s: &ResolvedSubject) -> proto::ResolvedSubject {
    proto::ResolvedSubject {
        subject_object_id: s.subject_id.clone(),
        permissionship: lookup_permissionship_to_proto(&s.permissionship),
        partial_caveat_info: s
            .partial_caveat
            .as_ref()
            .and_then(|c| partial_caveat_to_proto(&c.missing_required_context)),
    }
}

#[allow(deprecated)]
pub fn lookup_subject_to_proto(s: &LookupSubject) -> proto::LookupSubjectsResponse {
    proto::LookupSubjectsResponse {
        looked_up_at: token(&s.looked_up_at),
        subject: Some(resolved_subject_to_proto(&s.subject)),
        excluded_subjects: s
            .excluded_subjects
            .iter()
            .map(resolved_subject_to_proto)
            .collect(),
        ..Default::default()
    }
}

/// `None` for `PermissionTree::Unknown`.
pub fn permission_tree_to_proto(t: &PermissionTree) -> Option<proto::PermissionRelationshipTree> {
    use proto::permission_relationship_tree::TreeType;
    Some(match t {
        PermissionTree::Intermediate {
            expanded_object,
            expanded_relation,
            operation,
            children,
        } => proto::PermissionRelationshipTree {
            expanded_object: Some(expanded_object.to_proto()),
            expanded_relation: expanded_relation.clone(),
            tree_type: Some(TreeType::Intermediate(proto::AlgebraicSubjectSet {
                operation: match operation {
                    TreeOperation::Union => 1,
                    TreeOperation::Intersection => 2,
                    TreeOperation::Exclusion => 3,
                    _ => 0,
                },
                children: children
                    .iter()
                    .filter_map(permission_tree_to_proto)
                    .collect(),
            })),
        },
        PermissionTree::Leaf {
            expanded_object,
            expanded_relation,
            subjects,
        } => proto::PermissionRelationshipTree {
            expanded_object: Some(expanded_object.to_proto()),
            expanded_relation: expanded_relation.clone(),
            tree_type: Some(TreeType::Leaf(proto::DirectSubjectSet {
                subjects: subjects.iter().map(SubjectRef::to_proto).collect(),
            })),
        },
        _ => return None,
    })
}

pub fn expand_result_to_proto(
    r: &ExpandPermissionTreeResult,
) -> proto::ExpandPermissionTreeResponse {
    proto::ExpandPermissionTreeResponse {
        expanded_at: token(&r.revision),
        tree_root: permission_tree_to_proto(&r.tree),
    }
}

pub fn read_relationship_to_proto(r: &ReadRelationship) -> proto::ReadRelationshipsResponse {
    proto::ReadRelationshipsResponse {
        read_at: token(&r.read_at),
        relationship: Some(r.relationship.to_proto()),
        after_result_cursor: r
            .after_result_cursor
            .clone()
            .map(|token| proto::Cursor { token }),
    }
}

pub fn write_result_to_proto(r: &WriteResult) -> proto::WriteRelationshipsResponse {
    proto::WriteRelationshipsResponse {
        written_at: token(&r.revision),
    }
}

pub fn write_schema_result_to_proto(r: &WriteSchemaResult) -> proto::WriteSchemaResponse {
    proto::WriteSchemaResponse {
        written_at: token(&r.revision),
    }
}
