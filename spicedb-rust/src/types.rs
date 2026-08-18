//! Core types for SpiceDB relationships, filters, and transactions.
//!
//! Types in this module are independent of the client — you can construct
//! relationships and filters without creating a client.

use std::collections::HashMap;
use std::fmt;

use chrono::{DateTime, Utc};
use spicedb_proto::authzed::api::v1 as proto;

/// A flat representation of a SpiceDB relationship.
///
/// Avoids nested proto types in favor of plain Rust fields. All fields use
/// owned `String` values for ergonomics; borrow when passing to client methods
/// via `&Relationship`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Relationship {
    pub resource_type: String,
    pub resource_id: String,
    pub resource_relation: String,
    pub subject_type: String,
    pub subject_id: String,
    pub subject_relation: String,
    pub caveat_name: String,
    pub caveat_context: Option<HashMap<String, serde_json::Value>>,
    pub expiration: Option<DateTime<Utc>>,
}

impl Relationship {
    /// Creates a new relationship from resource and subject components.
    ///
    /// Returns an error if resource_type, resource_id, resource_relation,
    /// subject_type, or subject_id are empty.
    pub fn new(
        resource_type: impl Into<String>,
        resource_id: impl Into<String>,
        resource_relation: impl Into<String>,
        subject_type: impl Into<String>,
        subject_id: impl Into<String>,
        subject_relation: impl Into<String>,
    ) -> Result<Self, RelationshipError> {
        let r = Self {
            resource_type: resource_type.into(),
            resource_id: resource_id.into(),
            resource_relation: resource_relation.into(),
            subject_type: subject_type.into(),
            subject_id: subject_id.into(),
            subject_relation: subject_relation.into(),
            caveat_name: String::new(),
            caveat_context: None,
            expiration: None,
        };
        r.validate()?;
        Ok(r)
    }

    /// Creates a new relationship from resource object, relation, and subject
    /// object (no subject relation).
    pub fn from_objects(
        resource_type: impl Into<String>,
        resource_id: impl Into<String>,
        relation: impl Into<String>,
        subject_type: impl Into<String>,
        subject_id: impl Into<String>,
    ) -> Result<Self, RelationshipError> {
        Self::new(
            resource_type,
            resource_id,
            relation,
            subject_type,
            subject_id,
            "",
        )
    }

    /// Parses a relationship from a tuple string in the format:
    ///
    /// `resourceType:resourceID#relation@subjectType:subjectID[#subjectRelation]`
    pub fn from_tuple(tuple: &str) -> Result<Self, RelationshipError> {
        let (resource_part, subject_part) = tuple
            .split_once('@')
            .ok_or_else(|| RelationshipError::InvalidFormat("missing '@' separator".into()))?;

        let (resource_type_id, resource_relation) = resource_part
            .split_once('#')
            .ok_or_else(|| RelationshipError::InvalidFormat("missing '#' in resource".into()))?;

        let (resource_type, resource_id) = resource_type_id.split_once(':').ok_or_else(|| {
            RelationshipError::InvalidFormat("missing ':' in resource type:id".into())
        })?;

        let (subject_type, subject_id, subject_relation) =
            if let Some((subject_type_id, subject_rel)) = subject_part.split_once('#') {
                let (st, si) = subject_type_id.split_once(':').ok_or_else(|| {
                    RelationshipError::InvalidFormat("missing ':' in subject type:id".into())
                })?;
                (st, si, subject_rel)
            } else {
                let (st, si) = subject_part.split_once(':').ok_or_else(|| {
                    RelationshipError::InvalidFormat("missing ':' in subject type:id".into())
                })?;
                (st, si, "")
            };

        Self::new(
            resource_type,
            resource_id,
            resource_relation,
            subject_type,
            subject_id,
            subject_relation,
        )
    }

    /// Returns a copy of this relationship with the given caveat attached.
    pub fn with_caveat(
        mut self,
        name: impl Into<String>,
        context: Option<HashMap<String, serde_json::Value>>,
    ) -> Self {
        self.caveat_name = name.into();
        self.caveat_context = context;
        self
    }

    /// Returns a copy of this relationship with the given expiration.
    pub fn with_expiration(mut self, expiration: DateTime<Utc>) -> Self {
        self.expiration = Some(expiration);
        self
    }

    /// Returns a [`Filter`] that matches the exact resource of this relationship.
    pub fn filter(&self) -> Filter {
        Filter {
            resource_type: self.resource_type.clone(),
            resource_id: Some(self.resource_id.clone()),
            resource_id_prefix: None,
            relation: Some(self.resource_relation.clone()),
            subject_type: Some(self.subject_type.clone()),
            subject_id: Some(self.subject_id.clone()),
            subject_relation: None,
        }
    }

