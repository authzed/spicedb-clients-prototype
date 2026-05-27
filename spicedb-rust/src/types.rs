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

/// The result of an expand permission tree call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ExpandResult {
    /// The revision at which the tree was expanded.
    pub revision: String,
    // TODO: When spicedb-proto types are available, add:
    // pub tree_root: proto::PermissionRelationshipTree,
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
}
