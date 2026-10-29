//! Core types for SpiceDB relationships, filters, and transactions.
//!
//! Types in this module are independent of the client — you can construct
//! relationships and filters without creating a client.

use std::collections::HashMap;
use std::fmt;

use chrono::{DateTime, Utc};
use spicedb_proto::authzed::api::v1 as proto;

use crate::error::SpiceDBError;

#[path = "observe.rs"]
mod observe;

/// Native JSON context. Requests own every nested value.
pub type Context = HashMap<String, serde_json::Value>;

/// An opaque server revision with lossless string conversion.
///
/// Revision arguments require this value rather than an authentication string.
/// ```compile_fail
/// spicedb::consistency::at_least("plain string");
/// ```
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, Default, PartialEq, Eq, Hash)]
#[non_exhaustive]
pub struct ZedToken(String);
impl From<String> for ZedToken {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl From<&str> for ZedToken {
    fn from(value: &str) -> Self {
        Self(value.into())
    }
}
impl From<&ZedToken> for ZedToken {
    fn from(value: &ZedToken) -> Self {
        value.clone()
    }
}
impl AsRef<str> for ZedToken {
    fn as_ref(&self) -> &str {
        &self.0
    }
}
impl fmt::Display for ZedToken {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}
impl ZedToken {
    pub fn into_string(self) -> String {
        self.0
    }
    pub(crate) fn to_proto(&self) -> proto::ZedToken {
        proto::ZedToken {
            token: self.0.clone(),
        }
    }
}

#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct Relationship {
    resource: ObjectRef,
    relation: String,
    subject: SubjectRef,
    caveat_name: String,
    #[cfg_attr(feature = "serde", serde(skip))]
    caveat_specified: bool,
    caveat_context: Option<Context>,
    expiration: Option<DateTime<Utc>>,
}
impl Relationship {
    pub fn new(
        resource: ObjectRef,
        relation: impl Into<String>,
        subject: SubjectRef,
    ) -> Result<Self, SpiceDBError> {
        let relation = relation.into();
        required("relation", &relation)?;
        Ok(Self {
            resource,
            relation,
            subject,
            caveat_name: String::new(),
            caveat_specified: false,
            caveat_context: None,
            expiration: None,
        })
    }
    pub fn resource(&self) -> &ObjectRef {
        &self.resource
    }
    pub fn relation(&self) -> &str {
        &self.relation
    }
    pub fn subject(&self) -> &SubjectRef {
        &self.subject
    }
    pub fn caveat_name(&self) -> &str {
        &self.caveat_name
    }
    pub fn caveat_context(&self) -> Option<&Context> {
        self.caveat_context.as_ref()
    }
    pub fn expiration(&self) -> Option<DateTime<Utc>> {
        self.expiration
    }
    pub fn with_caveat(mut self, name: impl Into<String>, context: Option<Context>) -> Self {
        self.caveat_name = name.into();
        self.caveat_specified = true;
        self.caveat_context = context;
        self
    }
    pub fn with_expiration(mut self, expiration: DateTime<Utc>) -> Self {
        self.expiration = Some(expiration);
        self
    }
    pub fn filter(&self) -> Filter {
        Filter::new(self.resource.object_type())
            .with_resource_id(self.resource.object_id())
            .with_relation(&self.relation)
            .with_subject_type(self.subject.object().object_type())
            .with_subject_id(self.subject.object().object_id())
            .with_subject_relation(
                self.subject
                    .relation()
                    .map_or(SubjectRelationFilter::Direct, |r| {
                        SubjectRelationFilter::Named(r.into())
                    }),
            )
    }
    pub(crate) fn validate(&self) -> Result<(), SpiceDBError> {
        if self.caveat_specified && self.caveat_name.is_empty() {
            return Err(invalid("caveat_name", "required when context is supplied"));
        }
        if let Some(c) = &self.caveat_context {
            validate_context(c)?;
        }
        Ok(())
    }
    pub(crate) fn to_proto(&self) -> proto::Relationship {
        proto::Relationship {
            resource: Some(self.resource.to_proto()),
            relation: self.relation.clone(),
            subject: Some(self.subject.to_proto()),
            optional_caveat: if self.caveat_name.is_empty() {
                None
            } else {
                Some(proto::ContextualizedCaveat {
                    caveat_name: self.caveat_name.clone(),
                    context: self.caveat_context.as_ref().map(check_context_to_proto),
                })
            },
            optional_expires_at: self.expiration.map(|t| prost_types::Timestamp {
                seconds: t.timestamp(),
                nanos: t.timestamp_subsec_nanos() as i32,
            }),
        }
    }
    pub(crate) fn from_proto(r: &proto::Relationship) -> Self {
        Self {
            resource: ObjectRef::from_proto(r.resource.as_ref()),
            relation: r.relation.clone(),
            subject: SubjectRef::from_proto(r.subject.as_ref()),
            caveat_specified: r.optional_caveat.is_some(),
            caveat_name: r
                .optional_caveat
                .as_ref()
                .map(|c| c.caveat_name.clone())
                .unwrap_or_default(),
            caveat_context: r
                .optional_caveat
                .as_ref()
                .and_then(|c| c.context.as_ref())
                .map(context_from_proto),
            expiration: r
                .optional_expires_at
                .as_ref()
                .and_then(timestamp_from_proto),
        }
    }
}
pub(crate) fn invalid(field: &str, reason: &str) -> SpiceDBError {
    SpiceDBError::InvalidArgument(format!("{field}: {reason}").into())
}
pub(crate) fn required(field: &str, value: &str) -> Result<(), SpiceDBError> {
    if value.is_empty() {
        Err(invalid(field, "must not be empty"))
    } else {
        Ok(())
    }
}
pub(crate) fn validate_context(context: &Context) -> Result<(), SpiceDBError> {
    fn value(path: &str, v: &serde_json::Value) -> Result<(), SpiceDBError> {
        match v {
            serde_json::Value::Number(n) => {
                let number = n
                    .as_f64()
                    .ok_or_else(|| invalid(path, "not representable as a finite JSON number"))?;
                if !number.is_finite()
                    || n.as_i64().is_some_and(|x| x as i128 != number as i128)
                    || n.as_u64().is_some_and(|x| x as u128 != number as u128)
                {
                    return Err(invalid(path, "not exactly representable in protobuf JSON"));
                }
            }
            serde_json::Value::Object(o) => {
                for (k, v) in o {
                    value(&format!("{path}.{k}"), v)?;
                }
            }
            serde_json::Value::Array(a) => {
                for (i, v) in a.iter().enumerate() {
                    value(&format!("{path}[{i}]"), v)?;
                }
            }
            _ => {}
        }
        Ok(())
    }
    for (k, v) in context {
        value(k, v)?;
    }
    Ok(())
}
pub(crate) fn context_from_proto(c: &prost_types::Struct) -> Context {
    c.fields
        .iter()
        .map(|(k, v)| (k.clone(), prost_value_to_json(v)))
        .collect()
}
pub(crate) fn timestamp_from_proto(t: &prost_types::Timestamp) -> Option<DateTime<Utc>> {
    u32::try_from(t.nanos)
        .ok()
        .and_then(|n| DateTime::from_timestamp(t.seconds, n))
}

#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum SubjectRelationFilter {
    Any,
    Direct,
    Named(String),
}

#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct Filter {
    pub(crate) resource_type: String,
    pub(crate) resource_id: Option<String>,
    pub(crate) resource_id_prefix: Option<String>,
    pub(crate) relation: Option<String>,
    pub(crate) subject_type: Option<String>,
    pub(crate) subject_id: Option<String>,
    pub(crate) subject_relation: SubjectRelationFilter,
}

impl Filter {
    pub fn resource_type(&self) -> &str {
        &self.resource_type
    }
    pub fn resource_id(&self) -> Option<&str> {
        self.resource_id.as_deref()
    }
    pub fn resource_id_prefix(&self) -> Option<&str> {
        self.resource_id_prefix.as_deref()
    }
    pub fn relation(&self) -> Option<&str> {
        self.relation.as_deref()
    }
    pub fn subject_type(&self) -> Option<&str> {
        self.subject_type.as_deref()
    }
    pub fn subject_id(&self) -> Option<&str> {
        self.subject_id.as_deref()
    }