    fn validate(&self) -> Result<(), RelationshipError> {
        if self.resource_type.is_empty()
            || self.resource_id.is_empty()
            || self.resource_relation.is_empty()
        {
            return Err(RelationshipError::InvalidResource);
        }
        if self.subject_type.is_empty() || self.subject_id.is_empty() {
            return Err(RelationshipError::InvalidSubject);
        }
        Ok(())
    }

    pub(crate) fn to_proto(&self) -> proto::Relationship {
        let optional_caveat = if self.caveat_name.is_empty() {
            None
        } else {
            let context = self.caveat_context.as_ref().map(|ctx| {
                let fields = ctx
                    .iter()
                    .map(|(k, v)| (k.clone(), json_value_to_prost(v)))
                    .collect();
                prost_types::Struct { fields }
            });
            Some(proto::ContextualizedCaveat {
                caveat_name: self.caveat_name.clone(),
                context,
            })
        };

        let optional_expires_at = self.expiration.map(|exp| prost_types::Timestamp {
            seconds: exp.timestamp(),
            nanos: exp.timestamp_subsec_nanos() as i32,
        });

        proto::Relationship {
            resource: Some(proto::ObjectReference {
                object_type: self.resource_type.clone(),
                object_id: self.resource_id.clone(),
            }),
            relation: self.resource_relation.clone(),
            subject: Some(proto::SubjectReference {
                object: Some(proto::ObjectReference {
                    object_type: self.subject_type.clone(),
                    object_id: self.subject_id.clone(),
                }),
                optional_relation: self.subject_relation.clone(),
            }),
            optional_caveat,
            optional_expires_at,
        }
    }

    pub(crate) fn from_proto(pr: &proto::Relationship) -> Self {
        let resource = pr.resource.as_ref();
        let subject_ref = pr.subject.as_ref();
        let subject_obj = subject_ref.and_then(|s| s.object.as_ref());

        let (caveat_name, caveat_context) = if let Some(cav) = &pr.optional_caveat {
            let context = cav.context.as_ref().map(|s| {
                s.fields
                    .iter()
                    .map(|(k, v)| (k.clone(), prost_value_to_json(v)))
                    .collect()
            });
            (cav.caveat_name.clone(), context)
        } else {
            (String::new(), None)
        };

        let expiration = pr
            .optional_expires_at
            .as_ref()
            .and_then(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32));

        Self {
            resource_type: resource.map(|r| r.object_type.clone()).unwrap_or_default(),
            resource_id: resource.map(|r| r.object_id.clone()).unwrap_or_default(),
            resource_relation: pr.relation.clone(),
            subject_type: subject_obj
                .map(|o| o.object_type.clone())
                .unwrap_or_default(),
            subject_id: subject_obj.map(|o| o.object_id.clone()).unwrap_or_default(),
            subject_relation: subject_ref
                .map(|s| s.optional_relation.clone())
                .unwrap_or_default(),
            caveat_name,
            caveat_context,
            expiration,
        }
    }
}

impl fmt::Display for Relationship {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "{}:{}#{}@{}:{}",
            self.resource_type,
            self.resource_id,
            self.resource_relation,
            self.subject_type,
            self.subject_id,
        )?;
        if !self.subject_relation.is_empty() {
            write!(f, "#{}", self.subject_relation)?;
        }
        Ok(())
    }
}

/// Errors that can occur when constructing or validating relationships.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum RelationshipError {
    /// Resource type, ID, or relation is empty.
    #[error("resource type, id, and relation are required")]
    InvalidResource,

    /// Subject type or ID is empty.
    #[error("subject type and id are required")]
    InvalidSubject,

    /// The tuple string format is invalid.
    #[error("invalid tuple format: {0}")]
    InvalidFormat(String),
}

/// Specifies criteria for matching relationships in read and delete operations.
///
/// Use the builder methods to narrow the filter. A filter requires at minimum
/// a resource type.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Filter {
    pub resource_type: String,
    pub resource_id: Option<String>,
    pub resource_id_prefix: Option<String>,
    pub relation: Option<String>,
    pub subject_type: Option<String>,
    pub subject_id: Option<String>,
    pub subject_relation: Option<String>,
}

impl Filter {
    /// Creates a new filter that matches relationships of the given resource type.
    pub fn new(resource_type: impl Into<String>) -> Self {
        Self {
            resource_type: resource_type.into(),
            resource_id: None,
            resource_id_prefix: None,
            relation: None,
            subject_type: None,
            subject_id: None,
            subject_relation: None,
        }
    }

    /// Narrows the filter to a specific resource ID.
    pub fn with_resource_id(mut self, id: impl Into<String>) -> Self {
        self.resource_id = Some(id.into());
        self
    }

