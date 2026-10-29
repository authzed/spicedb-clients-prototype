//! An in-memory SpiceDB double for tests (feature `testing`).
//!
//! [`InMemorySpiceDB`] serves SpiceDB's `PermissionsService` over an
//! in-memory channel, and [`InMemorySpiceDB::client`] returns a real
//! [`SpiceDBClient`] connected to it through
//! [`SpiceDBClient::from_channel`]. The test double therefore has exactly the
//! real client's surface: code under test takes a `SpiceDBClient` and needs no
//! trait or adapter.
//!
//! It applies SpiceDB's relationship semantics — filter matching (via
//! [`Filter::matches`]), preconditions, `CREATE` conflicts and atomic
//! transactions (an empty write checks its preconditions and returns the
//! current revision), paged reads and exports with cursors, limits and the
//! export filter, all-or-nothing imports, bounded and all-or-nothing deletes —
//! and serves `SchemaService` reads and writes of the schema text. It
//! evaluates no schema. A permission check resolves to the result registered
//! for that exact question ([`InMemorySpiceDB::set_check_result`]), then to the
//! answer of a check resolver ([`InMemorySpiceDB::set_check_resolver`];
//! [`grant_all`] grants everything), then to the stored relationships (an
//! unexpired relationship on the resource whose relation is the permission's
//! name and whose subject matches exactly, or as a wildcard over a direct
//! subject, grants it; caveated ones only conditionally), and otherwise to
//! `NoPermission`. `lookup_resources` and `lookup_subjects` return the objects
//! of the requested type in the stored relationships that check resolution
//! grants, with conditional results reported as conditional.
//! `reflect_schema` returns the result registered with
//! [`InMemorySpiceDB::set_reflect_schema_result`]. `delete_all_relationships_non_atomic`
//! and `check_bulk` work through the RPCs it models.
//!
//! It serves every service the client calls (permissions, schema, watch,
//! experimental and Materialize), so every [`SpiceDBClient`] method reaches
//! it: RPCs it does not model (expand, watch, schema introspection, counters,
//! Materialize) are recorded, consult the fault hook, and otherwise return
//! `UNIMPLEMENTED`, which the client maps to
//! [`SpiceDBError`] as it maps SpiceDB's own status. A test that calls every
//! client method keeps the two in step, so no separate trait is needed: the
//! double's surface is the client.
//!
//! Read and export cursors resume after a count of matching relationships;
//! [`cursor_after`] builds one. [`InMemorySpiceDB::set_read_schema_revision`]
//! emulates servers whose schema reads report no revision.
//!
//! Because the double is a server behind a real client, every option is
//! honored exactly as the client sends it (cursors, limits, page sizes,
//! filters, per-call retry policies, request metadata), and errors have the
//! real client's codes and shape: server failures are mapped by the client
//! with [`SpiceDBError::server_message`] set, and local validation fails in
//! the client before any request, as it does against SpiceDB. Headers
//! registered with [`InMemorySpiceDB::set_response_metadata`] are sent on
//! every successful response, so [`ResponseMetadata`](crate::ResponseMetadata)
//! captures them.
//!
//! Every RPC the double receives is recorded ([`InMemorySpiceDB::calls`]) with
//! its operation, per-operation index, protocol request and request metadata:
//! options are recorded as the client sent them (cursors, limits, page sizes,
//! filters, consistency in the request; [`CallOptions`](crate::CallOptions)
//! metadata and the remaining deadline as `grpc-timeout` in the metadata).
//! Because the client's retries and pages are separate RPCs, they are recorded
//! separately. Faults can be injected ([`InMemorySpiceDB::set_fault_hook`],
//! [`InMemorySpiceDB::fail_next`]) before any RPC, before each import batch,
//! before an import commits and before each read, export or lookup item; the client
//! maps an injected [`server_error`] exactly as it maps SpiceDB's, so failure
//! paths need no hand-written gRPC server.
//!
//! ```
//! # #[tokio::main(flavor = "current_thread")]
//! # async fn main() -> Result<(), spicedb::SpiceDBError> {
//! use spicedb::testing::InMemorySpiceDB;
//! use spicedb::{consistency, PermissionCheck, Permissionship, Relationship, Transaction, WriteRequest};
//!
//! let fake = InMemorySpiceDB::new();
//! let client = fake.client()?;
//! let viewer: Relationship = "document:readme#viewer@user:alice".parse()?;
//! let mut txn = Transaction::new();
//! txn.create(&viewer);
//! client.write(WriteRequest::new(txn)).await?;
//! assert_eq!(fake.relationships(), vec![viewer.clone()]);
//!
//! // The stored relationship grants `viewer` itself; other permissions need a
//! // registered result or a resolver.
//! let check = PermissionCheck::new(viewer.resource().clone(), "viewer", viewer.subject().clone(), consistency::full());
//! assert!(client.check(check).await?.has_permission());
//! fake.set_check_result(viewer.resource(), "view", viewer.subject(), Permissionship::HasPermission);
//! let check = PermissionCheck::new(viewer.resource().clone(), "view", viewer.subject().clone(), consistency::full());
//! assert!(client.check(check).await?.has_permission());
//! # Ok(()) }
//! ```

// tonic services return `Status` errors by design.
#![allow(clippy::result_large_err)]

use std::any::Any;
use std::collections::HashMap;
use std::fmt::Debug;
use std::pin::Pin;
use std::sync::{Arc, Mutex};

use futures::Stream;
use spicedb_proto::authzed::api::materialize::v0 as materialize;
use spicedb_proto::authzed::api::materialize::v0::relationships_service_server::{
    RelationshipsService, RelationshipsServiceServer,
};
use spicedb_proto::authzed::api::materialize::v0::roaring_lookup_resources_service_server::{
    RoaringLookupResourcesService, RoaringLookupResourcesServiceServer,
};
use spicedb_proto::authzed::api::materialize::v0::watch_permission_sets_service_server::{
    WatchPermissionSetsService, WatchPermissionSetsServiceServer,
};
use spicedb_proto::authzed::api::v1 as proto;
use spicedb_proto::authzed::api::v1::experimental_service_server::{
    ExperimentalService, ExperimentalServiceServer,
};
use spicedb_proto::authzed::api::v1::permissions_service_server::{
    PermissionsService, PermissionsServiceServer,
};
use spicedb_proto::authzed::api::v1::schema_service_server::{SchemaService, SchemaServiceServer};
use spicedb_proto::authzed::api::v1::watch_service_server::{WatchService, WatchServiceServer};
use tonic::metadata::MetadataMap;
use tonic::transport::{Channel, Endpoint, Server};
use tonic::{Code, Request, Response, Status};
use tonic_types::{ErrorDetails, StatusExt};

use crate::error::SpiceDBError;
use crate::options::Operation;
use crate::protoconv;
use crate::types::*;
use crate::SpiceDBClient;

const DEFAULT_DELETE_LIMIT: u32 = 1_000;

#[derive(Default)]
struct State {
    relationships: Vec<Relationship>,
    revision: u64,
    checks: HashMap<String, Permissionship>,
    resolver: Option<CheckResolver>,
    schema: Option<String>,
    reflection: Option<ReflectSchemaResult>,
    headers: MetadataMap,
    calls: Vec<Call>,
    counts: HashMap<Operation, usize>,
    hook: Option<FaultHook>,
    schema_revision: SchemaRevision,
}