    /// Creates a new filter that matches relationships of the given resource type.
    pub fn new(resource_type: impl Into<String>) -> Self {
        Self {
            resource_type: resource_type.into(),
            resource_id: None,
            resource_id_prefix: None,
            relation: None,
            subject_type: None,
            subject_id: None,
            subject_relation: SubjectRelationFilter::Any,
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
    /// `SubjectRelationFilter::Named("...")` is read as
    /// [`SubjectRelationFilter::Direct`]: `...` is SpiceDB's name for a
    /// direct subject.
    pub fn with_subject_relation(mut self, relation: SubjectRelationFilter) -> Self {
        self.subject_relation = match relation {
            SubjectRelationFilter::Named(n) if n == "..." => SubjectRelationFilter::Direct,
            other => other,
        };
        self
    }

    pub fn subject_relation(&self) -> &SubjectRelationFilter {
        &self.subject_relation
    }

    /// Converts this filter to its proto representation.
    ///
    /// # Errors
    ///
    /// Returns [`SpiceDBError::InvalidArgument`] if `subject_id` or `subject_relation` is set
    /// without `subject_type`. The wire's `SubjectFilter.subject_type` is a required field, so
    /// there is no way to express a subject ID/relation constraint without it, which makes
    /// silently dropping the constraint the one unsafe resolution: a caller who wrote
    /// `Filter::new("document").with_subject_id("alice")`, expecting to narrow to alice's
    /// relationships, would instead match every subject on every document -- e.g.
    /// `delete_relationships` would delete every relationship on every document, not just
    /// alice's. See root DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail",
    /// clause 1.
    pub(crate) fn to_proto(&self) -> Result<proto::RelationshipFilter, SpiceDBError> {
        required("resource_type", &self.resource_type)?;
        if self.resource_id.is_some() && self.resource_id_prefix.is_some() {
            return Err(invalid(
                "resource_id",
                "cannot combine with resource_id_prefix",
            ));
        }
        for (field, value) in [
            ("resource_id", self.resource_id.as_deref()),
            ("resource_id_prefix", self.resource_id_prefix.as_deref()),
            ("relation", self.relation.as_deref()),
            ("subject_id", self.subject_id.as_deref()),
        ] {
            if let Some(value) = value {
                required(field, value)?;
            }
        }
        let relation = match &self.subject_relation {
            SubjectRelationFilter::Any => None,
            SubjectRelationFilter::Direct => Some(proto::subject_filter::RelationFilter {
                relation: String::new(),
            }),
            SubjectRelationFilter::Named(name) => {
                required("subject_relation", name)?;
                // `...` means a direct subject (the wire's empty relation).
                Some(proto::subject_filter::RelationFilter {
                    relation: if name == "..." {
                        String::new()
                    } else {
                        name.clone()
                    },
                })
            }
        };
        let optional_subject_filter = match &self.subject_type {
            Some(t) => {
                required("subject_type", t)?;
                Some(proto::SubjectFilter {
                    subject_type: t.clone(),
                    optional_subject_id: self.subject_id.clone().unwrap_or_default(),
                    optional_relation: relation,
                })
            }
            None if self.subject_id.is_some() || relation.is_some() => {
                return Err(invalid(
                    "subject_type",
                    "required with subject_id or subject_relation",
                ))
            }
            None => None,
        };
        Ok(proto::RelationshipFilter {
            resource_type: self.resource_type.clone(),
            optional_resource_id: self.resource_id.clone().unwrap_or_default(),
            optional_resource_id_prefix: self.resource_id_prefix.clone().unwrap_or_default(),
            optional_relation: self.relation.clone().unwrap_or_default(),
            optional_subject_filter,
        })
    }
}

/// The type of mutation in a relationship update.
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum UpdateOperation {
    /// The server sent an operation this client does not recognize -- either
    /// `OPERATION_UNSPECIFIED` on the wire, or a future operation value added
    /// after this client shipped. Only ever produced by the watch stream;
    /// never construct it for a write.
    ///
    /// Never treat this as a write: a cache or index mirror consuming the
    /// watch stream that upserts on an unrecognized operation could turn a
    /// delete it doesn't understand into a silent write. Handle it explicitly
    /// (re-read the relationship, or fail the mirror closed) -- and note the
    /// update is still yielded, because dropping it silently would make the
    /// consumer miss a change it has no way to learn about.
    Unspecified,
    /// Create the relationship. Fails if it already exists.
    Create,
    /// Create or update the relationship.
    Touch,
    /// Delete the relationship.
    Delete,
}

/// A relationship mutation (create, touch, or delete) as received from the
/// watch API.
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct Update {
    pub operation: UpdateOperation,
    pub relationship: Relationship,
}

/// A single event from [`SpiceDBClient::updates`](crate::client::SpiceDBClient::updates),
/// corresponding to one `WatchResponse` from the server.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct WatchEvent {
    pub schema_updated: bool,
    pub full_revision_metadata: Vec<Context>,
    pub transaction_metadata: Option<Context>,
    pub updates: Vec<Update>,
    /// The point in time this event is current through. Always populated.
    /// Proto: "the ZedToken that represents the point in time that the
    /// watch response is current through. This token can be used in a
    /// subsequent WatchRequest to resume watching from this point." Pass it
    /// as `start_revision` to a later `updates` call to resume after a
    /// dropped stream, instead of restarting from the original
    /// `start_revision` (reprocessing everything since, possibly past the
    /// GC window) or from head (silently losing every change in the gap).
    pub changes_through: ZedToken,
    /// True for a checkpoint event, which carries no [`updates`](Self::updates) -- it
    /// exists only to advertise a fresh [`changes_through`](Self::changes_through) and,
    /// behind a proxy that aborts idle connections, to keep the stream
    /// alive. Checkpoints are only sent when
    /// [`WatchOptions::with_checkpoints`] was passed to `updates`.
    pub is_checkpoint: bool,
}

/// Optional settings for [`SpiceDBClient::updates`](crate::client::SpiceDBClient::updates).
///
/// Watch from a typed revision and opt into periodic checkpoint delivery.
///
/// ```
/// use spicedb::{WatchOptions, ZedToken, CheckpointMode};
/// let options = WatchOptions::new()
///     .with_start_revision(ZedToken::from("some-zedtoken"))
///     .with_checkpoints(CheckpointMode::Include);
/// ```
#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct WatchOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) filters: Vec<Filter>,
    /// Resume from this revision instead of from head. Pass a
    /// [`WatchEvent::changes_through`] from a previous stream to pick up
    /// exactly where it stopped.
    pub(crate) start_revision: Option<ZedToken>,
    /// Request periodic checkpoint events ([`WatchEvent::is_checkpoint`],
    /// carrying no updates) in addition to relationship updates.
    /// Recommended if this SpiceDB instance is running behind a proxy that
    /// aborts idle connections, since a checkpoint keeps the stream alive
    /// even when there are no changes.
    pub(crate) checkpoints: CheckpointMode,
    pub(crate) content: WatchContent,
}

impl WatchOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn with_content(mut self, content: WatchContent) -> Self {
        self.content = content;
        self
    }
    pub fn with_timeout(mut self, timeout: std::time::Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }
    /// Watch relationships matching any filter (OR). Empty filters watch all relationships.
    /// Cannot be combined with object types in UpdatesRequest.
    pub fn with_filters(mut self, filters: Vec<Filter>) -> Self {
        self.filters = filters;
        self
    }
    /// Creates an empty `WatchOptions`: watch from head, no checkpoints.
    pub fn new() -> Self {
        Self::default()
    }

    /// Resumes from `revision` rather than from head.
    pub fn with_start_revision(mut self, revision: ZedToken) -> Self {
        self.start_revision = Some(revision);
        self
    }

    /// Requests periodic checkpoint events in addition to relationship
    /// updates.
    pub fn with_checkpoints(mut self, checkpoints: CheckpointMode) -> Self {
        self.checkpoints = checkpoints;
        self
    }
}

/// A transaction builder for batching relationship writes.
///
/// Transactions take `&Relationship` (borrow) rather than moving the
/// relationship, so callers can reuse relationship values.
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct Transaction {
    updates: Vec<(UpdateOperation, Relationship)>,
    preconditions: Vec<Precondition>,
}

/// A precondition that must hold for a write transaction to succeed.
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct Precondition {
    pub operation: PreconditionOperation,
    pub filter: Filter,
}

/// The type of precondition check.
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
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

    /// Whether replaying the transaction is safe: it contains only touches
    /// and no preconditions. This is the rule the client uses to decide
    /// whether a write may be retried. An empty transaction without
    /// preconditions is idempotent.
    pub fn is_idempotent(&self) -> bool {
        self.preconditions.is_empty()
            && self
                .updates
                .iter()
                .all(|(op, _)| *op == UpdateOperation::Touch)
    }

    // TODO: When spicedb-proto types are available, add:
    //
    // pub(crate) fn to_proto_updates(&self) -> Vec<proto::RelationshipUpdate> { ... }
    // pub(crate) fn to_proto_preconditions(&self) -> Vec<proto::Precondition> { ... }
}

/// Schema reflection types returned by `reflect_schema`.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct SchemaDefinition {
    pub name: String,
    pub comment: String,
    pub relations: Vec<SchemaRelation>,
    pub permissions: Vec<SchemaPermission>,
}