    /// Narrows the filter to resource IDs with the given prefix.
    pub fn with_resource_id_prefix(mut self, prefix: impl Into<String>) -> Self {
        self.resource_id_prefix = Some(prefix.into());
        self
    }

    /// Narrows the filter to a specific relation.
    pub fn with_relation(mut self, relation: impl Into<String>) -> Self {
        self.relation = Some(relation.into());
        self
    }

    /// Narrows the filter to a specific subject type.
    pub fn with_subject_type(mut self, subject_type: impl Into<String>) -> Self {
        self.subject_type = Some(subject_type.into());
        self
    }

    /// Narrows the filter to a specific subject ID.
    pub fn with_subject_id(mut self, subject_id: impl Into<String>) -> Self {
        self.subject_id = Some(subject_id.into());
        self
    }

    /// Narrows the filter to a specific subject relation.
    pub fn with_subject_relation(mut self, relation: impl Into<String>) -> Self {
        self.subject_relation = Some(relation.into());
        self
    }

    pub(crate) fn to_proto(&self) -> proto::RelationshipFilter {
        let optional_subject_filter = if self.subject_type.is_some()
            || self.subject_id.is_some()
            || self.subject_relation.is_some()
        {
            Some(proto::SubjectFilter {
                subject_type: self.subject_type.clone().unwrap_or_default(),
                optional_subject_id: self.subject_id.clone().unwrap_or_default(),
                optional_relation: self.subject_relation.as_ref().map(|r| {
                    proto::subject_filter::RelationFilter {
                        relation: r.clone(),
                    }
                }),
            })
        } else {
            None
        };

        proto::RelationshipFilter {
            resource_type: self.resource_type.clone(),
            optional_resource_id: self.resource_id.clone().unwrap_or_default(),
            optional_resource_id_prefix: self.resource_id_prefix.clone().unwrap_or_default(),
            optional_relation: self.relation.clone().unwrap_or_default(),
            optional_subject_filter,
        }
    }
}

/// The type of mutation in a relationship update.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum UpdateOperation {
    /// Create the relationship. Fails if it already exists.
    Create,
    /// Create or update the relationship.
    Touch,
    /// Delete the relationship.
    Delete,
}

/// A relationship mutation (create, touch, or delete) as received from the
/// watch API.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Update {
    pub operation: UpdateOperation,
    pub relationship: Relationship,
}

/// A transaction builder for batching relationship writes.
///
/// Transactions take `&Relationship` (borrow) rather than moving the
/// relationship, so callers can reuse relationship values.
#[derive(Debug, Clone, Default)]
pub struct Transaction {
    updates: Vec<(UpdateOperation, Relationship)>,
    preconditions: Vec<Precondition>,
}

/// A precondition that must hold for a write transaction to succeed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Precondition {
    pub operation: PreconditionOperation,
    pub filter: Filter,
}

/// The type of precondition check.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PreconditionOperation {
    /// The filter must NOT match any existing relationships.
    MustNotMatch,
    /// The filter MUST match at least one existing relationship.
    MustMatch,
}

/// Optional preconditions and a page-size override for
/// [`SpiceDBClient::delete_relationships_with`](crate::client::SpiceDBClient::delete_relationships_with).
///
/// Use [`DeleteOptions::default`] (or [`DeleteOptions::new`]) for no
/// preconditions and the default page size (1,000), and the builder methods
/// to add guards:
///
/// ```
/// use spicedb::types::{DeleteOptions, Filter};
///
/// let options = DeleteOptions::new()
///     .with_must_match(Filter::new("document").with_resource_id("doc1"))
///     .with_must_not_match(Filter::new("document").with_resource_id("doc2"))
///     .with_limit(100);
/// ```
///
/// # Preconditions and paging
///
/// Preconditions are a per-request proto field, so when a delete spans
/// multiple pages (more matches than the limit), they are re-evaluated by the
/// server on every page — there is no "check-once, apply-to-all-pages"
/// semantics. This means a delete that starts successfully can still fail
/// partway through if the guarded state changes between pages, after earlier
/// pages have already been deleted. For a single-shot, all-or-nothing guarded
/// delete, pair the precondition with [`DeleteOptions::with_limit`] set large
/// enough to cover every matching relationship in one call.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct DeleteOptions {
    /// Filters that must each match at least one existing relationship for
    /// the delete to proceed.
    pub must_match: Vec<Filter>,
    /// Filters that must each match no existing relationship for the delete
    /// to proceed.
    pub must_not_match: Vec<Filter>,
    /// Overrides the default per-request page size (1,000) used by
    /// `delete_relationships`'/`delete_relationships_with`'s auto-paging loop.
    pub limit: Option<u32>,
}