type FaultHook = Arc<dyn Fn(&Fault) -> Option<Status> + Send + Sync>;
type CheckResolver = Arc<dyn Fn(&PermissionCheck) -> Option<Resolution> + Send + Sync>;

/// The answer a check resolver gives
/// ([`InMemorySpiceDB::set_check_resolver`]).
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct Resolution {
    /// The check's permissionship.
    pub permissionship: Permissionship,
    /// For [`Permissionship::ConditionalPermission`], the caveat context the
    /// check is missing; it reaches the client as
    /// [`CheckResult::missing_context`] and as a lookup result's
    /// `partial_caveat`.
    pub missing_context: Vec<String>,
}

impl Resolution {
    /// Granted outright.
    pub fn has_permission() -> Self {
        Permissionship::HasPermission.into()
    }
    /// Not granted.
    pub fn no_permission() -> Self {
        Permissionship::NoPermission.into()
    }
    /// Granted only if a caveat the caller did not satisfy holds; `missing`
    /// names the context it needs.
    pub fn conditional<S: Into<String>>(missing: impl IntoIterator<Item = S>) -> Self {
        Self {
            permissionship: Permissionship::ConditionalPermission,
            missing_context: missing.into_iter().map(Into::into).collect(),
        }
    }
    fn granted(&self) -> bool {
        matches!(
            self.permissionship,
            Permissionship::HasPermission | Permissionship::ConditionalPermission
        )
    }
    fn partial_caveat(&self) -> Option<proto::PartialCaveatInfo> {
        (self.permissionship == Permissionship::ConditionalPermission).then(|| {
            proto::PartialCaveatInfo {
                missing_required_context: self.missing_context.clone(),
            }
        })
    }
}

impl From<Permissionship> for Resolution {
    fn from(permissionship: Permissionship) -> Self {
        Self {
            permissionship,
            missing_context: Vec::new(),
        }
    }
}

/// A check resolver that grants every check, for tests whose IDs are generated
/// at run time:
///
/// ```
/// # #[tokio::main(flavor = "current_thread")]
/// # async fn main() -> Result<(), spicedb::SpiceDBError> {
/// use spicedb::testing::{grant_all, InMemorySpiceDB};
/// use spicedb::{consistency, ObjectRef, PermissionCheck};
/// let fake = InMemorySpiceDB::new();
/// fake.set_check_resolver(grant_all);
/// let check = PermissionCheck::new(
///     ObjectRef::new("document", "generated-id")?,
///     "view",
///     ObjectRef::new("user", "alice")?.into(),
///     consistency::full(),
/// );
/// assert!(fake.client()?.check(check).await?.has_permission());
/// # Ok(()) }
/// ```
pub fn grant_all(_: &PermissionCheck) -> Option<Resolution> {
    Some(Resolution::has_permission())
}

/// A recorded protocol request, downcast with [`Call::request`].
trait Recorded: Any + Debug + Send + Sync {
    fn as_any(&self) -> &dyn Any;
}
impl<T: Any + Debug + Send + Sync> Recorded for T {
    fn as_any(&self) -> &dyn Any {
        self
    }
}

/// One RPC the double received.
#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct Call {
    operation: Operation,
    index: usize,
    request: Arc<dyn Recorded>,
    metadata: MetadataMap,
}

impl Call {
    /// The client operation that sends this RPC. Pages of
    /// `delete_all_relationships_non_atomic` are
    /// [`Operation::DeleteRelationships`] RPCs.
    pub fn operation(&self) -> Operation {
        self.operation
    }
    /// Counts RPCs of this operation, starting at 0.
    pub fn index(&self) -> usize {
        self.index
    }
    /// The protocol request, if it is a `T`, for example
    /// `call.request::<spicedb::spicedb_proto::authzed::api::v1::ReadRelationshipsRequest>()`.
    /// An import's request is an `ImportBulkRelationshipsRequest` without
    /// relationships (they arrive in batches; see [`FaultStage::ImportBatch`]
    /// and [`InMemorySpiceDB::relationships`]).
    pub fn request<T: Any>(&self) -> Option<&T> {
        (*self.request).as_any().downcast_ref()
    }
    /// The request metadata, including [`CallOptions`](crate::CallOptions)
    /// request metadata and `grpc-timeout`.
    pub fn metadata(&self) -> &MetadataMap {
        &self.metadata
    }
}

/// Where in an RPC a fault hook runs.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
#[non_exhaustive]
pub enum FaultStage {
    /// Before the RPC does anything, including before a stream sends its
    /// first item. The client sees the error as SpiceDB's: reads and stream
    /// establishment retry `Unavailable` under the client's policy.
    Call,
    /// Before import batch [`Fault::item`] (0-based) is applied. A batch is
    /// one message, so it has the call's batch size
    /// ([`ImportRelationshipsOptions::with_batch_size`](crate::ImportRelationshipsOptions::with_batch_size),
    /// default 1,000).
    ImportBatch,
    /// After every batch was received, before the import commits.
    ImportCommit,
    /// Before a read, export or lookup sends item [`Fault::item`] (0-based). The
    /// stream ends with the error; the client never restarts a stream that
    /// exposed data.
    Item,
}

/// The point at which a fault hook is consulted.
#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct Fault {
    pub call: Call,
    pub stage: FaultStage,
    /// The batch index for [`FaultStage::ImportBatch`], the item (or lookup result) index for
    /// [`FaultStage::Item`], 0 otherwise.
    pub item: usize,
    /// The relationships in the batch at [`FaultStage::ImportBatch`]; empty
    /// otherwise.
    pub batch: Vec<Relationship>,
}

/// Whether `read_schema` reports a revision
/// ([`InMemorySpiceDB::set_read_schema_revision`]).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Hash)]
#[non_exhaustive]
pub enum SchemaRevision {
    /// Report the store's current revision. The default.
    #[default]
    Included,
    /// Report none, as some SpiceDB deployments do, so code that falls back
    /// to another revision source can be tested. The client then returns an
    /// empty [`ZedToken`] as
    /// [`ReadSchemaResult::revision`](crate::ReadSchemaResult::revision).
    Omitted,
}

/// The cursor that resumes a read or export after its first `n` matching
/// relationships (in insertion order): the same cursor the double puts on
/// item `n` (1-based) as `after_result_cursor`. Pass it to the options'
/// `with_cursor`. For `lookup_resources` it resumes after the first `n`
/// granted resources (counting conditional ones a permissionship filter
/// drops). The format, the decimal count `n`, is stable for tests.
///
/// ```
/// # #[tokio::main(flavor = "current_thread")]
/// # async fn main() -> Result<(), spicedb::SpiceDBError> {
/// use futures::TryStreamExt;
/// use spicedb::testing::{cursor_after, InMemorySpiceDB};
/// use spicedb::{consistency, Filter, ReadRelationshipsOptions, ReadRelationshipsRequest, Relationship};
/// let rels: Vec<Relationship> = (0..3)
///     .map(|i| format!("doc:d{i}#viewer@user:x").parse().unwrap())
///     .collect();
/// let fake = InMemorySpiceDB::with_relationships(rels.clone());
/// let request = ReadRelationshipsRequest::new(Filter::new("doc"), consistency::full())
///     .with_options(ReadRelationshipsOptions::new().with_cursor(cursor_after(2)));
/// let read: Vec<_> = fake.client()?.read_relationships(request).try_collect().await?;
/// assert_eq!(read.len(), 1);
/// assert_eq!(read[0].relationship, rels[2]);
/// assert_eq!(read[0].after_result_cursor.as_deref(), Some(cursor_after(3).as_str()));
/// # Ok(()) }
/// ```
pub fn cursor_after(n: usize) -> String {
    n.to_string()
}