/// A relation within a schema definition.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct SchemaRelation {
    pub subject_types: Vec<SchemaTypeReference>,
    pub name: String,
    pub comment: String,
    pub parent_definition_name: String,
}

/// A permission within a schema definition.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct SchemaPermission {
    pub name: String,
    pub comment: String,
    pub parent_definition_name: String,
}

/// A caveat defined in a SpiceDB schema.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct SchemaCaveat {
    pub name: String,
    pub comment: String,
    pub expression: String,
    pub parameters: Vec<SchemaCaveatParameter>,
}

/// A parameter of a caveat.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct SchemaCaveatParameter {
    pub name: String,
    pub type_name: String,
    pub parent_caveat_name: String,
}

/// The result of a schema reflection call.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct ReflectSchemaResult {
    pub definitions: Vec<SchemaDefinition>,
    pub caveats: Vec<SchemaCaveat>,
    pub revision: ZedToken,
}

/// Identifies a relation or permission on a definition (used by computable
/// permissions and dependent relations).
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct RelationReference {
    pub definition_name: String,
    pub relation_name: String,
    pub is_permission: bool,
}

/// A single difference between two schemas.
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ObjectRef {
    object_type: String,
    object_id: String,
}
impl ObjectRef {
    pub fn new(
        object_type: impl Into<String>,
        object_id: impl Into<String>,
    ) -> Result<Self, SpiceDBError> {
        let value = Self {
            object_type: object_type.into(),
            object_id: object_id.into(),
        };
        required("object_type", &value.object_type)?;
        required("object_id", &value.object_id)?;
        Ok(value)
    }
    pub fn object_type(&self) -> &str {
        &self.object_type
    }
    pub fn object_id(&self) -> &str {
        &self.object_id
    }
    pub(crate) fn to_proto(&self) -> proto::ObjectReference {
        proto::ObjectReference {
            object_type: self.object_type.clone(),
            object_id: self.object_id.clone(),
        }
    }
    pub(crate) fn from_proto(o: Option<&proto::ObjectReference>) -> Self {
        o.map(|o| Self {
            object_type: o.object_type.clone(),
            object_id: o.object_id.clone(),
        })
        .unwrap_or(Self {
            object_type: String::new(),
            object_id: String::new(),
        })
    }
}
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct SubjectRef {
    object: ObjectRef,
    relation: Option<String>,
}
impl SubjectRef {
    /// A subject: `object` itself when `relation` is `None` (or `"..."`,
    /// SpiceDB's name for a direct subject), otherwise the subject set
    /// `object#relation`. Fails only for an empty relation.
    pub fn new(object: ObjectRef, relation: Option<String>) -> Result<Self, SpiceDBError> {
        let relation = relation.filter(|r| r != "...");
        if let Some(r) = &relation {
            required("subject_relation", r)?;
        }
        Ok(Self { object, relation })
    }
    /// The direct subject `object`, with no relation. Infallible; also
    /// available as `SubjectRef::from(object)` and `object.into()`.
    pub fn direct(object: ObjectRef) -> Self {
        Self {
            object,
            relation: None,
        }
    }
    pub fn object(&self) -> &ObjectRef {
        &self.object
    }
    pub fn relation(&self) -> Option<&str> {
        self.relation.as_deref()
    }
    pub(crate) fn to_proto(&self) -> proto::SubjectReference {
        proto::SubjectReference {
            object: Some(self.object.to_proto()),
            optional_relation: self.relation.clone().unwrap_or_default(),
        }
    }
    pub(crate) fn from_proto(s: Option<&proto::SubjectReference>) -> Self {
        s.map(|s| Self {
            object: ObjectRef::from_proto(s.object.as_ref()),
            relation: if s.optional_relation.is_empty() || s.optional_relation == "..." {
                None
            } else {
                Some(s.optional_relation.clone())
            },
        })
        .unwrap_or(Self {
            object: ObjectRef::from_proto(None),
            relation: None,
        })
    }
}
impl From<ObjectRef> for SubjectRef {
    /// The direct subject `object` ([`SubjectRef::direct`]).
    fn from(object: ObjectRef) -> Self {
        Self::direct(object)
    }
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum TreeOperation {
    Unspecified,
    Union,
    Intersection,
    Exclusion,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum PermissionTree {
    Intermediate {
        expanded_object: ObjectRef,
        expanded_relation: String,
        operation: TreeOperation,
        children: Vec<PermissionTree>,
    },
    Leaf {
        expanded_object: ObjectRef,
        expanded_relation: String,
        subjects: Vec<SubjectRef>,
    },
    Unknown,
}
pub(crate) fn tree_operation_from_proto(v: i32) -> TreeOperation {
    match v {
        1 => TreeOperation::Union,
        2 => TreeOperation::Intersection,
        3 => TreeOperation::Exclusion,
        _ => TreeOperation::Unspecified,
    }
}
pub(crate) fn permission_tree_from_proto(
    t: Option<&proto::PermissionRelationshipTree>,
) -> PermissionTree {
    let Some(t) = t else {
        return PermissionTree::Unknown;
    };
    let expanded_object = ObjectRef::from_proto(t.expanded_object.as_ref());
    let expanded_relation = t.expanded_relation.clone();
    match &t.tree_type {
        Some(proto::permission_relationship_tree::TreeType::Intermediate(i)) => {
            PermissionTree::Intermediate {
                expanded_object,
                expanded_relation,
                operation: tree_operation_from_proto(i.operation),
                children: i
                    .children
                    .iter()
                    .map(|c| permission_tree_from_proto(Some(c)))
                    .collect(),
            }
        }
        Some(proto::permission_relationship_tree::TreeType::Leaf(l)) => PermissionTree::Leaf {
            expanded_object,
            expanded_relation,
            subjects: l
                .subjects
                .iter()
                .map(|s| SubjectRef::from_proto(Some(s)))
                .collect(),
        },
        None => PermissionTree::Unknown,
    }
}

/// The result of an expand permission tree call.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExpandPermissionTreeResult {
    /// The root of the expanded permission tree.
    pub tree: PermissionTree,
    /// The revision at which the tree was expanded.
    pub revision: ZedToken,
}

/// The result of a relationship count operation.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct CountResult {
    pub relationship_count: u64,
    pub revision: ZedToken,
}

/// The result of an experimental roaring lookup resources call.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalRoaringLookupResourcesResult {
    /// The roaring64 bitmap, in RoaringFormatSpec 64-bit portable format, of
    /// the resource object IDs accessible to the subject. The IDs are the
    /// object IDs from the relationships themselves, as 44-bit integers --
    /// this client does not decode the bitmap.
    pub bitmap: Vec<u8>,
    /// The number of resource IDs in `bitmap`.
    pub cardinality: u64,
    /// The ZedToken revision the lookup was performed at.
    pub at_revision: ZedToken,
}

/// Indicates whether a check or lookup result reflects a full grant, a full
/// denial, or is conditional on caveat context that was not fully evaluated
/// by the server. Callers MUST check this before treating a result as a full
/// grant — a `ConditionalPermission` result may resolve to `false` once the
/// missing caveat context is supplied.
///
/// This type serves both the check surface ([`CheckResult`]) and the lookup
/// surface ([`LookupResource`], [`ResolvedSubject`]). Lookups never yield
/// `NoPermission`: a subject/resource pair that lacks the permission is
/// simply absent from a lookup stream rather than being yielded with that
/// permissionship. `NoPermission` only appears on `CheckResult`, where the
/// server is answering a question about one specific pair and "no" is
/// itself an answer.
///
/// `NoPermission` is inserted directly after `Unspecified` rather than
/// appended at the end — this enum carries no explicit discriminants, so
/// there's no risk of renumbering the other variants by doing so.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum Permissionship {
    Unspecified,
    NoPermission,
    HasPermission,
    ConditionalPermission,
}

/// Equality compares the error, when present, by code and display text.
#[must_use]
#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct CheckResult {
    /// The server's answer for this check.
    pub permissionship: Permissionship,
    /// Caveat context keys the server needed and did not receive. Empty
    /// unless `permissionship` is `ConditionalPermission`.
    pub missing_context: Vec<String>,
    /// The revision (ZedToken) this check was evaluated at. Thread it into
    /// [`crate::consistency::at_least`] so a later read observes this check.
    pub checked_at: ZedToken,
    pub debug_trace: Option<DebugInformation>,
    pub expiration: Option<DateTime<Utc>>,
    /// Set only by `check_bulk` with `BulkItemErrors::Returned`: this item's
    /// failure as [`SpiceDBError::BulkItem`] with its absolute input index.
    /// An errored item's permissionship is `Unspecified`, so it never grants.
    pub error: Option<SpiceDBError>,
}