impl DeleteOptions {
    /// Creates an empty `DeleteOptions`: no preconditions, default page size.
    pub fn new() -> Self {
        Self::default()
    }

    /// Adds a precondition that at least one relationship must match
    /// `filter`. Multiple calls accumulate; all are sent with every request.
    pub fn with_must_match(mut self, filter: Filter) -> Self {
        self.must_match.push(filter);
        self
    }

    /// Adds a precondition that no relationship may match `filter`. Multiple
    /// calls accumulate; all are sent with every request.
    pub fn with_must_not_match(mut self, filter: Filter) -> Self {
        self.must_not_match.push(filter);
        self
    }

    /// Overrides the default per-request page size (1,000).
    pub fn with_limit(mut self, limit: u32) -> Self {
        self.limit = Some(limit);
        self
    }

    /// Converts the accumulated must-match/must-not-match filters into
    /// [`Precondition`]s (must-match filters first, then must-not-match).
    ///
    /// This mirrors [`Transaction::preconditions`]'s shape so both delete and
    /// write preconditions flow through the same proto conversion in
    /// `client.rs`.
    pub(crate) fn preconditions(&self) -> Vec<Precondition> {
        let mut preconditions =
            Vec::with_capacity(self.must_match.len() + self.must_not_match.len());
        preconditions.extend(self.must_match.iter().cloned().map(|filter| Precondition {
            operation: PreconditionOperation::MustMatch,
            filter,
        }));
        preconditions.extend(
            self.must_not_match
                .iter()
                .cloned()
                .map(|filter| Precondition {
                    operation: PreconditionOperation::MustNotMatch,
                    filter,
                }),
        );
        preconditions
    }
}

impl Transaction {
    /// Creates an empty transaction.
    pub fn new() -> Self {
        Self::default()
    }

    /// Adds a relationship create to the transaction. The write will fail if the
    /// relationship already exists.
    pub fn create(&mut self, relationship: &Relationship) {
        self.updates
            .push((UpdateOperation::Create, relationship.clone()));
    }

    /// Adds a relationship touch to the transaction. Creates or updates the
    /// relationship.
    pub fn touch(&mut self, relationship: &Relationship) {
        self.updates
            .push((UpdateOperation::Touch, relationship.clone()));
    }

    /// Adds a relationship delete to the transaction.
    pub fn delete(&mut self, relationship: &Relationship) {
        self.updates
            .push((UpdateOperation::Delete, relationship.clone()));
    }

    /// Adds a precondition that no relationships must match the given filter.
    pub fn must_not_match(&mut self, filter: Filter) {
        self.preconditions.push(Precondition {
            operation: PreconditionOperation::MustNotMatch,
            filter,
        });
    }

    /// Adds a precondition that at least one relationship must match the given filter.
    pub fn must_match(&mut self, filter: Filter) {
        self.preconditions.push(Precondition {
            operation: PreconditionOperation::MustMatch,
            filter,
        });
    }

    /// Returns the updates in this transaction.
    pub fn updates(&self) -> &[(UpdateOperation, Relationship)] {
        &self.updates
    }

    /// Returns the preconditions in this transaction.
    pub fn preconditions(&self) -> &[Precondition] {
        &self.preconditions
    }

    /// Returns true if the transaction has no updates.
    pub fn is_empty(&self) -> bool {
        self.updates.is_empty()
    }

    /// Returns the number of updates in this transaction.
    pub fn len(&self) -> usize {
        self.updates.len()
    }

    // TODO: When spicedb-proto types are available, add:
    //
    // pub(crate) fn to_proto_updates(&self) -> Vec<proto::RelationshipUpdate> { ... }
    // pub(crate) fn to_proto_preconditions(&self) -> Vec<proto::Precondition> { ... }
}

/// Schema reflection types returned by `reflect_schema`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SchemaDefinition {
    pub name: String,
    pub comment: String,
    pub relations: Vec<SchemaRelation>,
    pub permissions: Vec<SchemaPermission>,
}

/// A relation within a schema definition.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SchemaRelation {
    pub name: String,
    pub comment: String,
    pub parent_definition_name: String,
}

/// A permission within a schema definition.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SchemaPermission {
    pub name: String,
    pub comment: String,
    pub parent_definition_name: String,
}

/// A caveat defined in a SpiceDB schema.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SchemaCaveat {
    pub name: String,
    pub comment: String,
    pub expression: String,
    pub parameters: Vec<SchemaCaveatParameter>,
}

/// A parameter of a caveat.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SchemaCaveatParameter {
    pub name: String,
    pub type_name: String,
    pub parent_caveat_name: String,
}

/// The result of a schema reflection call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReflectSchemaResult {
    pub definitions: Vec<SchemaDefinition>,
    pub caveats: Vec<SchemaCaveat>,
    pub revision: String,
}