/// A status shaped like SpiceDB's, for fault hooks. The client maps it as it
/// maps a real server failure: the matching [`SpiceDBError`] variant, with
/// `message` as [`SpiceDBError::server_message`].
pub fn server_error(code: Code, message: impl Into<String>) -> Status {
    Status::new(code, message)
}

/// [`server_error`] with SpiceDB's `ErrorInfo` reason (domain `authzed.com`),
/// for code that branches on [`SpiceDBError::reason`].
pub fn server_error_with_reason(code: Code, message: impl Into<String>, reason: &str) -> Status {
    reasoned(code, message.into(), reason)
}

impl State {
    /// A successful response carrying the registered headers.
    fn respond<T>(&self, message: T) -> Response<T> {
        let mut response = Response::new(message);
        *response.metadata_mut() = self.headers.clone();
        response
    }
    fn token(&self) -> proto::ZedToken {
        proto::ZedToken {
            token: self.revision.to_string(),
        }
    }
    fn index(&self, r: &Relationship) -> Option<usize> {
        let identity = r.filter();
        self.relationships.iter().position(|e| identity.matches(e))
    }
    fn upsert(&mut self, r: Relationship) {
        match self.index(&r) {
            Some(i) => self.relationships[i] = r,
            None => self.relationships.push(r),
        }
    }
    /// Step 3 of check resolution: an unexpired stored relationship on the
    /// resource, with the permission's name as its relation and a matching
    /// subject, grants; a caveated one only conditionally (caveats are not
    /// evaluated), and an uncaveated match wins.
    fn resolve_stored(&self, check: &PermissionCheck) -> Resolution {
        let now = chrono::Utc::now();
        let mut result = Resolution::no_permission();
        for r in &self.relationships {
            if r.resource() != check.resource()
                || r.relation() != check.permission()
                || !subject_matches(r.subject(), check.subject())
                || r.expiration().is_some_and(|e| e <= now)
            {
                continue;
            }
            if r.caveat_name().is_empty() {
                return Resolution::has_permission();
            }
            result = Resolution::conditional(Vec::<String>::new());
        }
        result
    }
    /// IDs of objects of `object_type` in the stored relationships (as
    /// resource or subject), in order of first appearance.
    fn object_ids(&self, object_type: &str) -> Vec<String> {
        let mut seen = std::collections::HashSet::new();
        let mut ids = Vec::new();
        for r in &self.relationships {
            for o in [r.resource(), r.subject().object()] {
                if o.object_type() == object_type && seen.insert(o.object_id().to_string()) {
                    ids.push(o.object_id().to_string());
                }
            }
        }
        ids
    }
    fn check_preconditions(&self, preconditions: &[proto::Precondition]) -> Result<(), Status> {
        for p in preconditions {
            let p = protoconv::precondition_from_proto(p).map_err(Status::from)?;
            let matched = self.relationships.iter().any(|r| p.filter.matches(r));
            let ok = match p.operation {
                PreconditionOperation::MustMatch => matched,
                _ => !matched,
            };
            if !ok {
                return Err(reasoned(
                    Code::FailedPrecondition,
                    format!("unable to satisfy write precondition `{p}`"),
                    "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
                ));
            }
        }
        Ok(())
    }
}

fn reasoned(code: Code, message: String, reason: &str) -> Status {
    Status::with_error_details(
        code,
        message,
        ErrorDetails::with_error_info(reason, "authzed.com", HashMap::new()),
    )
}

fn check_key(resource: &ObjectRef, permission: &str, subject: &SubjectRef) -> String {
    format!("{resource}#{permission}@{subject}")
}

/// Whether a stored subject grants to the checked one: exactly, or as a
/// wildcard (`type:*`) over a direct subject of that type.
fn subject_matches(stored: &SubjectRef, checked: &SubjectRef) -> bool {
    stored == checked
        || (stored.object().object_id() == "*"
            && stored.relation().is_none()
            && checked.relation().is_none()
            && stored.object().object_type() == checked.object().object_type())
}

/// The native check the server received, for the resolver.
fn check_from_proto(
    resource: Option<&proto::ObjectReference>,
    permission: &str,
    subject: Option<&proto::SubjectReference>,
    consistency: Option<&proto::Consistency>,
    context: Option<&prost_types::Struct>,
) -> Result<PermissionCheck, Status> {
    let resource =
        protoconv::object_from_proto(required(resource, "resource")?).map_err(Status::from)?;
    let subject =
        protoconv::subject_from_proto(required(subject, "subject")?).map_err(Status::from)?;
    if permission.is_empty() {
        return Err(Status::invalid_argument("permission is required"));
    }
    let consistency = match consistency {
        Some(c) => protoconv::consistency_from_proto(c).map_err(Status::from)?,
        None => crate::consistency::min_latency(),
    };
    let mut check = PermissionCheck::new(resource, permission, subject, consistency);
    if let Some(context) = context {
        check = check.with_context(context_from_proto(context));
    }
    Ok(check)
}

fn check_permissionship_to_proto(p: &Permissionship) -> i32 {
    use proto::check_permission_response::Permissionship as P;
    (match p {
        Permissionship::HasPermission => P::HasPermission,
        Permissionship::ConditionalPermission => P::ConditionalPermission,
        Permissionship::NoPermission => P::NoPermission,
        _ => P::Unspecified,
    }) as i32
}

fn lookup_permissionship_to_proto(p: &Permissionship) -> i32 {
    (match p {
        Permissionship::ConditionalPermission => proto::LookupPermissionship::ConditionalPermission,
        _ => proto::LookupPermissionship::HasPermission,
    }) as i32
}

/// An in-memory SpiceDB double. Cheap to clone; clones share one store.
#[derive(Clone, Default)]
pub struct InMemorySpiceDB {
    state: Arc<Mutex<State>>,
}

impl std::fmt::Debug for InMemorySpiceDB {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("InMemorySpiceDB")
            .field("relationships", &self.relationships())
            .finish()
    }
}

impl InMemorySpiceDB {
    /// An empty store.
    pub fn new() -> Self {
        Self::default()
    }