impl PartialEq for CheckResult {
    fn eq(&self, other: &Self) -> bool {
        let error_key = |e: &Option<SpiceDBError>| e.as_ref().map(|e| (e.code(), e.to_string()));
        self.permissionship == other.permissionship
            && self.missing_context == other.missing_context
            && self.checked_at == other.checked_at
            && self.debug_trace == other.debug_trace
            && self.expiration == other.expiration
            && error_key(&self.error) == error_key(&other.error)
    }
}
impl Eq for CheckResult {}

impl CheckResult {
    /// Returns `true` only when the permission was granted outright.
    ///
    /// This is `false` for a `ConditionalPermission` result: the server
    /// could not evaluate the caveat, so treating it as a grant would
    /// authorize on an unevaluated condition.
    pub fn has_permission(&self) -> bool {
        self.error.is_none() && self.permissionship == Permissionship::HasPermission
    }
}

/// Caveat context that was missing to fully evaluate a conditional result.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct PartialCaveatInfo {
    pub missing_required_context: Vec<String>,
}

/// One result from `lookup_resources`.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct LookupResource {
    pub resource_id: String,
    pub permissionship: Permissionship,
    /// Non-`None` when `permissionship` is `ConditionalPermission`.
    pub partial_caveat: Option<PartialCaveatInfo>,
    /// The revision (ZedToken) this result was computed at. Every page is
    /// pinned to the first page's revision by its cursor.
    pub looked_up_at: ZedToken,
    /// Resumes the lookup after this item: repeat the request and page size
    /// with `LookupResourcesOptions::with_cursor`.
    pub after_result_cursor: Option<String>,
}

/// One relationship from `read_relationships`, with its paging state.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ReadRelationship {
    pub relationship: Relationship,
    /// Resumes the read after this item: repeat the request and page size
    /// with `ReadRelationshipsOptions::with_cursor`. A pagination cursor, not
    /// a revision.
    pub after_result_cursor: Option<String>,
    /// The revision this item's response was read at.
    pub read_at: ZedToken,
}

impl ReadRelationship {
    pub fn into_relationship(self) -> Relationship {
        self.relationship
    }
}

/// One relationship from `export_relationships`, with its paging state.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExportedRelationship {
    pub relationship: Relationship,
    /// Resumes the export after this item's response (every item of one
    /// response shares it): repeat the request and page size with
    /// `ExportRelationshipsOptions::with_cursor`.
    pub after_result_cursor: Option<String>,
}

impl ExportedRelationship {
    pub fn into_relationship(self) -> Relationship {
        self.relationship
    }
}

/// A subject resolved by `lookup_subjects` — either the matched subject, or
/// (when found in [`LookupSubject::excluded_subjects`]) a subject excluded
/// from a wildcard match.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
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
#[non_exhaustive]
pub struct LookupSubject {
    pub subject: ResolvedSubject,
    pub excluded_subjects: Vec<ResolvedSubject>,
    /// The revision (ZedToken) this result was computed at. Identical for
    /// every item yielded by a single `lookup_subjects` call — it's a
    /// property of the call, not of the individual subject.
    pub looked_up_at: ZedToken,
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

/// Maps the proto `CheckPermissionResponse.Permissionship` enum (used by
/// both `CheckPermissionResponse` and `CheckBulkPermissionsResponseItem`,
/// represented as `i32` by prost) to its native equivalent. Unlike
/// [`permissionship_from_proto`] (used for lookups), this proto enum has a
/// `NoPermission` value. Unrecognized values map to
/// `Permissionship::Unspecified`.
pub(crate) fn check_permissionship_from_proto(v: i32) -> Permissionship {
    match v {
        x if x == proto::check_permission_response::Permissionship::NoPermission as i32 => {
            Permissionship::NoPermission
        }
        x if x == proto::check_permission_response::Permissionship::HasPermission as i32 => {
            Permissionship::HasPermission
        }
        x if x
            == proto::check_permission_response::Permissionship::ConditionalPermission as i32 =>
        {
            Permissionship::ConditionalPermission
        }
        _ => Permissionship::Unspecified,
    }
}

/// Maps a proto `CheckBulkPermissionsResponseItem` (one pair's successful
/// result from a `CheckBulkPermissions` call) to a native [`CheckResult`].
/// `CheckBulkPermissionsResponseItem` has no per-item `checked_at` of its
/// own — the token lives once on the enclosing `CheckBulkPermissionsResponse`
/// and applies to every pair in it, so callers pass it in as `checked_at` to
/// propagate onto each item.
pub(crate) fn check_result_from_bulk_item(
    item: &proto::CheckBulkPermissionsResponseItem,
    checked_at: &str,
) -> CheckResult {
    CheckResult {
        permissionship: check_permissionship_from_proto(item.permissionship),
        missing_context: item
            .partial_caveat_info
            .as_ref()
            .map(|c| c.missing_required_context.clone())
            .unwrap_or_default(),
        checked_at: checked_at.into(),
        debug_trace: item.debug_trace.as_ref().map(DebugInformation::from_proto),
        expiration: None,
        error: None,
    }
}

/// Merges call-level and per-item check-time caveat context per the
/// key-level merge rule (spec D3b): item keys win on conflict, and
/// call-level keys the item doesn't specify are retained. Returns `None`
/// only when neither is supplied — callers must not attach a `context`
/// field to the wire in that case (an empty `Struct` is not the same as no
/// context at all).
///
/// This is distinct from wholesale replacement: an item supplying one key
/// must not drop every call-level key it didn't mention, or the caveat
/// would fail for missing context that the caller thought it had already
/// supplied at the call level.
pub(crate) fn merge_check_context(
    call_level: Option<&HashMap<String, serde_json::Value>>,
    item: Option<&HashMap<String, serde_json::Value>>,
) -> Option<HashMap<String, serde_json::Value>> {
    if call_level.is_none() && item.is_none() {
        return None;
    }
    let mut merged = call_level.cloned().unwrap_or_default();
    if let Some(item) = item {
        for (k, v) in item {
            merged.insert(k.clone(), v.clone());
        }
    }
    Some(merged)
}

/// Converts a check-time caveat context map to the wire `Struct` type used
/// by `CheckBulkPermissionsRequestItem.context` (proto field 4) /
/// `CheckPermissionRequest.context` (proto field 5). Mirrors the write-time
/// conversion inlined in [`Relationship::to_proto`], but check-time context
/// is a different wire field on a different message — never
/// `Relationship.optional_caveat.context` — so it goes through its own
/// conversion rather than sharing `Relationship::to_proto`'s.
pub(crate) fn check_context_to_proto(
    ctx: &HashMap<String, serde_json::Value>,
) -> prost_types::Struct {
    prost_types::Struct {
        fields: ctx
            .iter()
            .map(|(k, v)| (k.clone(), json_value_to_prost(v)))
            .collect(),
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
        // Protobuf numbers are doubles. An integral value within the exactly
        // representable range comes back as a JSON integer, as protojson and
        // SpiceDB's tuple formatter print it (`5`, not `5.0`).
        Some(Kind::NumberValue(n)) if n.fract() == 0.0 && n.abs() <= 9_007_199_254_740_992.0 => {
            serde_json::Value::Number((*n as i64).into())
        }
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

/// Permission check tracing, disabled by default.
/// ```compile_fail
/// spicedb::CheckOptions::new().with_tracing(true);
/// ```
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum TracingMode {
    #[default]
    Disabled,
    Enabled,
}

/// Resource lookup debugging.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum DebugMode {
    #[default]
    Disabled,
    Enabled,
}

/// Periodic watch checkpoint delivery.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum CheckpointMode {
    #[default]
    Exclude,
    Include,
}

/// Kinds of changes delivered by a watch.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum WatchContent {
    #[default]
    Relationships,
    RelationshipsAndSchema,
}

/// Whether plaintext transport may send credentials to remote hosts.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum RemoteCredentialsPolicy {
    #[default]
    LoopbackOnly,
    /// Permit sending credentials without TLS to remote hostnames and IPs.
    AllowInsecureRemote,
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct CheckOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) tracing: TracingMode,
    /// Call-level caveat context, applied to every relationship the call
    /// evaluates and merged key-by-key with each relationship's own context,
    /// where the relationship's own value wins for keys present in both.
    pub(crate) context: Option<std::collections::HashMap<String, serde_json::Value>>,
    /// Deadline for this call, overriding the client's default. `None` applies
    /// that default rather than removing the bound — see root DESIGN.md,
    /// "RULE: A unary call must have a deadline".
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) item_errors: crate::options::BulkItemErrors,
}