/// Identifies a relation or permission on a definition (used by computable
/// permissions and dependent relations).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RelationReference {
    pub definition_name: String,
    pub relation_name: String,
    pub is_permission: bool,
}

/// A single difference between two schemas.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SchemaDiff {
    /// A human-readable description of the diff type (e.g. "definition_added",
    /// "relation_removed", "permission_expr_changed").
    pub kind: String,
    /// Set for definition and relation/permission-level diffs.
    pub definition_name: String,
    /// Set for relation-level diffs.
    pub relation_name: String,
    /// Set for permission-level diffs.
    pub permission_name: String,
    /// Set for caveat-level diffs.
    pub caveat_name: String,
}

/// Identifies an object (resource or subject) by type and ID.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ObjectRef {
    pub object_type: String,
    pub object_id: String,
}

/// The algebraic set operation combining an [`IntermediateNode`]'s children.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TreeOperation {
    Unspecified,
    Union,
    Intersection,
    Exclusion,
}

/// A subject with access at a leaf of the permission tree.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SubjectRef {
    pub subject_type: String,
    pub subject_id: String,
    /// Empty if the subject reference has no sub-relation.
    pub subject_relation: String,
}

/// Combines child subtrees with a [`TreeOperation`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct IntermediateNode {
    pub operation: TreeOperation,
    pub children: Vec<PermissionTree>,
}

/// Holds the concrete subjects at a leaf of the permission tree.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LeafNode {
    pub subjects: Vec<SubjectRef>,
}

/// A native node of an expanded permission tree, as returned by
/// [`SpiceDBClient::expand_permission_tree`](crate::client::SpiceDBClient::expand_permission_tree).
///
/// Exactly one of `intermediate` or `leaf` is `Some`: `intermediate` nodes
/// represent a set operation (union, intersection, exclusion) over child
/// subtrees, while `leaf` nodes hold the concrete resolved subjects.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct PermissionTree {
    pub expanded_object: ObjectRef,
    pub expanded_relation: String,
    pub intermediate: Option<IntermediateNode>,
    pub leaf: Option<LeafNode>,
}

/// Maps the proto `AlgebraicSubjectSet.Operation` enum (represented as `i32`
/// by prost) to its native equivalent. Unrecognized values map to
/// `TreeOperation::Unspecified`.
pub(crate) fn tree_operation_from_proto(v: i32) -> TreeOperation {
    match v {
        x if x == proto::algebraic_subject_set::Operation::Union as i32 => TreeOperation::Union,
        x if x == proto::algebraic_subject_set::Operation::Intersection as i32 => {
            TreeOperation::Intersection
        }
        x if x == proto::algebraic_subject_set::Operation::Exclusion as i32 => {
            TreeOperation::Exclusion
        }
        _ => TreeOperation::Unspecified,
    }
}

/// Recursively maps a proto `PermissionRelationshipTree` to its native
/// representation. `None` maps to a zero-value `PermissionTree`.
pub(crate) fn permission_tree_from_proto(
    t: Option<&proto::PermissionRelationshipTree>,
) -> PermissionTree {
    let Some(t) = t else {
        return PermissionTree::default();
    };

    let expanded_object = t
        .expanded_object
        .as_ref()
        .map(|o| ObjectRef {
            object_type: o.object_type.clone(),
            object_id: o.object_id.clone(),
        })
        .unwrap_or_default();

    let mut tree = PermissionTree {
        expanded_object,
        expanded_relation: t.expanded_relation.clone(),
        intermediate: None,
        leaf: None,
    };

    match &t.tree_type {
        Some(proto::permission_relationship_tree::TreeType::Intermediate(intermediate)) => {
            let children = intermediate
                .children
                .iter()
                .map(|child| permission_tree_from_proto(Some(child)))
                .collect();
            tree.intermediate = Some(IntermediateNode {
                operation: tree_operation_from_proto(intermediate.operation),
                children,
            });
        }
        Some(proto::permission_relationship_tree::TreeType::Leaf(leaf)) => {
            let subjects = leaf
                .subjects
                .iter()
                .map(|subject| SubjectRef {
                    subject_type: subject
                        .object
                        .as_ref()
                        .map(|o| o.object_type.clone())
                        .unwrap_or_default(),
                    subject_id: subject
                        .object
                        .as_ref()
                        .map(|o| o.object_id.clone())
                        .unwrap_or_default(),
                    subject_relation: subject.optional_relation.clone(),
                })
                .collect();
            tree.leaf = Some(LeafNode { subjects });
        }
        None => {}
    }

    tree
}

/// The result of an expand permission tree call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ExpandResult {
    /// The root of the expanded permission tree.
    pub tree: PermissionTree,
    /// The revision at which the tree was expanded.
    pub revision: String,
}