    /// A store holding `relationships`.
    pub fn with_relationships(relationships: impl IntoIterator<Item = Relationship>) -> Self {
        let fake = Self::new();
        {
            let mut state = fake.lock();
            for r in relationships {
                state.upsert(r);
            }
        }
        fake
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Installs `hook`, which runs at every [`Fault`] point (without the
    /// store's lock held). A returned status fails the RPC at that point, and
    /// a failed write or import applies nothing. Build statuses with
    /// [`server_error`]. Replaces any previous hook.
    ///
    /// ```
    /// # #[tokio::main(flavor = "current_thread")]
    /// # async fn main() {
    /// use spicedb::testing::{server_error, FaultStage, InMemorySpiceDB};
    /// use spicedb::{ImportRelationshipsOptions, ImportRelationshipsRequest, Operation, Relationship};
    /// let fake = InMemorySpiceDB::new();
    /// fake.set_fault_hook(|fault| match (fault.call.operation(), fault.stage, fault.item) {
    ///     (Operation::ImportRelationships, FaultStage::ImportBatch, 1) => {
    ///         Some(server_error(tonic::Code::AlreadyExists, "duplicate relationship"))
    ///     }
    ///     _ => None,
    /// });
    /// let rels: Vec<Relationship> = (0..4)
    ///     .map(|i| format!("doc:d{i}#viewer@user:x").parse().unwrap())
    ///     .collect();
    /// let request = ImportRelationshipsRequest::new(futures::stream::iter(rels.into_iter().map(Ok)))
    ///     .with_options(ImportRelationshipsOptions::new().with_batch_size(std::num::NonZeroU32::new(2).unwrap()));
    /// let err = fake.client().unwrap().import_relationships(request).await.unwrap_err();
    /// assert_eq!(err.code(), tonic::Code::AlreadyExists);
    /// assert!(fake.relationships().is_empty());
    /// # }
    /// ```
    pub fn set_fault_hook(&self, hook: impl Fn(&Fault) -> Option<Status> + Send + Sync + 'static) {
        self.lock().hook = Some(Arc::new(hook));
    }

    /// Removes the fault hook.
    pub fn clear_fault_hook(&self) {
        self.lock().hook = None;
    }

    /// Fails the next RPC of `operation` with `status` before it does
    /// anything; later RPCs succeed. Replaces any fault hook. With a
    /// retryable code (`Unavailable`), a read or stream establishment is
    /// retried by the client under its policy and then succeeds.
    pub fn fail_next(&self, operation: Operation, status: Status) {
        let pending = Mutex::new(Some(status));
        self.set_fault_hook(move |fault| {
            if fault.stage == FaultStage::Call && fault.call.operation == operation {
                return pending.lock().unwrap_or_else(|e| e.into_inner()).take();
            }
            None
        });
    }

    /// Every recorded RPC, in order.
    pub fn calls(&self) -> Vec<Call> {
        self.lock().calls.clone()
    }

    /// The recorded RPCs of one operation, in order.
    pub fn calls_of(&self, operation: Operation) -> Vec<Call> {
        self.lock()
            .calls
            .iter()
            .filter(|c| c.operation == operation)
            .cloned()
            .collect()
    }

    /// Records an RPC and runs the [`FaultStage::Call`] hook.
    fn begin<T: Any + Debug + Send + Sync + Clone>(
        &self,
        operation: Operation,
        request: &Request<T>,
    ) -> Result<Call, Status> {
        self.begin_with(operation, request.metadata(), request.get_ref().clone())
    }

    fn begin_with(
        &self,
        operation: Operation,
        metadata: &MetadataMap,
        request: impl Recorded,
    ) -> Result<Call, Status> {
        let call = {
            let mut state = self.lock();
            let count = state.counts.entry(operation).or_default();
            let call = Call {
                operation,
                index: *count,
                request: Arc::new(request),
                metadata: metadata.clone(),
            };
            *count += 1;
            state.calls.push(call.clone());
            call
        };
        self.fault(&call, FaultStage::Call, 0)?;
        Ok(call)
    }

    /// Runs the hook, if any, at one point of a call.
    fn fault(&self, call: &Call, stage: FaultStage, item: usize) -> Result<(), Status> {
        self.fault_batch(call, stage, item, Vec::new())
    }

    fn fault_batch(
        &self,
        call: &Call,
        stage: FaultStage,
        item: usize,
        batch: Vec<Relationship>,
    ) -> Result<(), Status> {
        let hook = self.lock().hook.clone();
        let injected = hook.and_then(|hook| {
            hook(&Fault {
                call: call.clone(),
                stage,
                item,
                batch,
            })
        });
        match injected {
            Some(status) => Err(status),
            None => Ok(()),
        }
    }

    /// A stream of `items` that consults the [`FaultStage::Item`] hook before
    /// each one and ends with the first injected error.
    fn faulted_items<T: Send + 'static>(&self, call: Call, items: Vec<T>) -> BoxStream<T> {
        let fake = self.clone();
        let mut failed = false;
        Box::pin(futures::stream::iter(
            items.into_iter().enumerate().map_while(move |(i, item)| {
                if failed {
                    return None;
                }
                match fake.fault(&call, FaultStage::Item, i) {
                    Ok(()) => Some(Ok(item)),
                    Err(status) => {
                        failed = true;
                        Some(Err(status))
                    }
                }
            }),
        ))
    }

    /// The stored relationships, in insertion order.
    pub fn relationships(&self) -> Vec<Relationship> {
        self.lock().relationships.clone()
    }

    /// Registers the permissionship `check`, `check_bulk` and lookups resolve
    /// to for this exact question, ahead of the check resolver and the stored
    /// relationships (see [`set_check_resolver`](Self::set_check_resolver)
    /// for the order).
    pub fn set_check_result(
        &self,
        resource: &ObjectRef,
        permission: &str,
        subject: &SubjectRef,
        permissionship: Permissionship,
    ) {
        self.lock()
            .checks
            .insert(check_key(resource, permission, subject), permissionship);
    }

    /// Installs `resolver`, which answers checks (including those a lookup
    /// makes) that have no result registered with
    /// [`set_check_result`](Self::set_check_result). A check resolves, in
    /// order, to:
    ///
    /// 1. the result registered for that exact question;
    /// 2. the resolver's answer, when it returns `Some` ([`grant_all`] grants
    ///    everything);
    /// 3. the stored relationships: an unexpired relationship on the resource
    ///    whose relation is the permission's name, with the checked subject
    ///    or a wildcard (`type:*`) over a direct subject of that type, grants
    ///    it. Caveats are not evaluated: a caveated match is
    ///    `ConditionalPermission`, and an uncaveated match wins over it;
    /// 4. otherwise `NoPermission`.
    ///
    /// The resolver receives the check as the server received it, including
    /// the consistency and caveat context (call-level context merged in, the
    /// check's own keys winning). It cannot fail a check: inject failures
    /// with [`set_fault_hook`](Self::set_fault_hook). It runs without the
    /// store's lock held. Replaces any previous resolver.
    ///
    /// ```
    /// # #[tokio::main(flavor = "current_thread")]
    /// # async fn main() -> Result<(), spicedb::SpiceDBError> {
    /// use spicedb::testing::{InMemorySpiceDB, Resolution};
    /// use spicedb::{consistency, ObjectRef, PermissionCheck};
    /// let fake = InMemorySpiceDB::new();
    /// // Admins see every document; everyone else falls through to the
    /// // stored relationships.
    /// fake.set_check_resolver(|check| {
    ///     (check.subject().object().object_id() == "admin").then(Resolution::has_permission)
    /// });
    /// let client = fake.client()?;
    /// let check = |user: &str| -> Result<PermissionCheck, spicedb::SpiceDBError> {
    ///     Ok(PermissionCheck::new(
    ///         ObjectRef::new("document", "any")?,
    ///         "view",
    ///         ObjectRef::new("user", user)?.into(),
    ///         consistency::full(),
    ///     ))
    /// };
    /// assert!(client.check(check("admin")?).await?.has_permission());
    /// assert!(!client.check(check("alice")?).await?.has_permission());
    /// # Ok(()) }
    /// ```
    pub fn set_check_resolver(
        &self,
        resolver: impl Fn(&PermissionCheck) -> Option<Resolution> + Send + Sync + 'static,
    ) {
        self.lock().resolver = Some(Arc::new(resolver));
    }

    /// Removes the check resolver.
    pub fn clear_check_resolver(&self) {
        self.lock().resolver = None;
    }

    /// Resolves one check in the documented order. Takes the lock itself and
    /// releases it around the resolver.
    fn resolve(&self, check: &PermissionCheck) -> Resolution {
        let resolver = {
            let state = self.lock();
            let key = check_key(check.resource(), check.permission(), check.subject());
            if let Some(p) = state.checks.get(&key) {
                return p.clone().into();
            }
            state.resolver.clone()
        };
        if let Some(answer) = resolver.and_then(|r| r(check)) {
            return answer;
        }
        self.lock().resolve_stored(check)
    }

    /// Registers the result `reflect_schema` returns (its revision is
    /// replaced with the store's current one). Without one, `reflect_schema`
    /// fails with `NotFound`. Build it with field assignment:
    ///
    /// ```
    /// use spicedb::{ReflectSchemaResult, SchemaDefinition};
    /// let mut user = SchemaDefinition::default();
    /// user.name = "user".into();
    /// let mut reflection = ReflectSchemaResult::default();
    /// reflection.definitions.push(user);
    /// spicedb::testing::InMemorySpiceDB::new().set_reflect_schema_result(reflection);
    /// ```
    pub fn set_reflect_schema_result(&self, result: ReflectSchemaResult) {
        self.lock().reflection = Some(result);
    }

    /// Selects whether `read_schema` reports a revision (by default it
    /// reports the store's current one).
    pub fn set_read_schema_revision(&self, mode: SchemaRevision) {
        self.lock().schema_revision = mode;
    }

    /// The schema text last written with `write_schema`.
    pub fn schema(&self) -> Option<String> {
        self.lock().schema.clone()
    }

    /// Headers sent on every successful response, for code that reads them
    /// through [`ResponseMetadata`](crate::ResponseMetadata).
    pub fn set_response_metadata(&self, headers: MetadataMap) {
        self.lock().headers = headers;
    }

    /// An in-memory channel to this double. Each connection the channel makes
    /// is served on the current Tokio runtime.
    pub fn channel(&self) -> Channel {
        let service = PermissionsServiceServer::new(Service(self.clone()));
        let schema = SchemaServiceServer::new(Service(self.clone()));
        let watch = WatchServiceServer::new(Service(self.clone()));
        let experimental = ExperimentalServiceServer::new(Service(self.clone()));
        let counts = RelationshipsServiceServer::new(Service(self.clone()));
        let roaring = RoaringLookupResourcesServiceServer::new(Service(self.clone()));
        let sets = WatchPermissionSetsServiceServer::new(Service(self.clone()));
        Endpoint::from_static("http://in-memory.spicedb").connect_with_connector_lazy(
            tower::service_fn(move |_: tonic::codegen::http::Uri| {
                let (client_io, server_io) = tokio::io::duplex(64 * 1024);
                let service = service.clone();
                let schema = schema.clone();
                let watch = watch.clone();
                let experimental = experimental.clone();
                let counts = counts.clone();
                let roaring = roaring.clone();
                let sets = sets.clone();
                tokio::spawn(async move {
                    let _ = Server::builder()
                        .add_service(service)
                        .add_service(schema)
                        .add_service(watch)
                        .add_service(experimental)
                        .add_service(counts)
                        .add_service(roaring)
                        .add_service(sets)
                        .serve_with_incoming(futures::stream::iter([Ok::<_, std::io::Error>(
                            server_io,
                        )]))
                        .await;
                });
                async move { Ok::<_, std::io::Error>(hyper_util::rt::TokioIo::new(client_io)) }
            }),
        )
    }

    /// A real [`SpiceDBClient`] connected to this double.
    pub fn client(&self) -> Result<SpiceDBClient, SpiceDBError> {
        SpiceDBClient::from_channel(self.channel()).build()
    }
}

struct Service(InMemorySpiceDB);

type BoxStream<T> = Pin<Box<dyn Stream<Item = Result<T, Status>> + Send>>;

/// For RPCs no client method sends (deprecated experimental RPCs): not
/// recorded, since no [`Operation`] names them.
/// Fails an RPC the double does not model with `UNIMPLEMENTED`, as SpiceDB
/// fails an RPC it does not serve.
fn unimplemented<T>(rpc: &str) -> Result<T, Status> {
    Err(Status::unimplemented(format!(
        "{rpc} is not modeled by spicedb::testing::InMemorySpiceDB"
    )))
}

impl Service {
    /// Records an RPC the double does not model, runs the
    /// [`FaultStage::Call`] hook, and otherwise fails it with
    /// `UNIMPLEMENTED`.
    fn unmodeled<T: Any + Debug + Send + Sync + Clone, R>(
        &self,
        operation: Operation,
        request: &Request<T>,
        rpc: &str,
    ) -> Result<R, Status> {
        self.0.begin(operation, request)?;
        unimplemented(rpc)
    }
}

fn required<'a, T>(value: Option<&'a T>, field: &str) -> Result<&'a T, Status> {
    value.ok_or_else(|| Status::invalid_argument(format!("{field} is required")))
}