impl CheckOptions {
    /// How `check_bulk` reports per-item failures: fail the call on the first
    /// (default) or return every item with errors on [`CheckResult::error`].
    /// Singular `check` has no items and ignores it.
    pub fn with_bulk_item_errors(mut self, mode: crate::options::BulkItemErrors) -> Self {
        self.item_errors = mode;
        self
    }
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn with_tracing(mut self, tracing: TracingMode) -> Self {
        self.tracing = tracing;
        self
    }
    /// Options with nothing set: the client's defaults apply.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the call-level caveat context.
    pub fn with_context(
        mut self,
        context: std::collections::HashMap<String, serde_json::Value>,
    ) -> Self {
        self.context = Some(context);
        self
    }

    /// Sets a per-call deadline, overriding the client's default.
    pub fn with_timeout(mut self, timeout: std::time::Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }
}

/// Call-level options for the lookup operations.
///
/// See [`CheckOptions`] for why this type exists and why it is
/// `#[non_exhaustive]`.
/// Native diagnostic trace; this never participates in authorization decisions.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DebugInformation {
    pub check: Option<CheckDebugTrace>,
    pub schema_used: String,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct CheckDebugTrace {
    pub resource: Option<ObjectRef>,
    pub permission: String,
    pub permission_type: PermissionType,
    pub subject: Option<SubjectRef>,
    pub result: Permissionship,
    pub caveat_evaluation: Option<CaveatEvaluation>,
    pub duration: Option<chrono::TimeDelta>,
    pub resolution: Option<TraceResolution>,
    pub expiration: Option<DateTime<Utc>>,
    pub operation_id: String,
    pub source: String,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum PermissionType {
    Unspecified,
    Relation,
    Permission,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum TraceResolution {
    Cached(bool),
    Subproblems(Vec<CheckDebugTrace>),
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum CaveatResult {
    Unspecified,
    Unevaluated,
    False,
    True,
    MissingContext,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct CaveatEvaluation {
    pub expression: String,
    pub result: CaveatResult,
    pub context: Option<Context>,
    pub missing_context: Vec<String>,
    pub name: String,
}
impl DebugInformation {
    pub(crate) fn from_proto(p: &proto::DebugInformation) -> Self {
        Self {
            check: p.check.as_ref().map(CheckDebugTrace::from_proto),
            schema_used: p.schema_used.clone(),
        }
    }
}
impl CheckDebugTrace {
    fn from_proto(p: &proto::CheckDebugTrace) -> Self {
        Self {
            resource: p.resource.as_ref().map(|r| ObjectRef::from_proto(Some(r))),
            permission: p.permission.clone(),
            permission_type: match p.permission_type {
                1 => PermissionType::Relation,
                2 => PermissionType::Permission,
                _ => PermissionType::Unspecified,
            },
            subject: p.subject.as_ref().map(|s| SubjectRef::from_proto(Some(s))),
            result: check_permissionship_from_proto(p.result),
            caveat_evaluation: p.caveat_evaluation_info.as_ref().map(|c| CaveatEvaluation {
                expression: c.expression.clone(),
                result: match c.result {
                    1 => CaveatResult::Unevaluated,
                    2 => CaveatResult::False,
                    3 => CaveatResult::True,
                    4 => CaveatResult::MissingContext,
                    _ => CaveatResult::Unspecified,
                },
                context: c.context.as_ref().map(context_from_proto),
                missing_context: c
                    .partial_caveat_info
                    .as_ref()
                    .map(|i| i.missing_required_context.clone())
                    .unwrap_or_default(),
                name: c.caveat_name.clone(),
            }),
            duration: p.duration.as_ref().and_then(|d| {
                chrono::TimeDelta::try_seconds(d.seconds)
                    .and_then(|s| s.checked_add(&chrono::TimeDelta::nanoseconds(d.nanos as i64)))
            }),
            resolution: p.resolution.as_ref().map(|r| match r {
                proto::check_debug_trace::Resolution::WasCachedResult(c) => {
                    TraceResolution::Cached(*c)
                }
                proto::check_debug_trace::Resolution::SubProblems(s) => {
                    TraceResolution::Subproblems(s.traces.iter().map(Self::from_proto).collect())
                }
            }),
            expiration: p
                .optional_expires_at
                .as_ref()
                .and_then(timestamp_from_proto),
            operation_id: p.trace_operation_id.clone(),
            source: p.source.clone(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct PermissionCheck {
    pub(crate) resource: ObjectRef,
    pub(crate) permission: String,
    pub(crate) subject: SubjectRef,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) context: Option<Context>,
    pub(crate) options: Option<CheckOptions>,
}
impl PermissionCheck {
    pub fn new(
        resource: ObjectRef,
        permission: impl Into<String>,
        subject: SubjectRef,
        consistency: crate::consistency::Strategy,
    ) -> Self {
        Self {
            resource,
            permission: permission.into(),
            subject,
            consistency,
            context: None,
            options: None,
        }
    }
    pub fn with_context(mut self, context: Context) -> Self {
        self.context = Some(context);
        self
    }
    pub fn with_options(mut self, options: CheckOptions) -> Self {
        self.options = Some(options);
        self
    }
    pub fn context(&self) -> Option<&Context> {
        self.context.as_ref()
    }
    pub fn resource(&self) -> &ObjectRef {
        &self.resource
    }
    pub fn permission(&self) -> &str {
        &self.permission
    }
    pub fn subject(&self) -> &SubjectRef {
        &self.subject
    }
    pub fn consistency(&self) -> &crate::consistency::Strategy {
        &self.consistency
    }
    pub(crate) fn validate(&self) -> Result<(), SpiceDBError> {
        required("permission", &self.permission)?;
        self.consistency.validate()?;
        if let Some(c) = &self.context {
            validate_context(c)?;
        }
        Ok(())
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct BulkCheckRequest {
    pub(crate) items: Vec<PermissionCheck>,
    pub(crate) options: CheckOptions,
}
impl BulkCheckRequest {
    pub fn new(items: Vec<PermissionCheck>) -> Self {
        Self {
            items,
            options: CheckOptions::new(),
        }
    }
    pub fn with_options(mut self, options: CheckOptions) -> Self {
        self.options = options;
        self
    }
    pub fn items(&self) -> &[PermissionCheck] {
        &self.items
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct WriteOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) transaction_metadata: Option<Context>,
}
impl WriteOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_transaction_metadata(mut self, value: Context) -> Self {
        self.transaction_metadata = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct WriteRequest {
    pub(crate) transaction: Transaction,
    pub(crate) options: WriteOptions,
}
impl WriteRequest {
    pub fn new(transaction: Transaction) -> Self {
        Self {
            transaction,
            options: WriteOptions::new(),
        }
    }
    pub fn with_options(mut self, options: WriteOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ReadRelationshipsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) page_size: std::num::NonZeroU32,
    pub(crate) limit: Option<u32>,
    pub(crate) cursor: Option<String>,
    pub(crate) paging: crate::options::Paging,
}
impl Default for ReadRelationshipsOptions {
    fn default() -> Self {
        Self {
            call: Default::default(),
            timeout: None,
            page_size: std::num::NonZeroU32::new(512).unwrap(),
            limit: None,
            cursor: None,
            paging: crate::options::Paging::Automatic,
        }
    }
}
impl ReadRelationshipsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_page_size(mut self, value: std::num::NonZeroU32) -> Self {
        self.page_size = value;
        self
    }
    pub fn with_limit(mut self, value: u32) -> Self {
        self.limit = Some(value);
        self
    }
    /// [`Paging::SingleRequest`](crate::Paging::SingleRequest) sends exactly
    /// one request, as the raw API does: its limit is
    /// [`with_limit`](Self::with_limit) or unbounded, the page size is ignored,
    /// and no follow-up page is fetched. The default pages automatically.
    pub fn with_paging(mut self, mode: crate::options::Paging) -> Self {
        self.paging = mode;
        self
    }
    pub fn with_cursor(mut self, value: impl Into<String>) -> Self {
        self.cursor = Some(value.into());
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ReadRelationshipsRequest {
    pub(crate) filter: Filter,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: ReadRelationshipsOptions,
}
impl ReadRelationshipsRequest {
    pub fn new(filter: Filter, consistency: crate::consistency::Strategy) -> Self {
        Self {
            filter,
            consistency,
            options: ReadRelationshipsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ReadRelationshipsOptions) -> Self {
        self.options = options;
        self
    }
}
/// Client-side selection of LookupResources results. Limits count yielded results.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum LookupPermissionshipFilter {
    /// Preserve all results, including conditional and unknown permissionships.
    #[default]
    All,
    /// Yield only unconditional grants.
    HasPermissionOnly,
}

#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct LookupResourcesOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) permissionship_filter: LookupPermissionshipFilter,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) page_size: std::num::NonZeroU32,
    pub(crate) limit: Option<u32>,
    pub(crate) cursor: Option<String>,
    pub(crate) paging: crate::options::Paging,
    pub(crate) context: Option<Context>,
    pub(crate) debug: DebugMode,
}
impl Default for LookupResourcesOptions {
    fn default() -> Self {
        Self {
            call: Default::default(),
            permissionship_filter: LookupPermissionshipFilter::All,
            timeout: None,
            page_size: std::num::NonZeroU32::new(512).unwrap(),
            limit: None,
            cursor: None,
            paging: crate::options::Paging::Automatic,
            context: None,
            debug: DebugMode::Disabled,
        }
    }
}
impl LookupResourcesOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    /// Filter results client-side; pagination continues past skipped results.
    pub fn with_permissionship_filter(mut self, value: LookupPermissionshipFilter) -> Self {
        self.permissionship_filter = value;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_page_size(mut self, value: std::num::NonZeroU32) -> Self {
        self.page_size = value;
        self
    }
    pub fn with_limit(mut self, value: u32) -> Self {
        self.limit = Some(value);
        self
    }
    /// [`Paging::SingleRequest`](crate::Paging::SingleRequest) sends exactly
    /// one request, as the raw API does: its limit is
    /// [`with_limit`](Self::with_limit) or unbounded, the page size is ignored,
    /// and no follow-up page is fetched. The default pages automatically.
    pub fn with_paging(mut self, mode: crate::options::Paging) -> Self {
        self.paging = mode;
        self
    }
    pub fn with_cursor(mut self, value: impl Into<String>) -> Self {
        self.cursor = Some(value.into());
        self
    }
    pub fn with_context(mut self, value: Context) -> Self {
        self.context = Some(value);
        self
    }
    pub fn with_debug(mut self, value: DebugMode) -> Self {
        self.debug = value;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct LookupResourcesRequest {
    pub(crate) resource_type: String,
    pub(crate) permission: String,
    pub(crate) subject: SubjectRef,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: LookupResourcesOptions,
}
impl LookupResourcesRequest {
    pub fn new(
        resource_type: impl Into<String>,
        permission: impl Into<String>,
        subject: SubjectRef,
        consistency: crate::consistency::Strategy,
    ) -> Self {
        Self {
            resource_type: resource_type.into(),
            permission: permission.into(),
            subject,
            consistency,
            options: LookupResourcesOptions::new(),
        }
    }
    pub fn with_options(mut self, options: LookupResourcesOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct LookupSubjectsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) context: Option<Context>,
    pub(crate) subject_relation: Option<String>,
    pub(crate) limit: Option<u32>,
    pub(crate) wildcard_option: WildcardOption,
}
impl Default for LookupSubjectsOptions {
    fn default() -> Self {
        Self {
            call: Default::default(),
            timeout: None,
            context: None,
            subject_relation: None,
            limit: None,
            wildcard_option: WildcardOption::Include,
        }
    }
}
impl LookupSubjectsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_context(mut self, value: Context) -> Self {
        self.context = Some(value);
        self
    }
    /// Looks up subject sets `type:id#relation`. `"..."` means direct
    /// subjects, the default.
    pub fn with_subject_relation(mut self, value: impl Into<String>) -> Self {
        let value = value.into();
        self.subject_relation = (value != "...").then_some(value);
        self
    }
    pub fn with_limit(mut self, value: u32) -> Self {
        self.limit = Some(value);
        self
    }
    pub fn with_wildcard_option(mut self, value: WildcardOption) -> Self {
        self.wildcard_option = value;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct LookupSubjectsRequest {
    pub(crate) resource: ObjectRef,
    pub(crate) permission: String,
    pub(crate) subject_type: String,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: LookupSubjectsOptions,
}
impl LookupSubjectsRequest {
    pub fn new(
        resource: ObjectRef,
        permission: impl Into<String>,
        subject_type: impl Into<String>,
        consistency: crate::consistency::Strategy,
    ) -> Self {
        Self {
            resource,
            permission: permission.into(),
            subject_type: subject_type.into(),
            consistency,
            options: LookupSubjectsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: LookupSubjectsOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ReadSchemaOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl ReadSchemaOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ReadSchemaRequest {
    pub(crate) options: ReadSchemaOptions,
}
impl ReadSchemaRequest {
    pub fn new() -> Self {
        Self {
            options: ReadSchemaOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ReadSchemaOptions) -> Self {
        self.options = options;
        self
    }
}
impl Default for ReadSchemaRequest {
    fn default() -> Self {
        Self::new()
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct WriteSchemaOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl WriteSchemaOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct WriteSchemaRequest {
    pub(crate) schema: String,
    pub(crate) options: WriteSchemaOptions,
}
impl WriteSchemaRequest {
    pub fn new(schema: impl Into<String>) -> Self {
        Self {
            schema: schema.into(),
            options: WriteSchemaOptions::new(),
        }
    }
    pub fn with_options(mut self, options: WriteSchemaOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ReflectSchemaOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) filters: Vec<ReflectionFilter>,
}
impl ReflectSchemaOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_filters(mut self, value: Vec<ReflectionFilter>) -> Self {
        self.filters = value;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ReflectSchemaRequest {
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: ReflectSchemaOptions,
}
impl ReflectSchemaRequest {
    pub fn new(consistency: crate::consistency::Strategy) -> Self {
        Self {
            consistency,
            options: ReflectSchemaOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ReflectSchemaOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ComputablePermissionsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) definition_name_filter: Option<String>,
}
impl ComputablePermissionsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_definition_name_filter(mut self, value: impl Into<String>) -> Self {
        self.definition_name_filter = Some(value.into());
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ComputablePermissionsRequest {
    pub(crate) definition_name: String,
    pub(crate) relation_name: String,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: ComputablePermissionsOptions,
}
impl ComputablePermissionsRequest {
    pub fn new(
        definition_name: impl Into<String>,
        relation_name: impl Into<String>,
        consistency: crate::consistency::Strategy,
    ) -> Self {
        Self {
            definition_name: definition_name.into(),
            relation_name: relation_name.into(),
            consistency,
            options: ComputablePermissionsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ComputablePermissionsOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct DependentRelationsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl DependentRelationsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DependentRelationsRequest {
    pub(crate) definition_name: String,
    pub(crate) permission_name: String,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: DependentRelationsOptions,
}
impl DependentRelationsRequest {
    pub fn new(
        definition_name: impl Into<String>,
        permission_name: impl Into<String>,
        consistency: crate::consistency::Strategy,
    ) -> Self {
        Self {
            definition_name: definition_name.into(),
            permission_name: permission_name.into(),
            consistency,
            options: DependentRelationsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: DependentRelationsOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct DiffSchemaOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl DiffSchemaOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DiffSchemaRequest {
    pub(crate) comparison_schema: String,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: DiffSchemaOptions,
}
impl DiffSchemaRequest {
    pub fn new(
        comparison_schema: impl Into<String>,
        consistency: crate::consistency::Strategy,
    ) -> Self {
        Self {
            comparison_schema: comparison_schema.into(),
            consistency,
            options: DiffSchemaOptions::new(),
        }
    }
    pub fn with_options(mut self, options: DiffSchemaOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ExpandPermissionTreeOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl ExpandPermissionTreeOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExpandPermissionTreeRequest {
    pub(crate) resource: ObjectRef,
    pub(crate) permission: String,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: ExpandPermissionTreeOptions,
}
impl ExpandPermissionTreeRequest {
    pub fn new(
        resource: ObjectRef,
        permission: impl Into<String>,
        consistency: crate::consistency::Strategy,
    ) -> Self {
        Self {
            resource,
            permission: permission.into(),
            consistency,
            options: ExpandPermissionTreeOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ExpandPermissionTreeOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExportRelationshipsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) page_size: std::num::NonZeroU32,
    pub(crate) filter: Option<Filter>,
    pub(crate) limit: Option<u32>,
    pub(crate) cursor: Option<String>,
}
impl Default for ExportRelationshipsOptions {
    fn default() -> Self {
        Self {
            call: Default::default(),
            timeout: None,
            page_size: std::num::NonZeroU32::new(512).unwrap(),
            filter: None,
            limit: None,
            cursor: None,
        }
    }
}
impl ExportRelationshipsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_page_size(mut self, value: std::num::NonZeroU32) -> Self {
        self.page_size = value;
        self
    }
    pub fn with_filter(mut self, value: Filter) -> Self {
        self.filter = Some(value);
        self
    }
    pub fn with_limit(mut self, value: u32) -> Self {
        self.limit = Some(value);
        self
    }
    pub fn with_cursor(mut self, value: impl Into<String>) -> Self {
        self.cursor = Some(value.into());
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExportRelationshipsRequest {
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: ExportRelationshipsOptions,
}
impl ExportRelationshipsRequest {
    pub fn new(consistency: crate::consistency::Strategy) -> Self {
        Self {
            consistency,
            options: ExportRelationshipsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ExportRelationshipsOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct UpdatesRequest {
    pub(crate) object_types: Vec<String>,
    pub(crate) options: WatchOptions,
}
impl UpdatesRequest {
    pub fn new(object_types: Vec<String>) -> Self {
        Self {
            object_types,
            options: WatchOptions::new(),
        }
    }
    pub fn with_options(mut self, options: WatchOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ExperimentalRegisterRelationshipCounterOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl ExperimentalRegisterRelationshipCounterOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalRegisterRelationshipCounterRequest {
    pub(crate) name: String,
    pub(crate) filter: Filter,
    pub(crate) options: ExperimentalRegisterRelationshipCounterOptions,
}
impl ExperimentalRegisterRelationshipCounterRequest {
    pub fn new(name: impl Into<String>, filter: Filter) -> Self {
        Self {
            name: name.into(),
            filter,
            options: ExperimentalRegisterRelationshipCounterOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ExperimentalRegisterRelationshipCounterOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ExperimentalCountRelationshipsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl ExperimentalCountRelationshipsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalCountRelationshipsRequest {
    pub(crate) name: String,
    pub(crate) options: ExperimentalCountRelationshipsOptions,
}
impl ExperimentalCountRelationshipsRequest {
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            options: ExperimentalCountRelationshipsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ExperimentalCountRelationshipsOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ExperimentalUnregisterRelationshipCounterOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl ExperimentalUnregisterRelationshipCounterOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalUnregisterRelationshipCounterRequest {
    pub(crate) name: String,
    pub(crate) options: ExperimentalUnregisterRelationshipCounterOptions,
}
impl ExperimentalUnregisterRelationshipCounterRequest {
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            options: ExperimentalUnregisterRelationshipCounterOptions::new(),
        }
    }
    pub fn with_options(
        mut self,
        options: ExperimentalUnregisterRelationshipCounterOptions,
    ) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ExperimentalRoaringLookupResourcesOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl ExperimentalRoaringLookupResourcesOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalRoaringLookupResourcesRequest {
    pub(crate) resource_type: String,
    pub(crate) permission: String,
    pub(crate) subject: SubjectRef,
    pub(crate) consistency: crate::consistency::Strategy,
    pub(crate) options: ExperimentalRoaringLookupResourcesOptions,
}
impl ExperimentalRoaringLookupResourcesRequest {
    pub fn new(
        resource_type: impl Into<String>,
        permission: impl Into<String>,
        subject: SubjectRef,
        consistency: crate::consistency::Strategy,
    ) -> Self {
        Self {
            resource_type: resource_type.into(),
            permission: permission.into(),
            subject,
            consistency,
            options: ExperimentalRoaringLookupResourcesOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ExperimentalRoaringLookupResourcesOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct DeleteOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) preconditions: Vec<Precondition>,
    pub(crate) cursor: Option<String>,
    pub(crate) transaction_metadata: Option<Context>,
    pub(crate) partial: crate::options::DeletePartial,
}
impl DeleteOptions {
    /// `DeletePartial::Disallowed` makes the delete all-or-nothing: the server
    /// rejects a delete exceeding the limit instead of deleting part of it.
    pub fn with_partial(mut self, mode: crate::options::DeletePartial) -> Self {
        self.partial = mode;
        self
    }
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_preconditions(mut self, value: Vec<Precondition>) -> Self {
        self.preconditions = value;
        self
    }
    pub fn with_cursor(mut self, value: impl Into<String>) -> Self {
        self.cursor = Some(value.into());
        self
    }
    pub fn with_transaction_metadata(mut self, value: Context) -> Self {
        self.transaction_metadata = Some(value);
        self
    }
    pub fn with_must_match(mut self, filter: Filter) -> Self {
        self.preconditions.push(Precondition {
            operation: PreconditionOperation::MustMatch,
            filter,
        });
        self
    }
    pub fn with_must_not_match(mut self, filter: Filter) -> Self {
        self.preconditions.push(Precondition {
            operation: PreconditionOperation::MustNotMatch,
            filter,
        });
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DeleteRequest {
    pub(crate) filter: Filter,
    /// `None` uses the server's configured default limit, which is only
    /// allowed with `DeletePartial::Disallowed`.
    pub(crate) limit: Option<std::num::NonZeroU32>,
    pub(crate) options: DeleteOptions,
}
impl DeleteRequest {
    /// One bounded delete of at most `limit` relationships.
    pub fn new(filter: Filter, limit: std::num::NonZeroU32) -> Self {
        Self {
            filter,
            limit: Some(limit),
            options: DeleteOptions::new(),
        }
    }
    /// A delete bounded by the server's configured default limit. Only valid
    /// together with `DeleteOptions::with_partial(DeletePartial::Disallowed)`
    /// (all-or-nothing); otherwise the call fails with `InvalidArgument`
    /// before any RPC.
    pub fn server_default_limit(filter: Filter) -> Self {
        Self {
            filter,
            limit: None,
            options: DeleteOptions::new(),
        }
    }
    pub fn with_options(mut self, options: DeleteOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DeleteAllOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) preconditions: Vec<Precondition>,
    pub(crate) cursor: Option<String>,
    pub(crate) transaction_metadata: Option<Context>,
    pub(crate) page_size: std::num::NonZeroU32,
    pub(crate) progress: Option<crate::options::Callback<DeleteResult>>,
}
impl Default for DeleteAllOptions {
    fn default() -> Self {
        Self {
            call: Default::default(),
            timeout: None,
            preconditions: Vec::new(),
            cursor: None,
            transaction_metadata: None,
            page_size: std::num::NonZeroU32::new(1000).unwrap(),
            progress: None,
        }
    }
}
impl DeleteAllOptions {
    /// Called after every acknowledged batch with the cumulative result so far
    /// (count, latest revision and cursor). It runs inline and should return
    /// quickly: the operation budget keeps running.
    pub fn with_progress(mut self, f: impl Fn(&DeleteResult) + Send + Sync + 'static) -> Self {
        self.progress = Some(crate::options::Callback::new(f));
        self
    }
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_preconditions(mut self, value: Vec<Precondition>) -> Self {
        self.preconditions = value;
        self
    }
    pub fn with_cursor(mut self, value: impl Into<String>) -> Self {
        self.cursor = Some(value.into());
        self
    }
    pub fn with_transaction_metadata(mut self, value: Context) -> Self {
        self.transaction_metadata = Some(value);
        self
    }
    pub fn with_page_size(mut self, value: std::num::NonZeroU32) -> Self {
        self.page_size = value;
        self
    }
    pub fn with_must_match(mut self, filter: Filter) -> Self {
        self.preconditions.push(Precondition {
            operation: PreconditionOperation::MustMatch,
            filter,
        });
        self
    }
    pub fn with_must_not_match(mut self, filter: Filter) -> Self {
        self.preconditions.push(Precondition {
            operation: PreconditionOperation::MustNotMatch,
            filter,
        });
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DeleteAllRequest {
    pub(crate) filter: Filter,
    pub(crate) options: DeleteAllOptions,
}
impl DeleteAllRequest {
    pub fn new(filter: Filter) -> Self {
        Self {
            filter,
            options: DeleteAllOptions::new(),
        }
    }
    pub fn with_options(mut self, options: DeleteAllOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ExperimentalCountRelationshipsByFilterOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
}
impl ExperimentalCountRelationshipsByFilterOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalCountRelationshipsByFilterRequest {
    pub(crate) filter: Filter,
    pub(crate) options: ExperimentalCountRelationshipsByFilterOptions,
}
impl ExperimentalCountRelationshipsByFilterRequest {
    pub fn new(filter: Filter) -> Self {
        Self {
            filter,
            options: ExperimentalCountRelationshipsByFilterOptions::new(),
        }
    }
    pub fn with_options(mut self, options: ExperimentalCountRelationshipsByFilterOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct WatchPermissionSetsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) start_revision: Option<ZedToken>,
}
impl WatchPermissionSetsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_start_revision(mut self, value: ZedToken) -> Self {
        self.start_revision = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct WatchPermissionSetsRequest {
    pub(crate) options: WatchPermissionSetsOptions,
}
impl WatchPermissionSetsRequest {
    pub fn new() -> Self {
        Self {
            options: WatchPermissionSetsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: WatchPermissionSetsOptions) -> Self {
        self.options = options;
        self
    }
}
impl Default for WatchPermissionSetsRequest {
    fn default() -> Self {
        Self::new()
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct LookupPermissionSetsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) at_revision: Option<ZedToken>,
    pub(crate) cursor: Option<PermissionSetCursor>,
}
impl LookupPermissionSetsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_at_revision(mut self, value: ZedToken) -> Self {
        self.at_revision = Some(value);
        self
    }
    pub fn with_cursor(mut self, value: PermissionSetCursor) -> Self {
        self.cursor = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct LookupPermissionSetsRequest {
    pub(crate) limit: std::num::NonZeroU32,
    pub(crate) options: LookupPermissionSetsOptions,
}
impl LookupPermissionSetsRequest {
    pub fn new(limit: std::num::NonZeroU32) -> Self {
        Self {
            limit,
            options: LookupPermissionSetsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: LookupPermissionSetsOptions) -> Self {
        self.options = options;
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct DownloadPermissionSetsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) at_revision: Option<ZedToken>,
}
impl DownloadPermissionSetsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, value: std::time::Duration) -> Self {
        self.timeout = Some(value);
        self
    }
    pub fn with_at_revision(mut self, value: ZedToken) -> Self {
        self.at_revision = Some(value);
        self
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DownloadPermissionSetsRequest {
    pub(crate) options: DownloadPermissionSetsOptions,
}
impl DownloadPermissionSetsRequest {
    pub fn new() -> Self {
        Self {
            options: DownloadPermissionSetsOptions::new(),
        }
    }
    pub fn with_options(mut self, options: DownloadPermissionSetsOptions) -> Self {
        self.options = options;
        self
    }
}
impl Default for DownloadPermissionSetsRequest {
    fn default() -> Self {
        Self::new()
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct WriteResult {
    pub revision: ZedToken,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct WriteSchemaResult {
    pub revision: ZedToken,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalRegisterRelationshipCounterResult {}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalUnregisterRelationshipCounterResult {}

#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ReadSchemaResult {
    pub schema_text: String,
    pub revision: ZedToken,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ComputablePermissionsResult {
    pub permissions: Vec<RelationReference>,
    pub revision: ZedToken,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DependentRelationsResult {
    pub relations: Vec<RelationReference>,
    pub revision: ZedToken,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DiffSchemaResult {
    pub diffs: Vec<SchemaDiff>,
    pub revision: ZedToken,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DeleteResult {
    pub revision: ZedToken,
    pub deleted_count: u64,
    pub complete: bool,
    pub cursor: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum WildcardOption {
    Include,
    Exclude,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ReflectionFilter {
    pub definition_name_filter: String,
    pub caveat_name_filter: String,
    pub relation_name_filter: String,
    pub permission_name_filter: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum ExperimentalCountRelationshipsResult {
    Count(CountResult),
    StillCalculating,
    Unknown,
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct ImportRelationshipsOptions {
    pub(crate) call: crate::options::CallOptions,
    pub(crate) timeout: Option<std::time::Duration>,
    pub(crate) batch_size: Option<std::num::NonZeroU32>,
}
impl ImportRelationshipsOptions {
    /// Per-call settings: request metadata, response header/trailer capture
    /// and a retry-policy override. See [`CallOptions`](crate::CallOptions).
    pub fn with_call_options(mut self, call: crate::options::CallOptions) -> Self {
        self.call = call;
        self
    }
    pub fn new() -> Self {
        Self::default()
    }
    pub fn with_timeout(mut self, timeout: std::time::Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }
    /// Relationships per stream message (default 1,000). This does not split
    /// the import: one call is one stream and one transaction, so callers
    /// wanting several transactions make several calls.
    pub fn with_batch_size(mut self, batch_size: std::num::NonZeroU32) -> Self {
        self.batch_size = Some(batch_size);
        self
    }
}
#[non_exhaustive]
pub struct ImportRelationshipsRequest {
    pub(crate) relationships:
        std::pin::Pin<Box<dyn futures::Stream<Item = Result<Relationship, SpiceDBError>> + Send>>,
    pub(crate) options: ImportRelationshipsOptions,
}
impl ImportRelationshipsRequest {
    pub fn new<S: futures::Stream<Item = Result<Relationship, SpiceDBError>> + Send + 'static>(
        relationships: S,
    ) -> Self {
        Self {
            relationships: Box::pin(relationships),
            options: ImportRelationshipsOptions::default(),
        }
    }
    pub fn with_options(mut self, options: ImportRelationshipsOptions) -> Self {
        self.options = options;
        self
    }
}
impl fmt::Debug for ImportRelationshipsRequest {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ImportRelationshipsRequest")
            .field("options", &self.options)
            .finish_non_exhaustive()
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ImportRelationshipsResult {
    pub imported_count: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct PermissionSetCursor {
    pub limit: u32,
    pub revision: Option<ZedToken>,
    pub starting_index: u32,
    pub completed_members: bool,
    pub starting_key: String,
    pub cursor: String,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct SetReference {
    pub object: ObjectRef,
    pub permission_or_relation: String,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct MemberReference {
    pub object: ObjectRef,
    pub permission_or_relation: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum SetOperation {
    Unspecified,
    Added,
    Removed,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum PermissionSetChild {
    Set(SetReference),
    Member(MemberReference),
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct PermissionSetChange {
    pub revision: ZedToken,
    pub operation: SetOperation,
    pub parent_set: Option<SetReference>,
    pub child: Option<PermissionSetChild>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct WatchedPermission {
    pub resource_type: String,
    pub permission: String,
    pub subject_type: String,
    pub subject_relation: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum PermissionSetEvent {
    Change(PermissionSetChange),
    CompletedRevision(ZedToken),
    LookupRequired {
        revision: ZedToken,
    },
    BreakingSchemaChange {
        revision: ZedToken,
        affected_permissions: Vec<WatchedPermission>,
    },
    Unknown,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct LookupPermissionSetsResult {
    pub change: Option<PermissionSetChange>,
    pub cursor: Option<PermissionSetCursor>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct PermissionSetFile {
    pub name: String,
    pub url: String,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct DownloadPermissionSetsResult {
    pub files: Vec<PermissionSetFile>,
    pub timestamp: Option<DateTime<Utc>>,
    pub revision: ZedToken,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct ExperimentalCountRelationshipsByFilterResult {
    pub relationship_count: u64,
    pub revision: ZedToken,
}

#[cfg(test)]
#[path = "result_tests.rs"]
mod result_tests;

#[derive(Debug, Clone, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct SchemaTypeReference {
    pub subject_definition_name: String,
    pub caveat_name: Option<String>,
    pub reference: Option<SchemaSubjectType>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum SchemaSubjectType {
    Terminal(bool),
    Relation(String),
    PublicWildcard(bool),
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct RelationSubjectTypeChange {
    pub relation: Option<SchemaRelation>,
    pub changed_subject_type: Option<SchemaTypeReference>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct CaveatParameterTypeChange {
    pub parameter: Option<SchemaCaveatParameter>,
    pub previous_type: String,
}
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum SchemaDiff {
    DefinitionAdded(SchemaDefinition),
    DefinitionRemoved(SchemaDefinition),
    DefinitionDocCommentChanged(SchemaDefinition),
    RelationAdded(SchemaRelation),
    RelationRemoved(SchemaRelation),
    RelationDocCommentChanged(SchemaRelation),
    RelationSubjectTypeAdded(RelationSubjectTypeChange),
    RelationSubjectTypeRemoved(RelationSubjectTypeChange),
    PermissionAdded(SchemaPermission),
    PermissionRemoved(SchemaPermission),
    PermissionDocCommentChanged(SchemaPermission),
    PermissionExprChanged(SchemaPermission),
    CaveatAdded(SchemaCaveat),
    CaveatRemoved(SchemaCaveat),
    CaveatDocCommentChanged(SchemaCaveat),
    CaveatExprChanged(SchemaCaveat),
    CaveatParameterAdded(SchemaCaveatParameter),
    CaveatParameterRemoved(SchemaCaveatParameter),
    CaveatParameterTypeChanged(CaveatParameterTypeChange),
    Unknown,
}

#[cfg(test)]
#[path = "types_tests.rs"]
mod conversion_tests;

impl ReflectionFilter {
    pub fn new() -> Self {
        Self {
            definition_name_filter: String::new(),
            caveat_name_filter: String::new(),
            relation_name_filter: String::new(),
            permission_name_filter: String::new(),
        }
    }
    pub fn with_definition_name(mut self, value: impl Into<String>) -> Self {
        self.definition_name_filter = value.into();
        self
    }
    pub fn with_caveat_name(mut self, value: impl Into<String>) -> Self {
        self.caveat_name_filter = value.into();
        self
    }
    pub fn with_relation_name(mut self, value: impl Into<String>) -> Self {
        self.relation_name_filter = value.into();
        self
    }
    pub fn with_permission_name(mut self, value: impl Into<String>) -> Self {
        self.permission_name_filter = value.into();
        self
    }
}
impl Default for ReflectionFilter {
    fn default() -> Self {
        Self::new()
    }
}