/// The result of a relationship count operation.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CountResult {
    pub relationship_count: u64,
    pub revision: String,
}

/// Result of a check_permission call.
///
/// Marked `#[must_use]` to prevent silently ignoring permission check results.
#[must_use]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CheckResult {
    /// Whether the permission is granted.
    pub has_permission: bool,
}

/// Indicates whether a lookup result reflects a full grant or is conditional
/// on caveat context that was not fully evaluated by the server. Callers
/// MUST check this before treating a result as a full grant — a
/// `ConditionalPermission` result may resolve to `false` once the missing
/// caveat context is supplied.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Permissionship {
    Unspecified,
    HasPermission,
    ConditionalPermission,
}

/// Caveat context that was missing to fully evaluate a conditional result.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PartialCaveatInfo {
    pub missing_required_context: Vec<String>,
}

/// One result from `lookup_resources`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LookupResource {
    pub resource_id: String,
    pub permissionship: Permissionship,
    /// Non-`None` when `permissionship` is `ConditionalPermission`.
    pub partial_caveat: Option<PartialCaveatInfo>,
}

/// A subject resolved by `lookup_subjects` — either the matched subject, or
/// (when found in [`LookupSubject::excluded_subjects`]) a subject excluded
/// from a wildcard match.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ResolvedSubject {
    pub subject_id: String,
    pub permissionship: Permissionship,
    pub partial_caveat: Option<PartialCaveatInfo>,
}

/// One result from `lookup_subjects`. When `subject.subject_id` is the
/// wildcard `"*"`, the server has granted the permission to every subject of
/// the requested subject type EXCEPT those listed in `excluded_subjects`.
/// Callers MUST check `excluded_subjects` before treating a wildcard match as
/// a blanket grant, or they risk granting access to subjects the server
/// explicitly excluded.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LookupSubject {
    pub subject: ResolvedSubject,
    pub excluded_subjects: Vec<ResolvedSubject>,
}

/// Maps the proto `LookupPermissionship` enum (represented as `i32` by
/// prost) to its native equivalent. Unrecognized values map to
/// `Permissionship::Unspecified`.
pub(crate) fn permissionship_from_proto(v: i32) -> Permissionship {
    match v {
        x if x == proto::LookupPermissionship::HasPermission as i32 => {
            Permissionship::HasPermission
        }
        x if x == proto::LookupPermissionship::ConditionalPermission as i32 => {
            Permissionship::ConditionalPermission
        }
        _ => Permissionship::Unspecified,
    }
}

/// Maps a proto `PartialCaveatInfo` to its native equivalent. `None` maps to
/// `None`.
pub(crate) fn partial_caveat_from_proto(
    v: Option<&proto::PartialCaveatInfo>,
) -> Option<PartialCaveatInfo> {
    v.map(|c| PartialCaveatInfo {
        missing_required_context: c.missing_required_context.clone(),
    })
}

/// Maps a proto `ResolvedSubject` to its native equivalent. `None` maps to a
/// zero-value `ResolvedSubject` (empty `subject_id`), which callers use as
/// the trigger for falling back to deprecated response-level fields.
pub(crate) fn resolved_subject_from_proto(v: Option<&proto::ResolvedSubject>) -> ResolvedSubject {
    match v {
        Some(v) => ResolvedSubject {
            subject_id: v.subject_object_id.clone(),
            permissionship: permissionship_from_proto(v.permissionship),
            partial_caveat: partial_caveat_from_proto(v.partial_caveat_info.as_ref()),
        },
        None => ResolvedSubject {
            subject_id: String::new(),
            permissionship: Permissionship::Unspecified,
            partial_caveat: None,
        },
    }
}

/// Convert a `serde_json::Value` to a `prost_types::Value`.
fn json_value_to_prost(v: &serde_json::Value) -> prost_types::Value {
    use prost_types::value::Kind;
    let kind = match v {
        serde_json::Value::Null => Some(Kind::NullValue(0)),
        serde_json::Value::Bool(b) => Some(Kind::BoolValue(*b)),
        serde_json::Value::Number(n) => Some(Kind::NumberValue(n.as_f64().unwrap_or(0.0))),
        serde_json::Value::String(s) => Some(Kind::StringValue(s.clone())),
        serde_json::Value::Array(arr) => Some(Kind::ListValue(prost_types::ListValue {
            values: arr.iter().map(json_value_to_prost).collect(),
        })),
        serde_json::Value::Object(obj) => Some(Kind::StructValue(prost_types::Struct {
            fields: obj
                .iter()
                .map(|(k, v)| (k.clone(), json_value_to_prost(v)))
                .collect(),
        })),
    };
    prost_types::Value { kind }
}