#[tonic::async_trait]
impl PermissionsService for Service {
    type ReadRelationshipsStream = BoxStream<proto::ReadRelationshipsResponse>;
    type LookupResourcesStream = BoxStream<proto::LookupResourcesResponse>;
    type LookupSubjectsStream = BoxStream<proto::LookupSubjectsResponse>;
    type ExportBulkRelationshipsStream = BoxStream<proto::ExportBulkRelationshipsResponse>;

    async fn read_relationships(
        &self,
        request: Request<proto::ReadRelationshipsRequest>,
    ) -> Result<Response<Self::ReadRelationshipsStream>, Status> {
        let call = self.0.begin(Operation::ReadRelationships, &request)?;
        let request = request.into_inner();
        let filter = protoconv::filter_from_proto(required(
            request.relationship_filter.as_ref(),
            "relationship_filter",
        )?);
        filter.to_proto().map_err(Status::from)?;
        let start = parse_cursor(request.optional_cursor.as_ref())?;
        let limit = match request.optional_limit {
            0 => usize::MAX,
            n => n as usize,
        };
        let state = self.0.lock();
        let read_at = state.token();
        let items: Vec<_> = state
            .relationships
            .iter()
            .filter(|r| filter.matches(r))
            .enumerate()
            .skip(start)
            .take(limit)
            .map(|(i, r)| proto::ReadRelationshipsResponse {
                read_at: Some(read_at.clone()),
                relationship: Some(r.to_proto()),
                after_result_cursor: Some(proto::Cursor {
                    token: cursor_after(i + 1),
                }),
            })
            .collect();
        let headers = state.headers.clone();
        drop(state);
        let mut response = Response::new(self.0.faulted_items(call, items));
        *response.metadata_mut() = headers;
        Ok(response)
    }

    async fn write_relationships(
        &self,
        request: Request<proto::WriteRelationshipsRequest>,
    ) -> Result<Response<proto::WriteRelationshipsResponse>, Status> {
        self.0.begin(Operation::Write, &request)?;
        let request = request.into_inner();
        let mut state = self.0.lock();
        state.check_preconditions(&request.optional_preconditions)?;
        if request.updates.is_empty() {
            // SpiceDB accepts an empty write and returns the current revision.
            return Ok(state.respond(proto::WriteRelationshipsResponse {
                written_at: Some(state.token()),
            }));
        }
        let mut staged = State {
            relationships: state.relationships.clone(),
            ..State::default()
        };
        for update in &request.updates {
            let update = protoconv::update_from_proto(update).map_err(Status::from)?;
            match update.operation {
                UpdateOperation::Create => {
                    if staged.index(&update.relationship).is_some() {
                        return Err(reasoned(
                            Code::AlreadyExists,
                            format!(
                                "could not CREATE relationship `{}`, as it already existed",
                                update.relationship
                            ),
                            "ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP",
                        ));
                    }
                    staged.upsert(update.relationship);
                }
                UpdateOperation::Touch => staged.upsert(update.relationship),
                UpdateOperation::Delete => {
                    if let Some(i) = staged.index(&update.relationship) {
                        staged.relationships.remove(i);
                    }
                }
                _ => return Err(Status::invalid_argument("unknown update operation")),
            }
        }
        state.relationships = staged.relationships;
        state.revision += 1;
        Ok(state.respond(proto::WriteRelationshipsResponse {
            written_at: Some(state.token()),
        }))
    }

    async fn delete_relationships(
        &self,
        request: Request<proto::DeleteRelationshipsRequest>,
    ) -> Result<Response<proto::DeleteRelationshipsResponse>, Status> {
        self.0.begin(Operation::DeleteRelationships, &request)?;
        let request = request.into_inner();
        let filter = protoconv::filter_from_proto(required(
            request.relationship_filter.as_ref(),
            "relationship_filter",
        )?);
        filter.to_proto().map_err(Status::from)?;
        let limit = match request.optional_limit {
            0 => DEFAULT_DELETE_LIMIT,
            n => n,
        } as usize;
        let mut state = self.0.lock();
        state.check_preconditions(&request.optional_preconditions)?;
        let matching = state
            .relationships
            .iter()
            .filter(|r| filter.matches(r))
            .count();
        if matching > limit && !request.optional_allow_partial_deletions {
            return Err(reasoned(
                Code::InvalidArgument,
                format!(
                    "found more than {limit} relationships to be deleted and partial deletion was not requested"
                ),
                "ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE",
            ));
        }
        let mut deleted = 0usize;
        state.relationships.retain(|r| {
            if deleted < limit && filter.matches(r) {
                deleted += 1;
                false
            } else {
                true
            }
        });
        state.revision += 1;
        let complete = deleted == matching;
        Ok(state.respond(proto::DeleteRelationshipsResponse {
            deleted_at: Some(state.token()),
            deletion_progress: if complete {
                proto::delete_relationships_response::DeletionProgress::Complete as i32
            } else {
                proto::delete_relationships_response::DeletionProgress::Partial as i32
            },
            relationships_deleted_count: deleted as u64,
            after_result_cursor: None,
        }))
    }

    async fn check_permission(
        &self,
        request: Request<proto::CheckPermissionRequest>,
    ) -> Result<Response<proto::CheckPermissionResponse>, Status> {
        self.0.begin(Operation::Check, &request)?;
        let request = request.into_inner();
        let check = check_from_proto(
            request.resource.as_ref(),
            &request.permission,
            request.subject.as_ref(),
            request.consistency.as_ref(),
            request.context.as_ref(),
        )?;
        let resolution = self.0.resolve(&check);
        let state = self.0.lock();
        Ok(state.respond(proto::CheckPermissionResponse {
            checked_at: Some(state.token()),
            permissionship: check_permissionship_to_proto(&resolution.permissionship),
            partial_caveat_info: resolution.partial_caveat(),
            ..Default::default()
        }))
    }

    async fn check_bulk_permissions(
        &self,
        request: Request<proto::CheckBulkPermissionsRequest>,
    ) -> Result<Response<proto::CheckBulkPermissionsResponse>, Status> {
        self.0.begin(Operation::CheckBulk, &request)?;
        let request = request.into_inner();
        let mut pairs = Vec::with_capacity(request.items.len());
        for item in request.items {
            let checked = check_from_proto(
                item.resource.as_ref(),
                &item.permission,
                item.subject.as_ref(),
                request.consistency.as_ref(),
                item.context.as_ref(),
            );
            let response = match checked {
                Ok(check) => {
                    let resolution = self.0.resolve(&check);
                    proto::check_bulk_permissions_pair::Response::Item(
                        proto::CheckBulkPermissionsResponseItem {
                            permissionship: check_permissionship_to_proto(
                                &resolution.permissionship,
                            ),
                            partial_caveat_info: resolution.partial_caveat(),
                            ..Default::default()
                        },
                    )
                }
                Err(status) => proto::check_bulk_permissions_pair::Response::Error(
                    spicedb_proto::google::rpc::Status {
                        code: status.code() as i32,
                        message: status.message().to_string(),
                        details: Vec::new(),
                    },
                ),
            };
            pairs.push(proto::CheckBulkPermissionsPair {
                request: Some(item),
                response: Some(response),
            });
        }
        let state = self.0.lock();
        Ok(state.respond(proto::CheckBulkPermissionsResponse {
            checked_at: Some(state.token()),
            pairs,
        }))
    }

    async fn expand_permission_tree(
        &self,
        request: Request<proto::ExpandPermissionTreeRequest>,
    ) -> Result<Response<proto::ExpandPermissionTreeResponse>, Status> {
        self.0.begin(Operation::ExpandPermissionTree, &request)?;
        unimplemented("ExpandPermissionTree")
    }

    /// The objects of the requested type in the stored relationships (as
    /// resource or subject), in first-appearance order, that check
    /// resolution grants to the subject. Each result's cursor counts granted
    /// results up to it (the client's permissionship filter drops some after
    /// they are counted); the limit counts results sent.
    async fn lookup_resources(
        &self,
        request: Request<proto::LookupResourcesRequest>,
    ) -> Result<Response<Self::LookupResourcesStream>, Status> {
        let call = self.0.begin(Operation::LookupResources, &request)?;
        let request = request.into_inner();
        if request.resource_object_type.is_empty() {
            return Err(Status::invalid_argument("resource_object_type is required"));
        }
        let start = parse_cursor(request.optional_cursor.as_ref())?;
        let limit = match request.optional_limit {
            0 => usize::MAX,
            n => n as usize,
        };
        let (ids, at, headers) = {
            let state = self.0.lock();
            (
                state.object_ids(&request.resource_object_type),
                state.token(),
                state.headers.clone(),
            )
        };
        let mut items = Vec::new();
        let mut position = 0;
        for id in ids {
            if id == "*" || items.len() >= limit {
                continue;
            }
            let resource = proto::ObjectReference {
                object_type: request.resource_object_type.clone(),
                object_id: id.clone(),
            };
            let check = check_from_proto(
                Some(&resource),
                &request.permission,
                request.subject.as_ref(),
                request.consistency.as_ref(),
                request.context.as_ref(),
            )?;
            let resolution = self.0.resolve(&check);
            if !resolution.granted() {
                continue;
            }
            position += 1;
            if position <= start {
                continue;
            }
            items.push(proto::LookupResourcesResponse {
                looked_up_at: Some(at.clone()),
                resource_object_id: id,
                permissionship: lookup_permissionship_to_proto(&resolution.permissionship),
                partial_caveat_info: resolution.partial_caveat(),
                after_result_cursor: Some(proto::Cursor {
                    token: cursor_after(position),
                }),
            });
        }
        let mut response = Response::new(self.0.faulted_items(call, items));
        *response.metadata_mut() = headers;
        Ok(response)
    }