/// Convert a `prost_types::Value` to a `serde_json::Value`.
fn prost_value_to_json(v: &prost_types::Value) -> serde_json::Value {
    use prost_types::value::Kind;
    match &v.kind {
        Some(Kind::NullValue(_)) => serde_json::Value::Null,
        Some(Kind::BoolValue(b)) => serde_json::Value::Bool(*b),
        Some(Kind::NumberValue(n)) => serde_json::Number::from_f64(*n)
            .map(serde_json::Value::Number)
            .unwrap_or(serde_json::Value::Null),
        Some(Kind::StringValue(s)) => serde_json::Value::String(s.clone()),
        Some(Kind::ListValue(list)) => {
            serde_json::Value::Array(list.values.iter().map(prost_value_to_json).collect())
        }
        Some(Kind::StructValue(s)) => {
            let map: serde_json::Map<String, serde_json::Value> = s
                .fields
                .iter()
                .map(|(k, v)| (k.clone(), prost_value_to_json(v)))
                .collect();
            serde_json::Value::Object(map)
        }
        None => serde_json::Value::Null,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

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
    fn test_relationship_new_invalid_resource() {
        let r = Relationship::new("", "doc1", "viewer", "user", "alice", "");
        assert_eq!(r.unwrap_err(), RelationshipError::InvalidResource);

        let r = Relationship::new("document", "", "viewer", "user", "alice", "");
        assert_eq!(r.unwrap_err(), RelationshipError::InvalidResource);

        let r = Relationship::new("document", "doc1", "", "user", "alice", "");
        assert_eq!(r.unwrap_err(), RelationshipError::InvalidResource);
    }

    #[test]
    fn test_relationship_new_invalid_subject() {
        let r = Relationship::new("document", "doc1", "viewer", "", "alice", "");
        assert_eq!(r.unwrap_err(), RelationshipError::InvalidSubject);

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
    fn test_relationship_from_tuple() {
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
    fn test_relationship_from_tuple_invalid() {
        assert!(Relationship::from_tuple("no-at-sign").is_err());
        assert!(Relationship::from_tuple("nohash@user:alice").is_err());
        assert!(Relationship::from_tuple("nocolon#rel@user:alice").is_err());
        assert!(Relationship::from_tuple("type:id#rel@nocolon").is_err());
    }

    #[test]
    fn test_relationship_display() {
        let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "").unwrap();
        assert_eq!(r.to_string(), "document:doc1#viewer@user:alice");

        let r = Relationship::new("document", "doc1", "viewer", "group", "eng", "member").unwrap();
        assert_eq!(r.to_string(), "document:doc1#viewer@group:eng#member");
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
        assert!(r.caveat_context.is_some());
    }

    #[test]
    fn test_relationship_with_expiration() {
        let exp = Utc::now();
        let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
            .unwrap()
            .with_expiration(exp);
        assert_eq!(r.expiration, Some(exp));
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
    fn test_relationship_immutable_modifiers() {
        let r = Relationship::new("document", "doc1", "viewer", "user", "alice", "").unwrap();
        let r2 = r.clone().with_caveat("caveat", None);
        // Original unchanged
        assert_eq!(r.caveat_name, "");
        assert_eq!(r2.caveat_name, "caveat");
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
    fn test_delete_options_default_is_empty() {
        let options = DeleteOptions::default();
        assert!(options.must_match.is_empty());
        assert!(options.must_not_match.is_empty());
        assert_eq!(options.limit, None);
        assert!(options.preconditions().is_empty());
    }

    #[test]
    fn test_delete_options_builder() {
        let must_match_filter = Filter::new("document").with_resource_id("doc1");
        let must_not_match_filter = Filter::new("document").with_resource_id("doc2");
        let options = DeleteOptions::new()
            .with_must_match(must_match_filter.clone())
            .with_must_not_match(must_not_match_filter.clone())
            .with_limit(100);

        assert_eq!(options.must_match, vec![must_match_filter]);
        assert_eq!(options.must_not_match, vec![must_not_match_filter]);
        assert_eq!(options.limit, Some(100));
    }

    #[test]
    fn test_delete_options_preconditions_must_match_before_must_not_match() {
        let must_match_filter = Filter::new("document").with_resource_id("doc1");
        let must_not_match_filter = Filter::new("document").with_resource_id("doc2");
        let options = DeleteOptions::new()
            .with_must_not_match(must_not_match_filter.clone())
            .with_must_match(must_match_filter.clone());

        let preconditions = options.preconditions();
        assert_eq!(preconditions.len(), 2);
        assert_eq!(preconditions[0].operation, PreconditionOperation::MustMatch);
        assert_eq!(preconditions[0].filter, must_match_filter);
        assert_eq!(
            preconditions[1].operation,
            PreconditionOperation::MustNotMatch
        );
        assert_eq!(preconditions[1].filter, must_not_match_filter);
    }

    #[test]
    fn test_delete_options_multiple_must_match_accumulate() {
        let f1 = Filter::new("document").with_resource_id("doc1");
        let f2 = Filter::new("document").with_resource_id("doc2");
        let options = DeleteOptions::new()
            .with_must_match(f1.clone())
            .with_must_match(f2.clone());

        assert_eq!(options.must_match, vec![f1, f2]);
        assert_eq!(options.preconditions().len(), 2);
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
        // Using the result (must_use is a compile-time lint, tested by compilation)
        assert!(result.has_permission);
    }

    #[test]
    fn test_update_operation() {
        assert_ne!(UpdateOperation::Create, UpdateOperation::Touch);
        assert_ne!(UpdateOperation::Touch, UpdateOperation::Delete);
        assert_ne!(UpdateOperation::Create, UpdateOperation::Delete);
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
    fn test_count_result() {
        let cr = CountResult {
            relationship_count: 42,
            revision: "rev-1".into(),
        };
        assert_eq!(cr.relationship_count, 42);
        assert_eq!(cr.revision, "rev-1");
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
    fn test_permission_tree_from_proto_none_yields_zero_value() {
        let tree = permission_tree_from_proto(None);
        assert_eq!(tree.expanded_object, ObjectRef::default());
        assert_eq!(tree.expanded_relation, "");
        assert!(tree.intermediate.is_none());
        assert!(tree.leaf.is_none());
    }

    #[test]
    fn test_permission_tree_from_proto_leaf() {
        let proto_tree = proto::PermissionRelationshipTree {
            expanded_object: Some(proto::ObjectReference {
                object_type: "document".to_string(),
                object_id: "firstdoc".to_string(),
            }),
            expanded_relation: "viewer".to_string(),
            tree_type: Some(proto::permission_relationship_tree::TreeType::Leaf(
                proto::DirectSubjectSet {
                    subjects: vec![proto::SubjectReference {
                        object: Some(proto::ObjectReference {
                            object_type: "user".to_string(),
                            object_id: "alice".to_string(),
                        }),
                        optional_relation: String::new(),
                    }],
                },
            )),
        };

        let tree = permission_tree_from_proto(Some(&proto_tree));
        assert_eq!(tree.expanded_object.object_type, "document");
        assert_eq!(tree.expanded_object.object_id, "firstdoc");
        assert_eq!(tree.expanded_relation, "viewer");
        assert!(tree.intermediate.is_none());
        let leaf = tree.leaf.expect("expected a leaf node");
        assert_eq!(leaf.subjects.len(), 1);
        assert_eq!(leaf.subjects[0].subject_type, "user");
        assert_eq!(leaf.subjects[0].subject_id, "alice");
        assert_eq!(leaf.subjects[0].subject_relation, "");
    }

    #[test]
    fn test_permission_tree_from_proto_intermediate_recurses() {
        let leaf_tree = proto::PermissionRelationshipTree {
            expanded_object: Some(proto::ObjectReference {
                object_type: "document".to_string(),
                object_id: "firstdoc".to_string(),
            }),
            expanded_relation: "editor".to_string(),
            tree_type: Some(proto::permission_relationship_tree::TreeType::Leaf(
                proto::DirectSubjectSet {
                    subjects: vec![proto::SubjectReference {
                        object: Some(proto::ObjectReference {
                            object_type: "user".to_string(),
                            object_id: "bob".to_string(),
                        }),
                        optional_relation: String::new(),
                    }],
                },
            )),
        };

        let proto_tree = proto::PermissionRelationshipTree {
            expanded_object: Some(proto::ObjectReference {
                object_type: "document".to_string(),
                object_id: "firstdoc".to_string(),
            }),
            expanded_relation: "view".to_string(),
            tree_type: Some(proto::permission_relationship_tree::TreeType::Intermediate(
                proto::AlgebraicSubjectSet {
                    operation: proto::algebraic_subject_set::Operation::Union as i32,
                    children: vec![leaf_tree],
                },
            )),
        };

        let tree = permission_tree_from_proto(Some(&proto_tree));
        assert_eq!(tree.expanded_relation, "view");
        assert!(tree.leaf.is_none());
        let intermediate = tree.intermediate.expect("expected an intermediate node");
        assert_eq!(intermediate.operation, TreeOperation::Union);
        assert_eq!(intermediate.children.len(), 1);
        let child_leaf = intermediate.children[0]
            .leaf
            .as_ref()
            .expect("expected child to be a leaf");
        assert_eq!(child_leaf.subjects[0].subject_id, "bob");
    }
}