    /// The objects of the requested subject type in the stored relationships
    /// (as resource or subject), in first-appearance order, that check
    /// resolution grants on the resource, with the requested subject relation
    /// (direct by default). A stored wildcard subject is returned as `*`
    /// unless wildcards are excluded. No cursor, as the client sends none;
    /// the limit counts results sent.
    #[allow(deprecated)]
    async fn lookup_subjects(
        &self,
        request: Request<proto::LookupSubjectsRequest>,
    ) -> Result<Response<Self::LookupSubjectsStream>, Status> {
        let call = self.0.begin(Operation::LookupSubjects, &request)?;
        let request = request.into_inner();
        if request.subject_object_type.is_empty() {
            return Err(Status::invalid_argument("subject_object_type is required"));
        }
        let limit = match request.optional_concrete_limit {
            0 => usize::MAX,
            n => n as usize,
        };
        let exclude_wildcards = request.wildcard_option
            == proto::lookup_subjects_request::WildcardOption::ExcludeWildcards as i32;
        let (ids, at, headers) = {
            let state = self.0.lock();
            (
                state.object_ids(&request.subject_object_type),
                state.token(),
                state.headers.clone(),
            )
        };
        let mut items = Vec::new();
        for id in ids {
            if items.len() >= limit || (id == "*" && exclude_wildcards) {
                continue;
            }
            let subject = proto::SubjectReference {
                object: Some(proto::ObjectReference {
                    object_type: request.subject_object_type.clone(),
                    object_id: id.clone(),
                }),
                optional_relation: request.optional_subject_relation.clone(),
            };
            let check = check_from_proto(
                request.resource.as_ref(),
                &request.permission,
                Some(&subject),
                request.consistency.as_ref(),
                request.context.as_ref(),
            )?;
            let resolution = self.0.resolve(&check);
            if !resolution.granted() {
                continue;
            }
            let permissionship = lookup_permissionship_to_proto(&resolution.permissionship);
            let partial_caveat_info = resolution.partial_caveat();
            items.push(proto::LookupSubjectsResponse {
                looked_up_at: Some(at.clone()),
                subject_object_id: id.clone(),
                excluded_subject_ids: Vec::new(),
                permissionship,
                partial_caveat_info: partial_caveat_info.clone(),
                subject: Some(proto::ResolvedSubject {
                    subject_object_id: id,
                    permissionship,
                    partial_caveat_info,
                }),
                excluded_subjects: Vec::new(),
                after_result_cursor: None,
            });
        }
        let mut response = Response::new(self.0.faulted_items(call, items));
        *response.metadata_mut() = headers;
        Ok(response)
    }

    /// One transaction, as SpiceDB does: a relationship that already exists
    /// (or repeats within the import) fails it with `ALREADY_EXISTS` and
    /// nothing is added. Each message is one batch for fault injection.
    async fn import_bulk_relationships(
        &self,
        request: Request<tonic::Streaming<proto::ImportBulkRelationshipsRequest>>,
    ) -> Result<Response<proto::ImportBulkRelationshipsResponse>, Status> {
        let call = self.0.begin_with(
            Operation::ImportRelationships,
            request.metadata(),
            proto::ImportBulkRelationshipsRequest::default(),
        )?;
        let mut stream = request.into_inner();
        let mut batch = Vec::new();
        let mut index = 0;
        while let Some(message) = stream.message().await? {
            let received = message
                .relationships
                .iter()
                .map(|r| protoconv::relationship_from_proto(r).map_err(Status::from))
                .collect::<Result<Vec<_>, _>>()?;
            self.0
                .fault_batch(&call, FaultStage::ImportBatch, index, received.clone())?;
            index += 1;
            batch.extend(received);
        }
        self.0.fault(&call, FaultStage::ImportCommit, 0)?;
        let mut state = self.0.lock();
        let mut staged = State {
            relationships: state.relationships.clone(),
            ..State::default()
        };
        for r in &batch {
            if staged.index(r).is_some() {
                return Err(Status::already_exists(format!(
                    "found duplicate relationship `{r}` while importing"
                )));
            }
            staged.relationships.push(r.clone());
        }
        state.relationships = staged.relationships;
        state.revision += 1;
        Ok(state.respond(proto::ImportBulkRelationshipsResponse {
            num_loaded: batch.len() as u64,
        }))
    }

    /// Every relationship (or those matching the request's filter), one per
    /// response so each carries its own cursor, honoring cursor and limit.
    async fn export_bulk_relationships(
        &self,
        request: Request<proto::ExportBulkRelationshipsRequest>,
    ) -> Result<Response<Self::ExportBulkRelationshipsStream>, Status> {
        let call = self.0.begin(Operation::ExportRelationships, &request)?;
        let request = request.into_inner();
        let filter = request
            .optional_relationship_filter
            .as_ref()
            .map(protoconv::filter_from_proto);
        if let Some(f) = &filter {
            f.to_proto().map_err(Status::from)?;
        }
        let start = parse_cursor(request.optional_cursor.as_ref())?;
        let limit = match request.optional_limit {
            0 => usize::MAX,
            n => n as usize,
        };
        let state = self.0.lock();
        let items: Vec<_> = state
            .relationships
            .iter()
            .filter(|r| filter.as_ref().is_none_or(|f| f.matches(r)))
            .enumerate()
            .skip(start)
            .take(limit)
            .map(|(i, r)| proto::ExportBulkRelationshipsResponse {
                after_result_cursor: Some(proto::Cursor {
                    token: cursor_after(i + 1),
                }),
                relationships: vec![r.to_proto()],
            })
            .collect();
        let headers = state.headers.clone();
        drop(state);
        let mut response = Response::new(self.0.faulted_items(call, items));
        *response.metadata_mut() = headers;
        Ok(response)
    }
}

fn parse_cursor(cursor: Option<&proto::Cursor>) -> Result<usize, Status> {
    match cursor {
        Some(c) => c
            .token
            .parse::<usize>()
            .map_err(|_| Status::invalid_argument("invalid cursor")),
        None => Ok(0),
    }
}

const NO_SCHEMA: &str = "No schema has been defined; please call WriteSchema to start";

#[tonic::async_trait]
impl SchemaService for Service {
    /// The schema last written, or `NOT_FOUND` as SpiceDB returns.
    async fn read_schema(
        &self,
        request: Request<proto::ReadSchemaRequest>,
    ) -> Result<Response<proto::ReadSchemaResponse>, Status> {
        self.0.begin(Operation::ReadSchema, &request)?;
        let state = self.0.lock();
        let schema = state
            .schema
            .clone()
            .ok_or_else(|| Status::not_found(NO_SCHEMA))?;
        let read_at = match state.schema_revision {
            SchemaRevision::Omitted => None,
            _ => Some(state.token()),
        };
        Ok(state.respond(proto::ReadSchemaResponse {
            schema_text: schema,
            read_at,
        }))
    }

    /// Stores the schema text. It is not compiled or validated.
    async fn write_schema(
        &self,
        request: Request<proto::WriteSchemaRequest>,
    ) -> Result<Response<proto::WriteSchemaResponse>, Status> {
        self.0.begin(Operation::WriteSchema, &request)?;
        let mut state = self.0.lock();
        state.schema = Some(request.into_inner().schema);
        state.revision += 1;
        Ok(state.respond(proto::WriteSchemaResponse {
            written_at: Some(state.token()),
        }))
    }

    /// The registered reflection at the current revision, or `NOT_FOUND`.
    async fn reflect_schema(
        &self,
        request: Request<proto::ReflectSchemaRequest>,
    ) -> Result<Response<proto::ReflectSchemaResponse>, Status> {
        self.0.begin(Operation::ReflectSchema, &request)?;
        let state = self.0.lock();
        let reflection = state
            .reflection
            .as_ref()
            .ok_or_else(|| Status::not_found(NO_SCHEMA))?;
        let mut response = protoconv::reflect_schema_result_to_proto(reflection);
        response.read_at = Some(state.token());
        Ok(state.respond(response))
    }

    async fn computable_permissions(
        &self,
        request: Request<proto::ComputablePermissionsRequest>,
    ) -> Result<Response<proto::ComputablePermissionsResponse>, Status> {
        self.0.begin(Operation::ComputablePermissions, &request)?;
        unimplemented("ComputablePermissions")
    }

    async fn dependent_relations(
        &self,
        request: Request<proto::DependentRelationsRequest>,
    ) -> Result<Response<proto::DependentRelationsResponse>, Status> {
        self.0.begin(Operation::DependentRelations, &request)?;
        unimplemented("DependentRelations")
    }

    async fn diff_schema(
        &self,
        request: Request<proto::DiffSchemaRequest>,
    ) -> Result<Response<proto::DiffSchemaResponse>, Status> {
        self.0.begin(Operation::DiffSchema, &request)?;
        unimplemented("DiffSchema")
    }
}

#[tonic::async_trait]
impl WatchService for Service {
    type WatchStream = BoxStream<proto::WatchResponse>;

    async fn watch(
        &self,
        request: Request<proto::WatchRequest>,
    ) -> Result<Response<Self::WatchStream>, Status> {
        self.unmodeled(Operation::Updates, &request, "Watch")
    }
}

/// The relationship counter RPCs are recorded; the deprecated experimental
/// RPCs, which no client method sends, are not.
#[tonic::async_trait]
impl ExperimentalService for Service {
    type BulkExportRelationshipsStream = BoxStream<proto::BulkExportRelationshipsResponse>;

    async fn bulk_import_relationships(
        &self,
        _: Request<tonic::Streaming<proto::BulkImportRelationshipsRequest>>,
    ) -> Result<Response<proto::BulkImportRelationshipsResponse>, Status> {
        unimplemented("BulkImportRelationships")
    }

    async fn bulk_export_relationships(
        &self,
        _: Request<proto::BulkExportRelationshipsRequest>,
    ) -> Result<Response<Self::BulkExportRelationshipsStream>, Status> {
        unimplemented("BulkExportRelationships")
    }

    async fn bulk_check_permission(
        &self,
        _: Request<proto::BulkCheckPermissionRequest>,
    ) -> Result<Response<proto::BulkCheckPermissionResponse>, Status> {
        unimplemented("BulkCheckPermission")
    }

    async fn experimental_reflect_schema(
        &self,
        _: Request<proto::ExperimentalReflectSchemaRequest>,
    ) -> Result<Response<proto::ExperimentalReflectSchemaResponse>, Status> {
        unimplemented("ExperimentalReflectSchema")
    }

    async fn experimental_computable_permissions(
        &self,
        _: Request<proto::ExperimentalComputablePermissionsRequest>,
    ) -> Result<Response<proto::ExperimentalComputablePermissionsResponse>, Status> {
        unimplemented("ExperimentalComputablePermissions")
    }

    async fn experimental_dependent_relations(
        &self,
        _: Request<proto::ExperimentalDependentRelationsRequest>,
    ) -> Result<Response<proto::ExperimentalDependentRelationsResponse>, Status> {
        unimplemented("ExperimentalDependentRelations")
    }

    async fn experimental_diff_schema(
        &self,
        _: Request<proto::ExperimentalDiffSchemaRequest>,
    ) -> Result<Response<proto::ExperimentalDiffSchemaResponse>, Status> {
        unimplemented("ExperimentalDiffSchema")
    }

    async fn experimental_register_relationship_counter(
        &self,
        request: Request<proto::ExperimentalRegisterRelationshipCounterRequest>,
    ) -> Result<Response<proto::ExperimentalRegisterRelationshipCounterResponse>, Status> {
        self.unmodeled(
            Operation::ExperimentalRegisterRelationshipCounter,
            &request,
            "ExperimentalRegisterRelationshipCounter",
        )
    }

    async fn experimental_count_relationships(
        &self,
        request: Request<proto::ExperimentalCountRelationshipsRequest>,
    ) -> Result<Response<proto::ExperimentalCountRelationshipsResponse>, Status> {
        self.unmodeled(
            Operation::ExperimentalCountRelationships,
            &request,
            "ExperimentalCountRelationships",
        )
    }

    async fn experimental_unregister_relationship_counter(
        &self,
        request: Request<proto::ExperimentalUnregisterRelationshipCounterRequest>,
    ) -> Result<Response<proto::ExperimentalUnregisterRelationshipCounterResponse>, Status> {
        self.unmodeled(
            Operation::ExperimentalUnregisterRelationshipCounter,
            &request,
            "ExperimentalUnregisterRelationshipCounter",
        )
    }
}

#[tonic::async_trait]
impl RelationshipsService for Service {
    async fn experimental_count_relationships_by_filter(
        &self,
        request: Request<materialize::ExperimentalCountRelationshipsByFilterRequest>,
    ) -> Result<Response<materialize::ExperimentalCountRelationshipsByFilterResponse>, Status> {
        self.unmodeled(
            Operation::ExperimentalCountRelationshipsByFilter,
            &request,
            "ExperimentalCountRelationshipsByFilter",
        )
    }
}

#[tonic::async_trait]
impl RoaringLookupResourcesService for Service {
    async fn experimental_roaring_lookup_resources(
        &self,
        request: Request<materialize::ExperimentalRoaringLookupResourcesRequest>,
    ) -> Result<Response<materialize::ExperimentalRoaringLookupResourcesResponse>, Status> {
        self.unmodeled(
            Operation::ExperimentalRoaringLookupResources,
            &request,
            "ExperimentalRoaringLookupResources",
        )
    }
}

#[tonic::async_trait]
impl WatchPermissionSetsService for Service {
    type WatchPermissionSetsStream = BoxStream<materialize::WatchPermissionSetsResponse>;
    type LookupPermissionSetsStream = BoxStream<materialize::LookupPermissionSetsResponse>;

    async fn watch_permission_sets(
        &self,
        request: Request<materialize::WatchPermissionSetsRequest>,
    ) -> Result<Response<Self::WatchPermissionSetsStream>, Status> {
        self.unmodeled(
            Operation::WatchPermissionSets,
            &request,
            "WatchPermissionSets",
        )
    }

    async fn lookup_permission_sets(
        &self,
        request: Request<materialize::LookupPermissionSetsRequest>,
    ) -> Result<Response<Self::LookupPermissionSetsStream>, Status> {
        self.unmodeled(
            Operation::LookupPermissionSets,
            &request,
            "LookupPermissionSets",
        )
    }

    async fn download_permission_sets(
        &self,
        request: Request<materialize::DownloadPermissionSetsRequest>,
    ) -> Result<Response<materialize::DownloadPermissionSetsResponse>, Status> {
        self.unmodeled(
            Operation::DownloadPermissionSets,
            &request,
            "DownloadPermissionSets",
        )
    }
}

#[cfg(test)]
#[path = "testing_surface_tests.rs"]
mod surface_tests;
