//! The SpiceDB client and all operations.
//!
//! Use [`SpiceDBClient::new_plaintext`], [`SpiceDBClient::new_system_tls`], or
//! [`SpiceDBClient::builder`] to create a client.
//!
//! # Connection Types
//!
//! - **Plaintext** — insecure, for testing only. The constructor name makes the
//!   lack of TLS obvious.
//! - **System TLS** — uses the system's TLS certificate pool. Recommended for
//!   production.
//! - **Builder** — full control over TLS configuration and other options.

use std::collections::HashMap;
use std::time::Duration;

use crate::consistency::Strategy;
use crate::error::{self, SpiceDBError};
use crate::types::{
    check_context_to_proto, check_result_from_bulk_item, merge_check_context,
    partial_caveat_from_proto, permission_tree_from_proto, permissionship_from_proto,
    resolved_subject_from_proto, CheckResult, CountResult, DeleteOptions, ExpandResult, Filter,
    LookupResource, LookupSubject, Permissionship, Precondition, PreconditionOperation,
    ReflectSchemaResult, RelationReference, Relationship, ResolvedSubject, SchemaCaveat,
    SchemaCaveatParameter, SchemaDefinition, SchemaDiff, SchemaPermission, SchemaRelation,
    Transaction, Update, UpdateOperation, WatchEvent, WatchOptions,
};

use futures::Stream;

use spicedb_proto::authzed::api::v1 as proto;
use spicedb_proto::SpiceDBProtoClient;

/// Default page sizes for transparent cursor-based pagination.
const DEFAULT_READ_PAGE_SIZE: u32 = 512;
const DEFAULT_LOOKUP_PAGE_SIZE: u32 = 512;
const DEFAULT_EXPORT_PAGE_SIZE: u32 = 512;
const DEFAULT_DELETE_PAGE_SIZE: u32 = 1_000;
const DEFAULT_CHECK_BATCH_SIZE: usize = 1_000;
const DEFAULT_IMPORT_BATCH_SIZE: usize = 1_000;

/// Maximum number of retry attempts for transient gRPC errors.
const MAX_RETRIES: u32 = 3;

/// Base delay for exponential backoff (in milliseconds).
const BASE_RETRY_DELAY_MS: u64 = 100;

/// Applied to every unary call that does not pass its own timeout (via a
/// `_with_timeout` method, or [`DeleteOptions::with_timeout`] for
/// `delete_relationships_with`).
///
/// Mirrors `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its comment
/// cites `grpc/grpc-node#541`, a known gRPC failure mode where a channel
/// that accepts a connection but never answers produces no error at all).
/// Without a finite default, a wedged SpiceDB hangs every caller that
/// didn't opt in to a timeout -- in practice, most callers -- forever: the
/// connection looks fine at the transport level, so nothing ever times out
/// and nothing is ever produced to retry. See root DESIGN.md, "RULE: A
/// unary call must have a deadline".
///
/// Deliberately NOT applied to streaming calls (`read_relationships`,
/// `lookup_resources`, `lookup_subjects`, `updates`, `export_relationships`)
/// -- those are long-lived by design, and applying this default to them
/// would make the stream itself the outage (see DESIGN.md, "Streaming
/// calls MUST NOT inherit the unary default").
const DEFAULT_TIMEOUT: Duration = Duration::from_secs(30);

/// The idiomatic SpiceDB client.
///
/// All methods are async and require a tokio runtime. Read operations take an
/// explicit [`Strategy`] parameter — consistency is never silently defaulted.
///
/// Streaming operations return `impl Stream<Item = Result<T, SpiceDBError>>`.
/// Cursor-based pagination is handled transparently within the client.
///
/// Transient gRPC errors (UNAVAILABLE, ABORTED) on read operations are
/// automatically retried with full-jitter exponential backoff. Mutations
/// (`write`, `delete_relationships`/`delete_relationships_with`,
/// `write_schema`, the counter register/unregister calls) are never
/// automatically retried, even on a transient error -- see
/// [`SpiceDBClient::call_once`].
pub struct SpiceDBClient {
    // The proto client holds the gRPC channel and bearer-token interceptor.
    proto: SpiceDBProtoClient,
    // Applied to any unary call that doesn't specify its own timeout. See
    // [`DEFAULT_TIMEOUT`].
    default_timeout: Duration,
}

/// Builder for configuring a [`SpiceDBClient`] with custom TLS and connection
/// options.
pub struct SpiceDBClientBuilder {
    endpoint: String,
    token: String,
    plaintext: bool,
    allow_insecure_remote_credentials: bool,
    default_timeout: Duration,
}

impl SpiceDBClientBuilder {
    /// Disables TLS for the connection. Use only for testing.
    ///
    /// By itself, this only permits a plaintext connection to a loopback
    /// endpoint (localhost, 127.0.0.0/8, or ::1) -- a `unix:` target is NOT
    /// loopback here and is refused outright, since tonic dials a URI and
    /// would resolve the DNS name `unix` instead of a socket path. See
    /// root DESIGN.md, "RULE: Credentials over insecure transport
    /// require an explicit opt-in". For a non-loopback endpoint, also call
    /// [`allow_insecure_remote_credentials`](Self::allow_insecure_remote_credentials).
    pub fn plaintext(mut self) -> Self {
        self.plaintext = true;
        self
    }

    /// Explicit, separately named opt-in permitting [`plaintext`](Self::plaintext)
    /// to target a non-loopback endpoint. Named and separate from
    /// `plaintext` on purpose: the rule requires an option a reader cannot
    /// mistake for a default, not a boolean that does double duty as the
    /// plaintext-transport switch. Call this only if you genuinely mean to
    /// send a bearer token in cleartext to a remote host.
    pub fn allow_insecure_remote_credentials(mut self) -> Self {
        self.allow_insecure_remote_credentials = true;
        self
    }

    /// Overrides the default applied to every unary call that doesn't
    /// specify its own timeout (see [`DEFAULT_TIMEOUT`], 30s). NOT applied
    /// to streaming calls, which are long-lived by design.
    pub fn default_timeout(mut self, timeout: Duration) -> Self {
        self.default_timeout = timeout;
        self
    }

    /// Builds the client, establishing the gRPC connection.
    ///
    /// # Errors
    ///
    /// Returns [`SpiceDBError::InvalidArgument`] if [`plaintext`](Self::plaintext)
    /// was called, `endpoint` is not loopback, and
    /// [`allow_insecure_remote_credentials`](Self::allow_insecure_remote_credentials)
    /// was not -- before any channel is created. See root DESIGN.md, "RULE:
    /// Credentials over insecure transport require an explicit opt-in".
    pub async fn build(self) -> Result<SpiceDBClient, SpiceDBError> {
        let proto = SpiceDBProtoClient::new_with_options(
            &self.endpoint,
            &self.token,
            self.plaintext,
            self.allow_insecure_remote_credentials,
        )
        .await
        .map_err(|e| match e {
            spicedb_proto::SpiceDBProtoClientError::InsecureRemoteHostNotAllowed(msg)
            | spicedb_proto::SpiceDBProtoClientError::UnixSocketNotSupported(msg)
            | spicedb_proto::SpiceDBProtoClientError::InvalidToken(msg) => {
                SpiceDBError::InvalidArgument(msg.into())
            }
            spicedb_proto::SpiceDBProtoClientError::Transport(e) => {
                SpiceDBError::Transport(e.to_string().into())
            }
        })?;

        Ok(SpiceDBClient {
            proto,
            default_timeout: self.default_timeout,
        })
    }
}

impl SpiceDBClient {
    /// Creates a client with an insecure (plaintext) connection.
    ///
    /// Use this for testing only — the lack of TLS is made obvious by the name.
    pub async fn new_plaintext(
        endpoint: impl Into<String>,
        token: impl Into<String>,
    ) -> Result<Self, SpiceDBError> {
        Self::builder(endpoint, token).plaintext().build().await
    }

    /// Creates a client using the system's TLS certificate pool.
    ///
    /// Use this for production connections.
    pub async fn new_system_tls(
        endpoint: impl Into<String>,
        token: impl Into<String>,
    ) -> Result<Self, SpiceDBError> {
        Self::builder(endpoint, token).build().await
    }

    /// Returns a builder for configuring the client with custom options.
    pub fn builder(endpoint: impl Into<String>, token: impl Into<String>) -> SpiceDBClientBuilder {
        SpiceDBClientBuilder {
            endpoint: endpoint.into(),
            token: token.into(),
            plaintext: false,
            allow_insecure_remote_credentials: false,
            default_timeout: DEFAULT_TIMEOUT,
        }
    }

    /// Resolves a per-call timeout override against [`Self`]'s
    /// `default_timeout`. `None` means "use the client default" -- there is
    /// deliberately no way to make an unbounded unary call. See root
    /// DESIGN.md, "RULE: A unary call must have a deadline".
    fn effective_timeout(&self, timeout: Option<Duration>) -> Duration {
        timeout.unwrap_or(self.default_timeout)
    }

    // -----------------------------------------------------------------------
    // Checks — all via BulkCheckPermissions
    // -----------------------------------------------------------------------

    /// Checks a single permission and returns a [`CheckResult`].
    ///
    /// Uses `BulkCheckPermissions` under the hood. `CheckResult::permissionship`
    /// carries the server's answer — a `ConditionalPermission` result means the
    /// server needed caveat context that was not supplied, and is NOT a grant.
    /// Prefer [`CheckResult::has_permission`] over comparing `permissionship`
    /// directly for the common case.
    ///
    /// See [`check_permission_with_context`](Self::check_permission_with_context)
    /// to supply a caveat context for evaluating caveats encountered during
    /// the check; a relationship built with
    /// [`Relationship::with_check_context`] still supplies its own context
    /// through this method (`check_permission` is
    /// `check_permission_with_context` with no context).
    ///
    /// # Errors
    ///
    /// Returns [`SpiceDBError`] if the gRPC call fails.
    #[must_use = "check results should not be silently discarded"]
    pub async fn check_permission(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationship: &Relationship,
    ) -> Result<CheckResult, SpiceDBError> {
        self.check_permission_with_context(consistency, permission, relationship, None)
            .await
    }

    /// [`check_permission`](Self::check_permission) with a caveat context.
    /// Caveat context supplies named values (e.g. `"now"`) that SpiceDB
    /// needs to evaluate a caveat expression encountered during the check;
    /// without it, a caveated match comes back as
    /// `Permissionship::ConditionalPermission` instead of a grant, and
    /// `CheckResult::missing_context` names what was needed.
    ///
    /// If `relationship` was built with [`Relationship::with_check_context`],
    /// its context is merged with `context` per the key-level merge rule
    /// documented on
    /// [`check_permissions_with_context`](Self::check_permissions_with_context):
    /// the relationship's keys win on conflict, and any `context` keys it
    /// doesn't specify are retained. Pass `None` for no context (equivalent
    /// to [`check_permission`](Self::check_permission)).
    ///
    /// # Errors
    ///
    /// Returns [`SpiceDBError`] if the gRPC call fails.
    #[must_use = "check results should not be silently discarded"]
    pub async fn check_permission_with_context(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationship: &Relationship,
        context: Option<&HashMap<String, serde_json::Value>>,
    ) -> Result<CheckResult, SpiceDBError> {
        let mut results = self
            .check_permissions_impl(
                consistency,
                permission,
                std::slice::from_ref(relationship),
                context,
                None,
            )
            .await?;
        Ok(results.remove(0))
    }

    /// [`check_permission`](Self::check_permission) with a per-call timeout
    /// that overrides the client's `default_timeout` (seconds bounding this
    /// call). See root DESIGN.md, "RULE: A unary call must have a
    /// deadline".
    #[must_use = "check results should not be silently discarded"]
    pub async fn check_permission_with_timeout(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationship: &Relationship,
        timeout: Duration,
    ) -> Result<CheckResult, SpiceDBError> {
        let mut results = self
            .check_permissions_impl(
                consistency,
                permission,
                std::slice::from_ref(relationship),
                None,
                Some(timeout),
            )
            .await?;
        Ok(results.remove(0))
    }

    /// Checks permissions on multiple relationships and returns a
    /// [`CheckResult`] for each, in the same order as `relationships`.
    ///
    /// Uses `BulkCheckPermissions` under the hood. Large batches are
    /// automatically split into chunks of 1,000. Each result's `checked_at`
    /// is the revision the whole batch was evaluated at (`BulkCheckPermissions`
    /// carries one `checked_at` per response, not per item).
    ///
    /// See [`check_permissions_with_context`](Self::check_permissions_with_context)
    /// to supply a call-level caveat context default; a relationship built
    /// with [`Relationship::with_check_context`] still supplies its own
    /// per-item context through this method.
    pub async fn check_permissions(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
    ) -> Result<Vec<CheckResult>, SpiceDBError> {
        self.check_permissions_with_context(consistency, permission, relationships, None)
            .await
    }

    /// [`check_permissions`](Self::check_permissions) with a call-level
    /// default caveat context applied to every relationship in
    /// `relationships`.
    ///
    /// A relationship built with [`Relationship::with_check_context`]
    /// overrides `context` on a per-key basis for that one relationship: the
    /// item's keys win on conflict, and any call-level keys the item doesn't
    /// specify are retained. For example, a call-level
    /// `{now: 42, region: "us"}` plus a per-item `{region: "eu"}` sends
    /// `{now: 42, region: "eu"}` for that item, while a sibling item with no
    /// per-item context still gets the untouched call-level default. Pass
    /// `None` for no call-level default (equivalent to
    /// [`check_permissions`](Self::check_permissions)).
    ///
    /// When neither a call-level nor a per-item context applies to a given
    /// item, no `context` field is set on that item's wire request — never
    /// an empty `Struct`.
    ///
    /// `CheckBulkPermissionsRequest` has no call-level context field on the
    /// wire (only `CheckBulkPermissionsRequestItem.context`, one per item),
    /// so `context` is fanned out onto every item at request-build time
    /// rather than sent once for the whole batch.
    pub async fn check_permissions_with_context(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
        context: Option<&HashMap<String, serde_json::Value>>,
    ) -> Result<Vec<CheckResult>, SpiceDBError> {
        self.check_permissions_impl(consistency, permission, relationships, context, None)
            .await
    }

    /// [`check_permissions`](Self::check_permissions) with a per-call
    /// timeout that overrides the client's `default_timeout` (seconds
    /// bounding this call). See root DESIGN.md, "RULE: A unary call must
    /// have a deadline".
    pub async fn check_permissions_with_timeout(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
        timeout: Duration,
    ) -> Result<Vec<CheckResult>, SpiceDBError> {
        self.check_permissions_impl(consistency, permission, relationships, None, Some(timeout))
            .await
    }

    /// Shared implementation behind [`check_permissions_with_context`](Self::check_permissions_with_context)
    /// and [`check_permissions_with_timeout`](Self::check_permissions_with_timeout).
    async fn check_permissions_impl(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
        context: Option<&HashMap<String, serde_json::Value>>,
        timeout: Option<Duration>,
    ) -> Result<Vec<CheckResult>, SpiceDBError> {
        if relationships.is_empty() {
            return Ok(Vec::new());
        }

        let timeout = self.effective_timeout(timeout);
        let mut all_results = Vec::with_capacity(relationships.len());

        for chunk in relationships.chunks(DEFAULT_CHECK_BATCH_SIZE) {
            let items = build_check_items(permission, chunk, context);

            let resp = self
                .retry(|| async {
                    let mut request = tonic::Request::new(proto::CheckBulkPermissionsRequest {
                        consistency: Some(consistency.to_proto()),
                        items: items.clone(),
                        with_tracing: false,
                    });
                    request.set_timeout(timeout);
                    self.proto
                        .permissions
                        .clone()
                        .check_bulk_permissions(request)
                        .await
                })
                .await?;

            let inner = resp.into_inner();

            // The proto guarantees pairs are returned in request order but says
            // nothing about count. A short response would otherwise silently
            // desync results[i] from relationships[i] for every item after the
            // gap -- one resource's answer attributed to another. Fail loudly
            // instead of returning a misaligned-but-"successful" Vec.
            if inner.pairs.len() != items.len() {
                return Err(error::internal(format!(
                    "check_bulk_permissions returned {} pair(s) for {} request item(s)",
                    inner.pairs.len(),
                    items.len()
                )));
            }

            let checked_at = inner.checked_at.map(|z| z.token).unwrap_or_default();
            for (i, pair) in inner.pairs.iter().enumerate() {
                match &pair.response {
                    Some(proto::check_bulk_permissions_pair::Response::Error(err_resp)) => {
                        // Route through the same mapper every other error path
                        // uses, instead of hardcoding a status — a per-item
                        // PERMISSION_DENIED must surface as PermissionDenied,
                        // not as a generic InvalidArgument.
                        //
                        // The item's own `google.rpc.Status` is re-encoded into
                        // the `grpc-status-details-bin` form a `tonic::Status`
                        // carries, so the item's `ErrorInfo` reaches the caller
                        // too: a per-item failure and an RPC-level failure must
                        // be indistinguishable except by the reason itself. See
                        // root DESIGN.md, "RULE: Error mapping must not lose the
                        // server's detail".
                        let message = format!("check item {}: {}", i, err_resp.message);
                        let details = prost::Message::encode_to_vec(err_resp);
                        return Err(error::from_grpc_status_with_message(
                            tonic::Status::with_details(
                                tonic::Code::from_i32(err_resp.code),
                                message.clone(),
                                details.into(),
                            ),
                            message,
                        ));
                    }
                    Some(proto::check_bulk_permissions_pair::Response::Item(item)) => {
                        all_results.push(check_result_from_bulk_item(item, &checked_at));
                    }
                    None => {
                        // The proto's `response` is a oneof — a well-behaved
                        // server always sets it to Item or Error, so this
                        // should be unreachable in practice. Silently
                        // skipping the index here would shrink all_results
                        // below relationships.len(), desyncing every
                        // subsequent results[i] from relationships[i] for
                        // the rest of the batch. Fail loudly instead of
                        // returning a misaligned-but-"successful" Vec.
                        return Err(error::internal(format!(
                            "check item {i}: malformed CheckBulkPermissionsPair (neither Item \
                             nor Error set)"
                        )));
                    }
                }
            }
        }

        Ok(all_results)
    }

    /// Returns `true` if **any** of the given relationships have the permission
    /// outright. A `ConditionalPermission` result does not count as granted —
    /// only results where [`CheckResult::has_permission`] is `true` are
    /// considered.
    pub async fn check_any(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
    ) -> Result<bool, SpiceDBError> {
        self.check_any_with_context(consistency, permission, relationships, None)
            .await
    }

    /// [`check_any`](Self::check_any) with a call-level default caveat
    /// context (see
    /// [`check_permissions_with_context`](Self::check_permissions_with_context)
    /// for the merge rule with any per-item context).
    pub async fn check_any_with_context(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
        context: Option<&HashMap<String, serde_json::Value>>,
    ) -> Result<bool, SpiceDBError> {
        let results = self
            .check_permissions_impl(consistency, permission, relationships, context, None)
            .await?;
        Ok(results.iter().any(CheckResult::has_permission))
    }

    /// [`check_any`](Self::check_any) with a per-call timeout that
    /// overrides the client's `default_timeout`.
    pub async fn check_any_with_timeout(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
        timeout: Duration,
    ) -> Result<bool, SpiceDBError> {
        let results = self
            .check_permissions_impl(consistency, permission, relationships, None, Some(timeout))
            .await?;
        Ok(results.iter().any(CheckResult::has_permission))
    }

    /// Returns `true` if **all** of the given relationships have the permission
    /// outright. A `ConditionalPermission` result does not count as granted —
    /// every result must satisfy [`CheckResult::has_permission`] for this to
    /// return `true`.
    pub async fn check_all(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
    ) -> Result<bool, SpiceDBError> {
        self.check_all_with_context(consistency, permission, relationships, None)
            .await
    }

    /// [`check_all`](Self::check_all) with a call-level default caveat
    /// context (see
    /// [`check_permissions_with_context`](Self::check_permissions_with_context)
    /// for the merge rule with any per-item context).
    ///
    /// Returns `false`, not the vacuous `true` that `Iterator::all` yields
    /// on an empty sequence, when `relationships` is empty -- "no checks to
    /// run" is not "all checks passed".
    pub async fn check_all_with_context(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
        context: Option<&HashMap<String, serde_json::Value>>,
    ) -> Result<bool, SpiceDBError> {
        if relationships.is_empty() {
            return Ok(false);
        }

        let results = self
            .check_permissions_impl(consistency, permission, relationships, context, None)
            .await?;
        Ok(results.iter().all(CheckResult::has_permission))
    }

    /// [`check_all`](Self::check_all) with a per-call timeout that
    /// overrides the client's `default_timeout`.
    ///
    /// Returns `false`, not the vacuous `true` an empty sequence would
    /// otherwise yield, when `relationships` is empty.
    pub async fn check_all_with_timeout(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
        timeout: Duration,
    ) -> Result<bool, SpiceDBError> {
        if relationships.is_empty() {
            return Ok(false);
        }

        let results = self
            .check_permissions_impl(consistency, permission, relationships, None, Some(timeout))
            .await?;
        Ok(results.iter().all(CheckResult::has_permission))
    }

    // -----------------------------------------------------------------------
    // Relationships
    // -----------------------------------------------------------------------

    /// Commits a transaction of relationship mutations to SpiceDB.
    ///
    /// Returns the revision (ZedToken) at which the write occurred.
    pub async fn write(&self, txn: &Transaction) -> Result<String, SpiceDBError> {
        self.write_impl(txn, None).await
    }

    /// [`write`](Self::write) with a per-call timeout that overrides the
    /// client's `default_timeout`.
    pub async fn write_with_timeout(
        &self,
        txn: &Transaction,
        timeout: Duration,
    ) -> Result<String, SpiceDBError> {
        self.write_impl(txn, Some(timeout)).await
    }

    async fn write_impl(
        &self,
        txn: &Transaction,
        timeout: Option<Duration>,
    ) -> Result<String, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let updates: Vec<proto::RelationshipUpdate> = txn
            .updates()
            .iter()
            .map(|(op, rel)| {
                let operation = match op {
                    UpdateOperation::Create => proto::relationship_update::Operation::Create as i32,
                    UpdateOperation::Touch => proto::relationship_update::Operation::Touch as i32,
                    UpdateOperation::Delete => proto::relationship_update::Operation::Delete as i32,
                    // Transaction's builder never produces Unspecified -- it is only ever
                    // produced by the watch stream for an operation this client does not
                    // recognize. Send it through as OPERATION_UNSPECIFIED so the server
                    // rejects the write, rather than guessing at a mutation the caller
                    // never asked for.
                    UpdateOperation::Unspecified => {
                        proto::relationship_update::Operation::Unspecified as i32
                    }
                };
                proto::RelationshipUpdate {
                    operation,
                    relationship: Some(rel.to_proto()),
                }
            })
            .collect();

        let preconditions = preconditions_to_proto(txn.preconditions())?;

        let resp = self
            .call_once(|| async {
                let mut request = tonic::Request::new(proto::WriteRelationshipsRequest {
                    updates: updates.clone(),
                    optional_preconditions: preconditions.clone(),
                    optional_transaction_metadata: None,
                });
                request.set_timeout(timeout);
                self.proto
                    .permissions
                    .clone()
                    .write_relationships(request)
                    .await
            })
            .await?;

        Ok(resp
            .into_inner()
            .written_at
            .map(|z| z.token)
            .unwrap_or_default())
    }

    /// Returns a stream of relationships matching the given filter.
    ///
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 relationships using the `AfterResultCursor`, lazily
    /// fetching the next page only once the current page has been consumed.
    ///
    /// # Streaming
    ///
    /// Returns `impl Stream<Item = Result<Relationship, SpiceDBError>>`. Items
    /// are yielded incrementally as they arrive from the server. Consume with
    /// [`StreamExt::next`](futures::StreamExt::next) (pin it first, e.g.
    /// `tokio::pin!`):
    ///
    /// ```no_run
    /// # use futures::StreamExt;
    /// # use spicedb::types::Filter;
    /// # async fn demo(client: spicedb::client::SpiceDBClient, filter: Filter) -> Result<(), spicedb::error::SpiceDBError> {
    /// let stream = client.read_relationships(&spicedb::consistency::full(), &filter);
    /// tokio::pin!(stream);
    /// while let Some(rel) = stream.next().await {
    ///     let rel = rel?;
    ///     // ...
    /// }
    /// # Ok(())
    /// # }
    /// ```
    pub fn read_relationships(
        &self,
        consistency: &Strategy,
        filter: &Filter,
    ) -> impl Stream<Item = Result<Relationship, SpiceDBError>> + 'static {
        let consistency = consistency.to_proto();
        let filter = filter.clone();
        let perms = self.proto.permissions.clone();

        async_stream::try_stream! {
            let filter = filter.to_proto()?;
            let mut cursor: Option<proto::Cursor> = None;

            loop {
                let resp_stream = retry_call(|| async {
                    perms
                        .clone()
                        .read_relationships(proto::ReadRelationshipsRequest {
                            consistency: Some(consistency.clone()),
                            relationship_filter: Some(filter.clone()),
                            optional_limit: DEFAULT_READ_PAGE_SIZE,
                            optional_cursor: cursor.clone(),
                        })
                        .await
                })
                .await?;

                let mut stream = resp_stream.into_inner();
                let mut count: u32 = 0;

                while let Some(resp) = stream
                    .message()
                    .await
                    .map_err(error::from_grpc_status)?
                {
                    count += 1;
                    if let Some(c) = resp.after_result_cursor {
                        cursor = Some(c);
                    }
                    if let Some(rel) = resp.relationship {
                        yield Relationship::from_proto(&rel);
                    }
                }

                // If we got fewer than the page size, we've read everything.
                if count < DEFAULT_READ_PAGE_SIZE {
                    break;
                }
            }
        }
    }

    /// Deletes all relationships matching the given filter.
    ///
    /// Large result sets are automatically paged in batches of 1,000. Repeats
    /// until the server reports all matching relationships are deleted.
    ///
    /// Returns the revision of the final deletion.
    ///
    /// This is a convenience wrapper around
    /// [`delete_relationships_with`](Self::delete_relationships_with) with
    /// [`DeleteOptions::default`] (no preconditions, default page size). To
    /// guard the delete with preconditions or override the page size, use
    /// `delete_relationships_with` directly.
    pub async fn delete_relationships(&self, filter: &Filter) -> Result<String, SpiceDBError> {
        self.delete_relationships_with(filter, &DeleteOptions::default())
            .await
    }

    /// Deletes all relationships matching the given filter, guarded by
    /// optional preconditions and/or an overridden page size.
    ///
    /// Large result sets are automatically paged — in batches of 1,000, or
    /// [`DeleteOptions::limit`] if set. Repeats until the server reports all
    /// matching relationships are deleted. Returns the revision of the final
    /// deletion.
    ///
    /// [`DeleteOptions::must_match`]/[`DeleteOptions::must_not_match`] add
    /// preconditions that guard the delete: if a precondition fails, the
    /// server rejects that call and deletes nothing for it. See
    /// [`DeleteOptions`]'s docs for how preconditions interact with paging.
    pub async fn delete_relationships_with(
        &self,
        filter: &Filter,
        options: &DeleteOptions,
    ) -> Result<String, SpiceDBError> {
        let preconditions = preconditions_to_proto(&options.preconditions())?;
        let filter = filter.to_proto()?;
        let limit = options.limit.unwrap_or(DEFAULT_DELETE_PAGE_SIZE);
        let timeout = self.effective_timeout(options.timeout);

        loop {
            let resp = self
                .call_once(|| async {
                    let mut request = tonic::Request::new(proto::DeleteRelationshipsRequest {
                        relationship_filter: Some(filter.clone()),
                        optional_limit: limit,
                        optional_allow_partial_deletions: true,
                        optional_preconditions: preconditions.clone(),
                        optional_transaction_metadata: None,
                    });
                    request.set_timeout(timeout);
                    self.proto
                        .permissions
                        .clone()
                        .delete_relationships(request)
                        .await
                })
                .await?;

            let inner = resp.into_inner();
            let revision = inner.deleted_at.map(|z| z.token).unwrap_or_default();

            // DeletionProgress::Complete = 1
            if inner.deletion_progress
                == proto::delete_relationships_response::DeletionProgress::Complete as i32
            {
                return Ok(revision);
            }
        }
    }

    // -----------------------------------------------------------------------
    // Lookups
    // -----------------------------------------------------------------------

    /// Returns a stream of resources that the subject has the given
    /// permission on. Each yielded [`LookupResource`] carries the
    /// permissionship (full grant vs conditional on caveat context) and, for
    /// conditional results, which caveat context was missing.
    ///
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 results, lazily fetching the next page only once the
    /// current page has been consumed.
    ///
    /// # Streaming
    ///
    /// Returns `impl Stream<Item = Result<LookupResource, SpiceDBError>>`.
    /// Items are yielded incrementally as they arrive from the server.
    /// Consume with [`StreamExt::next`](futures::StreamExt::next) (pin it
    /// first, e.g. `tokio::pin!`).
    pub fn lookup_resources(
        &self,
        consistency: &Strategy,
        resource_type: &str,
        permission: &str,
        subject_type: &str,
        subject_id: &str,
    ) -> impl Stream<Item = Result<LookupResource, SpiceDBError>> + 'static {
        let consistency = consistency.to_proto();
        let resource_type = resource_type.to_string();
        let permission = permission.to_string();
        let subject_type = subject_type.to_string();
        let subject_id = subject_id.to_string();
        let perms = self.proto.permissions.clone();

        async_stream::try_stream! {
            let mut cursor: Option<proto::Cursor> = None;

            loop {
                let resp_stream = retry_call(|| async {
                    perms
                        .clone()
                        .lookup_resources(proto::LookupResourcesRequest {
                            consistency: Some(consistency.clone()),
                            resource_object_type: resource_type.clone(),
                            permission: permission.clone(),
                            subject: Some(proto::SubjectReference {
                                object: Some(proto::ObjectReference {
                                    object_type: subject_type.clone(),
                                    object_id: subject_id.clone(),
                                }),
                                optional_relation: String::new(),
                            }),
                            optional_limit: DEFAULT_LOOKUP_PAGE_SIZE,
                            optional_cursor: cursor.clone(),
                            context: None,
                        })
                        .await
                })
                .await?;

                let mut stream = resp_stream.into_inner();
                let mut count: u32 = 0;

                while let Some(resp) = stream
                    .message()
                    .await
                    .map_err(error::from_grpc_status)?
                {
                    count += 1;
                    if let Some(c) = resp.after_result_cursor {
                        cursor = Some(c);
                    }
                    let permissionship = permissionship_from_proto(resp.permissionship);
                    let partial_caveat = partial_caveat_from_proto(resp.partial_caveat_info.as_ref());
                    let looked_up_at = resp.looked_up_at.map(|z| z.token).unwrap_or_default();
                    yield LookupResource {
                        resource_id: resp.resource_object_id,
                        permissionship,
                        partial_caveat,
                        looked_up_at,
                    };
                }

                if count < DEFAULT_LOOKUP_PAGE_SIZE {
                    break;
                }
            }
        }
    }

    /// Returns a stream of subjects that have the given permission on the
    /// resource.
    ///
    /// When a yielded [`LookupSubject::subject`] is the wildcard `"*"`, the
    /// server has granted the permission to every subject of `subject_type`
    /// EXCEPT those listed in [`LookupSubject::excluded_subjects`]. Callers
    /// MUST check `excluded_subjects` before treating a wildcard match as a
    /// blanket grant, or they risk granting access to subjects the server
    /// explicitly excluded.
    ///
    /// Unlike `lookup_resources`, `lookup_subjects` does not currently support
    /// cursor-based pagination in SpiceDB — all results stream in a single
    /// server-streaming call.
    ///
    /// # Streaming
    ///
    /// Returns `impl Stream<Item = Result<LookupSubject, SpiceDBError>>`.
    /// Items are yielded incrementally as they arrive from the server.
    /// Consume with [`StreamExt::next`](futures::StreamExt::next) (pin it
    /// first, e.g. `tokio::pin!`).
    pub fn lookup_subjects(
        &self,
        consistency: &Strategy,
        resource_type: &str,
        resource_id: &str,
        permission: &str,
        subject_type: &str,
    ) -> impl Stream<Item = Result<LookupSubject, SpiceDBError>> + 'static {
        let consistency = consistency.to_proto();
        let resource_type = resource_type.to_string();
        let resource_id = resource_id.to_string();
        let permission = permission.to_string();
        let subject_type = subject_type.to_string();
        let perms = self.proto.permissions.clone();

        async_stream::try_stream! {
            let resp_stream = retry_call(|| async {
                perms
                    .clone()
                    .lookup_subjects(proto::LookupSubjectsRequest {
                        consistency: Some(consistency.clone()),
                        resource: Some(proto::ObjectReference {
                            object_type: resource_type.clone(),
                            object_id: resource_id.clone(),
                        }),
                        permission: permission.clone(),
                        subject_object_type: subject_type.clone(),
                        optional_subject_relation: String::new(),
                        context: None,
                        optional_concrete_limit: 0,
                        optional_cursor: None,
                        wildcard_option: 0,
                    })
                    .await
            })
            .await?;

            let mut stream = resp_stream.into_inner();

            while let Some(resp) = stream
                .message()
                .await
                .map_err(error::from_grpc_status)?
            {
                // Prefer the nested subject field; fall back to the deprecated
                // top-level fields for servers that don't yet populate it.
                let primary_subject = resolved_subject_from_proto(resp.subject.as_ref());
                let subject = if primary_subject.subject_id.is_empty() {
                    #[allow(deprecated, clippy::let_and_return)]
                    let fallback = ResolvedSubject {
                        subject_id: resp.subject_object_id.clone(),
                        permissionship: permissionship_from_proto(resp.permissionship),
                        partial_caveat: partial_caveat_from_proto(resp.partial_caveat_info.as_ref()),
                    };
                    fallback
                } else {
                    primary_subject
                };

                // Prefer the nested excluded_subjects field (carries full
                // permissionship/caveat info); fall back to the deprecated
                // excluded_subject_ids field (IDs only) for servers that don't
                // yet populate it.
                let excluded_subjects: Vec<ResolvedSubject> = if !resp.excluded_subjects.is_empty()
                {
                    resp.excluded_subjects
                        .iter()
                        .map(|s| resolved_subject_from_proto(Some(s)))
                        .collect()
                } else {
                    #[allow(deprecated)]
                    let ids = resp.excluded_subject_ids.clone();
                    ids.into_iter()
                        .map(|id| ResolvedSubject {
                            subject_id: id,
                            permissionship: Permissionship::Unspecified,
                            partial_caveat: None,
                        })
                        .collect()
                };

                let looked_up_at = resp.looked_up_at.map(|z| z.token).unwrap_or_default();
                yield LookupSubject {
                    subject,
                    excluded_subjects,
                    looked_up_at,
                };
            }
        }
    }

    // -----------------------------------------------------------------------
    // Schema
    // -----------------------------------------------------------------------

    /// Returns the current SpiceDB schema and the revision at which it was read.
    pub async fn read_schema(&self) -> Result<(String, String), SpiceDBError> {
        self.read_schema_impl(None).await
    }

    /// [`read_schema`](Self::read_schema) with a per-call timeout that
    /// overrides the client's `default_timeout`.
    pub async fn read_schema_with_timeout(
        &self,
        timeout: Duration,
    ) -> Result<(String, String), SpiceDBError> {
        self.read_schema_impl(Some(timeout)).await
    }

    async fn read_schema_impl(
        &self,
        timeout: Option<Duration>,
    ) -> Result<(String, String), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::ReadSchemaRequest {});
                request.set_timeout(timeout);
                self.proto.schema.clone().read_schema(request).await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();
        Ok((inner.schema_text, revision))
    }

    /// Writes a new schema to SpiceDB.
    ///
    /// Returns the revision at which the schema was written.
    pub async fn write_schema(&self, schema: &str) -> Result<String, SpiceDBError> {
        self.write_schema_impl(schema, None).await
    }

    /// [`write_schema`](Self::write_schema) with a per-call timeout that
    /// overrides the client's `default_timeout`.
    pub async fn write_schema_with_timeout(
        &self,
        schema: &str,
        timeout: Duration,
    ) -> Result<String, SpiceDBError> {
        self.write_schema_impl(schema, Some(timeout)).await
    }

    async fn write_schema_impl(
        &self,
        schema: &str,
        timeout: Option<Duration>,
    ) -> Result<String, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .call_once(|| async {
                let mut request = tonic::Request::new(proto::WriteSchemaRequest {
                    schema: schema.to_string(),
                });
                request.set_timeout(timeout);
                self.proto.schema.clone().write_schema(request).await
            })
            .await?;

        Ok(resp
            .into_inner()
            .written_at
            .map(|z| z.token)
            .unwrap_or_default())
    }

    /// Returns the definitions and caveats in the current schema via reflection.
    pub async fn reflect_schema(
        &self,
        consistency: &Strategy,
    ) -> Result<ReflectSchemaResult, SpiceDBError> {
        self.reflect_schema_impl(consistency, None).await
    }

    /// [`reflect_schema`](Self::reflect_schema) with a per-call timeout
    /// that overrides the client's `default_timeout`.
    pub async fn reflect_schema_with_timeout(
        &self,
        consistency: &Strategy,
        timeout: Duration,
    ) -> Result<ReflectSchemaResult, SpiceDBError> {
        self.reflect_schema_impl(consistency, Some(timeout)).await
    }

    async fn reflect_schema_impl(
        &self,
        consistency: &Strategy,
        timeout: Option<Duration>,
    ) -> Result<ReflectSchemaResult, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::ReflectSchemaRequest {
                    consistency: Some(consistency.to_proto()),
                    optional_filters: Vec::new(),
                });
                request.set_timeout(timeout);
                self.proto.schema.clone().reflect_schema(request).await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();

        let definitions = inner
            .definitions
            .iter()
            .map(|def| SchemaDefinition {
                name: def.name.clone(),
                comment: def.comment.clone(),
                relations: def
                    .relations
                    .iter()
                    .map(|r| SchemaRelation {
                        name: r.name.clone(),
                        comment: r.comment.clone(),
                        parent_definition_name: r.parent_definition_name.clone(),
                    })
                    .collect(),
                permissions: def
                    .permissions
                    .iter()
                    .map(|p| SchemaPermission {
                        name: p.name.clone(),
                        comment: p.comment.clone(),
                        parent_definition_name: p.parent_definition_name.clone(),
                    })
                    .collect(),
            })
            .collect();

        let caveats = inner
            .caveats
            .iter()
            .map(|cav| SchemaCaveat {
                name: cav.name.clone(),
                comment: cav.comment.clone(),
                expression: cav.expression.clone(),
                parameters: cav
                    .parameters
                    .iter()
                    .map(|p| SchemaCaveatParameter {
                        name: p.name.clone(),
                        type_name: p.r#type.clone(),
                        parent_caveat_name: p.parent_caveat_name.clone(),
                    })
                    .collect(),
            })
            .collect();

        Ok(ReflectSchemaResult {
            definitions,
            caveats,
            revision,
        })
    }

    /// Returns the permissions that are computable for the given relation on a
    /// definition.
    pub async fn computable_permissions(
        &self,
        consistency: &Strategy,
        definition_name: &str,
        relation_name: &str,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        self.computable_permissions_impl(consistency, definition_name, relation_name, None)
            .await
    }

    /// [`computable_permissions`](Self::computable_permissions) with a
    /// per-call timeout that overrides the client's `default_timeout`.
    pub async fn computable_permissions_with_timeout(
        &self,
        consistency: &Strategy,
        definition_name: &str,
        relation_name: &str,
        timeout: Duration,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        self.computable_permissions_impl(consistency, definition_name, relation_name, Some(timeout))
            .await
    }

    async fn computable_permissions_impl(
        &self,
        consistency: &Strategy,
        definition_name: &str,
        relation_name: &str,
        timeout: Option<Duration>,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::ComputablePermissionsRequest {
                    consistency: Some(consistency.to_proto()),
                    definition_name: definition_name.to_string(),
                    relation_name: relation_name.to_string(),
                    optional_definition_name_filter: String::new(),
                });
                request.set_timeout(timeout);
                self.proto
                    .schema
                    .clone()
                    .computable_permissions(request)
                    .await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();

        let refs = inner
            .permissions
            .iter()
            .map(|perm| RelationReference {
                definition_name: perm.definition_name.clone(),
                relation_name: perm.relation_name.clone(),
                is_permission: perm.is_permission,
            })
            .collect();

        Ok((refs, revision))
    }

    /// Returns the relations that the given permission depends on.
    pub async fn dependent_relations(
        &self,
        consistency: &Strategy,
        definition_name: &str,
        permission_name: &str,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        self.dependent_relations_impl(consistency, definition_name, permission_name, None)
            .await
    }

    /// [`dependent_relations`](Self::dependent_relations) with a per-call
    /// timeout that overrides the client's `default_timeout`.
    pub async fn dependent_relations_with_timeout(
        &self,
        consistency: &Strategy,
        definition_name: &str,
        permission_name: &str,
        timeout: Duration,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        self.dependent_relations_impl(consistency, definition_name, permission_name, Some(timeout))
            .await
    }

    async fn dependent_relations_impl(
        &self,
        consistency: &Strategy,
        definition_name: &str,
        permission_name: &str,
        timeout: Option<Duration>,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::DependentRelationsRequest {
                    consistency: Some(consistency.to_proto()),
                    definition_name: definition_name.to_string(),
                    permission_name: permission_name.to_string(),
                });
                request.set_timeout(timeout);
                self.proto.schema.clone().dependent_relations(request).await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();

        let refs = inner
            .relations
            .iter()
            .map(|rel| RelationReference {
                definition_name: rel.definition_name.clone(),
                relation_name: rel.relation_name.clone(),
                is_permission: rel.is_permission,
            })
            .collect();

        Ok((refs, revision))
    }

    /// Compares the current schema against the given comparison schema, returning
    /// the differences.
    pub async fn diff_schema(
        &self,
        consistency: &Strategy,
        comparison_schema: &str,
    ) -> Result<(Vec<SchemaDiff>, String), SpiceDBError> {
        self.diff_schema_impl(consistency, comparison_schema, None)
            .await
    }

    /// [`diff_schema`](Self::diff_schema) with a per-call timeout that
    /// overrides the client's `default_timeout`.
    pub async fn diff_schema_with_timeout(
        &self,
        consistency: &Strategy,
        comparison_schema: &str,
        timeout: Duration,
    ) -> Result<(Vec<SchemaDiff>, String), SpiceDBError> {
        self.diff_schema_impl(consistency, comparison_schema, Some(timeout))
            .await
    }

    async fn diff_schema_impl(
        &self,
        consistency: &Strategy,
        comparison_schema: &str,
        timeout: Option<Duration>,
    ) -> Result<(Vec<SchemaDiff>, String), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::DiffSchemaRequest {
                    consistency: Some(consistency.to_proto()),
                    comparison_schema: comparison_schema.to_string(),
                });
                request.set_timeout(timeout);
                self.proto.schema.clone().diff_schema(request).await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();

        let diffs = inner.diffs.iter().map(schema_diff_from_proto).collect();

        Ok((diffs, revision))
    }

    // -----------------------------------------------------------------------
    // Expand
    // -----------------------------------------------------------------------

    /// Expands the permission tree for the given resource and permission,
    /// returning the full tree of subjects with access.
    pub async fn expand_permission_tree(
        &self,
        consistency: &Strategy,
        resource_type: &str,
        resource_id: &str,
        permission: &str,
    ) -> Result<ExpandResult, SpiceDBError> {
        self.expand_permission_tree_impl(consistency, resource_type, resource_id, permission, None)
            .await
    }

    /// [`expand_permission_tree`](Self::expand_permission_tree) with a
    /// per-call timeout that overrides the client's `default_timeout`.
    pub async fn expand_permission_tree_with_timeout(
        &self,
        consistency: &Strategy,
        resource_type: &str,
        resource_id: &str,
        permission: &str,
        timeout: Duration,
    ) -> Result<ExpandResult, SpiceDBError> {
        self.expand_permission_tree_impl(
            consistency,
            resource_type,
            resource_id,
            permission,
            Some(timeout),
        )
        .await
    }

    async fn expand_permission_tree_impl(
        &self,
        consistency: &Strategy,
        resource_type: &str,
        resource_id: &str,
        permission: &str,
        timeout: Option<Duration>,
    ) -> Result<ExpandResult, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::ExpandPermissionTreeRequest {
                    consistency: Some(consistency.to_proto()),
                    resource: Some(proto::ObjectReference {
                        object_type: resource_type.to_string(),
                        object_id: resource_id.to_string(),
                    }),
                    permission: permission.to_string(),
                });
                request.set_timeout(timeout);
                self.proto
                    .permissions
                    .clone()
                    .expand_permission_tree(request)
                    .await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.expanded_at.map(|z| z.token).unwrap_or_default();
        let tree = permission_tree_from_proto(inner.tree_root.as_ref());

        Ok(ExpandResult { tree, revision })
    }

    // -----------------------------------------------------------------------
    // Bulk
    // -----------------------------------------------------------------------

    /// Streams relationships to SpiceDB for bulk import.
    ///
    /// Accepts anything `IntoIterator<Item = Relationship>` -- a `Vec`, an
    /// array, or an arbitrary (possibly one-shot) iterator/generator -- and
    /// automatically batches into chunks of 1,000. The source is consumed
    /// lazily, one batch at a time, by a background task: unlike every other
    /// bulk/paginated RPC, `ImportBulkRelationships` is client-streaming, so
    /// the caller is the one producing an unbounded amount of data, and a
    /// caller streaming in millions of relationships from an iterator should
    /// never be forced to collect them into a `Vec` first just to call this.
    ///
    /// `ImportBulkRelationships` is client-streaming: its duration scales
    /// with the size of `relationships`, not with server latency, so unlike
    /// every other method on this client, this call is NOT bounded by
    /// `default_timeout` (root DESIGN.md, "RULE: A unary call must have a
    /// deadline", clause 3). It is unbounded; use
    /// [`import_relationships_with_timeout`](Self::import_relationships_with_timeout)
    /// to bound it explicitly.
    ///
    /// Returns the number of relationships loaded.
    pub async fn import_relationships<I>(&self, relationships: I) -> Result<u64, SpiceDBError>
    where
        I: IntoIterator<Item = Relationship> + Send + 'static,
        I::IntoIter: Send,
    {
        self.import_relationships_impl(relationships, None).await
    }

    /// [`import_relationships`](Self::import_relationships) with an explicit
    /// per-call timeout. There is no client default to override here (see
    /// [`import_relationships`](Self::import_relationships)) -- this is the
    /// only way to bound this call at all.
    pub async fn import_relationships_with_timeout<I>(
        &self,
        relationships: I,
        timeout: Duration,
    ) -> Result<u64, SpiceDBError>
    where
        I: IntoIterator<Item = Relationship> + Send + 'static,
        I::IntoIter: Send,
    {
        self.import_relationships_impl(relationships, Some(timeout))
            .await
    }

    async fn import_relationships_impl<I>(
        &self,
        relationships: I,
        timeout: Option<Duration>,
    ) -> Result<u64, SpiceDBError>
    where
        I: IntoIterator<Item = Relationship> + Send + 'static,
        I::IntoIter: Send,
    {
        // Deliberately NOT `self.effective_timeout(timeout)` -- no client
        // default applies here, only an explicit per-call `timeout`.
        let (tx, rx) = tokio::sync::mpsc::channel(4);

        // Spawn a task to batch and send relationships. Chunks the source
        // iterator manually (rather than `Vec::chunks`, which needs a
        // contiguous slice the caller may not have materialized) so only one
        // batch's worth is ever held in memory at a time, regardless of how
        // `relationships` is produced.
        let batch_task = tokio::spawn(async move {
            let mut iter = relationships.into_iter();
            loop {
                let chunk: Vec<proto::Relationship> = iter
                    .by_ref()
                    .take(DEFAULT_IMPORT_BATCH_SIZE)
                    .map(|r| r.to_proto())
                    .collect();
                if chunk.is_empty() {
                    break;
                }
                if tx
                    .send(proto::ImportBulkRelationshipsRequest {
                        relationships: chunk,
                    })
                    .await
                    .is_err()
                {
                    break;
                }
            }
        });

        let stream = tokio_stream::wrappers::ReceiverStream::new(rx);
        let mut request = tonic::Request::new(stream);
        if let Some(t) = timeout {
            request.set_timeout(t);
        }
        let resp = self
            .proto
            .permissions
            .clone()
            .import_bulk_relationships(request)
            .await
            .map_err(error::from_grpc_status)?;

        batch_task.await.ok();
        Ok(resp.into_inner().num_loaded)
    }

    /// Returns a stream of all relationships matching the optional filter,
    /// streamed from SpiceDB in bulk.
    ///
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 relationships, lazily fetching the next page only once the
    /// current page has been consumed.
    ///
    /// # Streaming
    ///
    /// Returns `impl Stream<Item = Result<Relationship, SpiceDBError>>`. Items
    /// are yielded incrementally as they arrive from the server. Consume with
    /// [`StreamExt::next`](futures::StreamExt::next) (pin it first, e.g.
    /// `tokio::pin!`).
    pub fn export_relationships(
        &self,
        consistency: &Strategy,
        filter: Option<&Filter>,
    ) -> impl Stream<Item = Result<Relationship, SpiceDBError>> + 'static {
        let consistency = consistency.to_proto();
        let filter = filter.cloned();
        let perms = self.proto.permissions.clone();

        async_stream::try_stream! {
            let filter = filter.map(|f| f.to_proto()).transpose()?;
            let mut cursor: Option<proto::Cursor> = None;

            loop {
                let req = proto::ExportBulkRelationshipsRequest {
                    consistency: Some(consistency.clone()),
                    optional_limit: DEFAULT_EXPORT_PAGE_SIZE,
                    optional_cursor: cursor.clone(),
                    optional_relationship_filter: filter.clone(),
                };

                let resp_stream = retry_call(|| async {
                    perms.clone().export_bulk_relationships(req.clone()).await
                })
                .await?;

                let mut stream = resp_stream.into_inner();
                let mut page_count: u32 = 0;

                while let Some(resp) = stream
                    .message()
                    .await
                    .map_err(error::from_grpc_status)?
                {
                    if let Some(c) = resp.after_result_cursor {
                        cursor = Some(c);
                    }
                    for rel in &resp.relationships {
                        page_count += 1;
                        yield Relationship::from_proto(rel);
                    }
                }

                if page_count < DEFAULT_EXPORT_PAGE_SIZE {
                    break;
                }
            }
        }
    }

    // -----------------------------------------------------------------------
    // Watch
    // -----------------------------------------------------------------------

    /// Returns a stream of watch events from SpiceDB's watch API, starting
    /// from head. Each yielded [`WatchEvent`] corresponds to one server
    /// response: zero or more relationship updates, all current through
    /// [`WatchEvent::changes_through`].
    ///
    /// Watch is an open-ended server stream: events are yielded incrementally,
    /// as they occur, and the stream stays open indefinitely (until the caller
    /// drops it or the connection fails). Consume it with
    /// [`StreamExt::next`](futures::StreamExt::next):
    ///
    /// ```no_run
    /// # use futures::StreamExt;
    /// # async fn demo(client: spicedb::client::SpiceDBClient) -> Result<(), spicedb::error::SpiceDBError> {
    /// let object_types = vec!["document".to_string()];
    /// let stream = client.updates(&object_types);
    /// tokio::pin!(stream);
    /// while let Some(event) = stream.next().await {
    ///     let event = event?;
    ///     for update in &event.updates {
    ///         // handle update.operation / update.relationship
    ///     }
    /// }
    /// # Ok(())
    /// # }
    /// ```
    ///
    /// [`WatchEvent::changes_through`] is always populated -- pass it to
    /// [`WatchOptions::with_start_revision`] on a later
    /// [`updates_with`](Self::updates_with) call to resume after a dropped
    /// stream, instead of restarting from the original revision
    /// (reprocessing everything since, possibly past the GC window) or from
    /// head (silently losing every change in the gap).
    ///
    /// This is a convenience wrapper around
    /// [`updates_with`](Self::updates_with) with
    /// [`WatchOptions::default`] (from head, no checkpoints). To resume from
    /// a revision or request checkpoints, use `updates_with` directly.
    pub fn updates(
        &self,
        object_types: &[String],
    ) -> impl Stream<Item = Result<WatchEvent, SpiceDBError>> + 'static {
        self.updates_with(object_types, &WatchOptions::default())
    }

    /// Returns a stream of watch events, resuming from a revision and/or
    /// requesting checkpoints.
    ///
    /// See [`updates`](Self::updates) for the stream's shape and lifetime;
    /// this differs only in taking [`WatchOptions`]:
    ///
    /// ```no_run
    /// # use futures::StreamExt;
    /// # use spicedb::types::WatchOptions;
    /// # async fn demo(client: spicedb::client::SpiceDBClient, resume_from: &str) -> Result<(), spicedb::error::SpiceDBError> {
    /// let object_types = vec!["document".to_string()];
    /// let stream = client.updates_with(
    ///     &object_types,
    ///     &WatchOptions::new()
    ///         .with_start_revision(resume_from)
    ///         .with_checkpoints(),
    /// );
    /// # tokio::pin!(stream);
    /// # while let Some(event) = stream.next().await { let _ = event?; }
    /// # Ok(())
    /// # }
    /// ```
    ///
    /// [`WatchOptions::with_checkpoints`] requests periodic checkpoint events
    /// ([`WatchEvent::is_checkpoint`], no updates) in addition to
    /// relationship updates. Recommended if this SpiceDB instance is
    /// running behind a proxy that aborts idle connections, since a
    /// checkpoint keeps the stream alive even when there are no changes.
    pub fn updates_with(
        &self,
        object_types: &[String],
        options: &WatchOptions,
    ) -> impl Stream<Item = Result<WatchEvent, SpiceDBError>> + 'static {
        // Build the request and clone the watch client up front so the returned
        // stream owns everything it needs and borrows nothing from `self` or the
        // arguments (it is therefore `'static`).
        let mut req = proto::WatchRequest {
            optional_object_types: object_types.to_vec(),
            optional_relationship_filters: Vec::new(),
            optional_update_kinds: Vec::new(),
            optional_start_cursor: None,
        };
        if let Some(rev) = options.start_revision.as_deref() {
            req.optional_start_cursor = Some(proto::ZedToken {
                token: rev.to_string(),
            });
        }
        if options.include_checkpoints {
            // optional_update_kinds is empty-means-default (relationship updates only, for
            // backwards compatibility) -- a non-empty list is the exact set requested, so
            // asking for checkpoints must also spell out relationship updates or the server
            // would stop sending them.
            req.optional_update_kinds = vec![
                proto::WatchKind::IncludeRelationshipUpdates as i32,
                proto::WatchKind::IncludeCheckpoints as i32,
            ];
        }
        let watch = self.proto.watch.clone();

        async_stream::try_stream! {
            let resp_stream = retry_call(|| async { watch.clone().watch(req.clone()).await }).await?;

            let mut stream = resp_stream.into_inner();

            while let Some(resp) = stream
                .message()
                .await
                .map_err(error::from_grpc_status)?
            {
                let mut updates = Vec::with_capacity(resp.updates.len());
                for update in &resp.updates {
                    // Server-supplied data: an unrecognized operation
                    // (OPERATION_UNSPECIFIED, or a future wire value added after this
                    // client shipped) MUST NOT map to a write, and MUST NOT be dropped.
                    // Root DESIGN.md, "RULE: A conversion that cannot preserve meaning
                    // must fail", clause 2: server-supplied values the client does not
                    // recognise MUST NOT raise, and MUST map to the safe, non-permissive
                    // default. Dropping the update here would silently swallow the whole
                    // event, so a consumer mirroring the stream would miss a change it had
                    // no way to learn about -- worse than a mapping it can inspect and act
                    // on.
                    let operation = match update.operation {
                        x if x == proto::relationship_update::Operation::Create as i32 => {
                            UpdateOperation::Create
                        }
                        x if x == proto::relationship_update::Operation::Touch as i32 => {
                            UpdateOperation::Touch
                        }
                        x if x == proto::relationship_update::Operation::Delete as i32 => {
                            UpdateOperation::Delete
                        }
                        _ => UpdateOperation::Unspecified,
                    };
                    if let Some(rel) = &update.relationship {
                        updates.push(Update {
                            operation,
                            relationship: Relationship::from_proto(rel),
                        });
                    }
                }
                yield WatchEvent {
                    updates,
                    changes_through: resp.changes_through.map(|z| z.token).unwrap_or_default(),
                    is_checkpoint: resp.is_checkpoint,
                };
            }
        }
    }

    // -----------------------------------------------------------------------
    // Experimental
    // -----------------------------------------------------------------------

    /// Registers a named counter that tracks relationships matching the given
    /// filter. The counter is computed asynchronously by SpiceDB.
    ///
    /// # Experimental
    ///
    /// This API is experimental and may change without following the backwards
    /// compatibility mandate.
    pub async fn experimental_register_relationship_counter(
        &self,
        name: &str,
        filter: &Filter,
    ) -> Result<(), SpiceDBError> {
        self.experimental_register_relationship_counter_impl(name, filter, None)
            .await
    }

    /// [`experimental_register_relationship_counter`](Self::experimental_register_relationship_counter)
    /// with a per-call timeout that overrides the client's `default_timeout`.
    pub async fn experimental_register_relationship_counter_with_timeout(
        &self,
        name: &str,
        filter: &Filter,
        timeout: Duration,
    ) -> Result<(), SpiceDBError> {
        self.experimental_register_relationship_counter_impl(name, filter, Some(timeout))
            .await
    }

    async fn experimental_register_relationship_counter_impl(
        &self,
        name: &str,
        filter: &Filter,
        timeout: Option<Duration>,
    ) -> Result<(), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let filter = filter.to_proto()?;
        self.call_once(|| async {
            let mut request =
                tonic::Request::new(proto::ExperimentalRegisterRelationshipCounterRequest {
                    name: name.to_string(),
                    relationship_filter: Some(filter.clone()),
                });
            request.set_timeout(timeout);
            self.proto
                .experimental
                .clone()
                .experimental_register_relationship_counter(request)
                .await
        })
        .await?;

        Ok(())
    }

    /// Reads the value of a previously registered relationship counter.
    ///
    /// Returns `Ok(Some(result))` if the count is ready, or `Ok(None)` if the
    /// counter is still being calculated.
    ///
    /// # Experimental
    ///
    /// This API is experimental and may change without following the backwards
    /// compatibility mandate.
    pub async fn experimental_count_relationships(
        &self,
        name: &str,
    ) -> Result<Option<CountResult>, SpiceDBError> {
        self.experimental_count_relationships_impl(name, None).await
    }

    /// [`experimental_count_relationships`](Self::experimental_count_relationships)
    /// with a per-call timeout that overrides the client's `default_timeout`.
    pub async fn experimental_count_relationships_with_timeout(
        &self,
        name: &str,
        timeout: Duration,
    ) -> Result<Option<CountResult>, SpiceDBError> {
        self.experimental_count_relationships_impl(name, Some(timeout))
            .await
    }

    async fn experimental_count_relationships_impl(
        &self,
        name: &str,
        timeout: Option<Duration>,
    ) -> Result<Option<CountResult>, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request =
                    tonic::Request::new(proto::ExperimentalCountRelationshipsRequest {
                        name: name.to_string(),
                    });
                request.set_timeout(timeout);
                self.proto
                    .experimental
                    .clone()
                    .experimental_count_relationships(request)
                    .await
            })
            .await?;

        let inner = resp.into_inner();

        match inner.counter_result {
            Some(
                proto::experimental_count_relationships_response::CounterResult::CounterStillCalculating(
                    _,
                ),
            ) => Ok(None),
            Some(
                proto::experimental_count_relationships_response::CounterResult::ReadCounterValue(
                    cv,
                ),
            ) => {
                let revision = cv.read_at.map(|z| z.token).unwrap_or_default();
                Ok(Some(CountResult {
                    relationship_count: cv.relationship_count,
                    revision,
                }))
            }
            None => Ok(None),
        }
    }

    /// Removes a previously registered relationship counter.
    ///
    /// # Experimental
    ///
    /// This API is experimental and may change without following the backwards
    /// compatibility mandate.
    pub async fn experimental_unregister_relationship_counter(
        &self,
        name: &str,
    ) -> Result<(), SpiceDBError> {
        self.experimental_unregister_relationship_counter_impl(name, None)
            .await
    }

    /// [`experimental_unregister_relationship_counter`](Self::experimental_unregister_relationship_counter)
    /// with a per-call timeout that overrides the client's `default_timeout`.
    pub async fn experimental_unregister_relationship_counter_with_timeout(
        &self,
        name: &str,
        timeout: Duration,
    ) -> Result<(), SpiceDBError> {
        self.experimental_unregister_relationship_counter_impl(name, Some(timeout))
            .await
    }

    async fn experimental_unregister_relationship_counter_impl(
        &self,
        name: &str,
        timeout: Option<Duration>,
    ) -> Result<(), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        self.call_once(|| async {
            let mut request =
                tonic::Request::new(proto::ExperimentalUnregisterRelationshipCounterRequest {
                    name: name.to_string(),
                });
            request.set_timeout(timeout);
            self.proto
                .experimental
                .clone()
                .experimental_unregister_relationship_counter(request)
                .await
        })
        .await?;

        Ok(())
    }

    // -----------------------------------------------------------------------
    // Retry with exponential backoff
    // -----------------------------------------------------------------------

    /// Retries a gRPC call with full-jitter exponential backoff for transient
    /// errors.
    ///
    /// Only for idempotent (read) calls -- see [`SpiceDBClient::call_once`]
    /// for mutations.
    ///
    /// Retries on UNAVAILABLE and ABORTED up to MAX_RETRIES times with
    /// exponentially increasing delay caps starting at BASE_RETRY_DELAY_MS.
    async fn retry<F, Fut, T>(&self, f: F) -> Result<tonic::Response<T>, SpiceDBError>
    where
        F: Fn() -> Fut,
        Fut: std::future::Future<Output = Result<tonic::Response<T>, tonic::Status>>,
    {
        retry_call(f).await
    }

    /// Calls `f` once, converting a `tonic::Status` error, but never
    /// retrying.
    ///
    /// For mutations. A `WriteRelationships` containing `OPERATION_CREATE`,
    /// or any request carrying preconditions, is not idempotent: if it
    /// commits and the response is lost (a rolling restart, a proxy dropping
    /// the connection), a retry would surface `ALREADY_EXISTS`/
    /// `FAILED_PRECONDITION` for a write that in fact succeeded, and the
    /// caller would wrongly conclude it had failed. See DESIGN.md,
    /// "Automatic retry is for idempotent operations only".
    async fn call_once<F, Fut, T>(&self, f: F) -> Result<tonic::Response<T>, SpiceDBError>
    where
        F: Fn() -> Fut,
        Fut: std::future::Future<Output = Result<tonic::Response<T>, tonic::Status>>,
    {
        f().await.map_err(error::from_grpc_status)
    }
}

/// Full-jitter backoff delay in milliseconds for retry attempt `attempt`:
/// `uniform(0, cap)` rather than the fixed `cap`. Plain exponential backoff
/// has every client in a fleet retry on the same schedule after a server
/// restart, turning the recovery into a thundering herd; sampling uniformly
/// under the cap spreads retries out instead.
fn jittered_backoff_ms(attempt: u32) -> u64 {
    let cap_ms = (BASE_RETRY_DELAY_MS * 2u64.pow(attempt)).min(5000);
    (rand::random::<f64>() * cap_ms as f64) as u64
}

/// Retries a gRPC call with full-jitter exponential backoff for transient
/// errors.
///
/// Free-function twin of [`SpiceDBClient::retry`] that does not borrow `self`,
/// so it can be used from inside `+ 'static` streams (which own a cloned
/// service client rather than borrowing the `SpiceDBClient`).
///
/// Retries on UNAVAILABLE and ABORTED up to MAX_RETRIES times with
/// exponentially increasing delay caps starting at BASE_RETRY_DELAY_MS,
/// capped at 5s.
pub(crate) async fn retry_call<F, Fut, T>(f: F) -> Result<tonic::Response<T>, SpiceDBError>
where
    F: Fn() -> Fut,
    Fut: std::future::Future<Output = Result<tonic::Response<T>, tonic::Status>>,
{
    let mut attempt = 0u32;
    loop {
        match f().await {
            Ok(resp) => return Ok(resp),
            Err(status) => {
                let err = error::from_grpc_status(status);
                if !error::is_transient(&err) || attempt >= MAX_RETRIES {
                    return Err(err);
                }
                let delay_ms = jittered_backoff_ms(attempt);
                tokio::time::sleep(tokio::time::Duration::from_millis(delay_ms)).await;
                attempt += 1;
            }
        }
    }
}

/// Builds the `CheckBulkPermissionsRequestItem`s for a chunk of
/// relationships, merging call-level and per-item check-time caveat context
/// per the key-level merge rule (see
/// [`SpiceDBClient::check_permissions_with_context`]). A free function (no
/// `&self` needed) so it's directly unit-testable without a mock server.
fn build_check_items(
    permission: &str,
    relationships: &[Relationship],
    call_level_context: Option<&HashMap<String, serde_json::Value>>,
) -> Vec<proto::CheckBulkPermissionsRequestItem> {
    relationships
        .iter()
        .map(|r| {
            let merged = merge_check_context(call_level_context, r.check_context.as_ref());
            proto::CheckBulkPermissionsRequestItem {
                resource: Some(proto::ObjectReference {
                    object_type: r.resource_type.clone(),
                    object_id: r.resource_id.clone(),
                }),
                permission: permission.to_string(),
                subject: Some(proto::SubjectReference {
                    object: Some(proto::ObjectReference {
                        object_type: r.subject_type.clone(),
                        object_id: r.subject_id.clone(),
                    }),
                    optional_relation: r.subject_relation.clone(),
                }),
                context: merged.as_ref().map(check_context_to_proto),
            }
        })
        .collect()
}

/// Converts idiomatic [`Precondition`]s (shared by [`Transaction`] and
/// [`DeleteOptions`]) into their proto representation.
///
/// Returns an error if any precondition's filter cannot be converted -- see
/// [`Filter::to_proto`].
fn preconditions_to_proto(
    preconditions: &[Precondition],
) -> Result<Vec<proto::Precondition>, SpiceDBError> {
    preconditions
        .iter()
        .map(|pc| {
            let operation = match pc.operation {
                PreconditionOperation::MustNotMatch => {
                    proto::precondition::Operation::MustNotMatch as i32
                }
                PreconditionOperation::MustMatch => {
                    proto::precondition::Operation::MustMatch as i32
                }
            };
            Ok(proto::Precondition {
                operation,
                filter: Some(pc.filter.to_proto()?),
            })
        })
        .collect()
}

/// Converts a proto ReflectionSchemaDiff into an idiomatic SchemaDiff.
fn schema_diff_from_proto(d: &proto::ReflectionSchemaDiff) -> SchemaDiff {
    if let Some(diff) = &d.diff {
        use proto::reflection_schema_diff::Diff;
        match diff {
            Diff::DefinitionAdded(def) => SchemaDiff {
                kind: "definition_added".into(),
                definition_name: def.name.clone(),
                ..Default::default()
            },
            Diff::DefinitionRemoved(def) => SchemaDiff {
                kind: "definition_removed".into(),
                definition_name: def.name.clone(),
                ..Default::default()
            },
            Diff::DefinitionDocCommentChanged(def) => SchemaDiff {
                kind: "definition_doc_comment_changed".into(),
                definition_name: def.name.clone(),
                ..Default::default()
            },
            Diff::RelationAdded(rel) => SchemaDiff {
                kind: "relation_added".into(),
                definition_name: rel.parent_definition_name.clone(),
                relation_name: rel.name.clone(),
                ..Default::default()
            },
            Diff::RelationRemoved(rel) => SchemaDiff {
                kind: "relation_removed".into(),
                definition_name: rel.parent_definition_name.clone(),
                relation_name: rel.name.clone(),
                ..Default::default()
            },
            Diff::RelationDocCommentChanged(rel) => SchemaDiff {
                kind: "relation_doc_comment_changed".into(),
                definition_name: rel.parent_definition_name.clone(),
                relation_name: rel.name.clone(),
                ..Default::default()
            },
            Diff::RelationSubjectTypeAdded(change) => {
                let rel = change.relation.as_ref();
                SchemaDiff {
                    kind: "relation_subject_type_added".into(),
                    definition_name: rel
                        .map(|r| r.parent_definition_name.clone())
                        .unwrap_or_default(),
                    relation_name: rel.map(|r| r.name.clone()).unwrap_or_default(),
                    ..Default::default()
                }
            }
            Diff::RelationSubjectTypeRemoved(change) => {
                let rel = change.relation.as_ref();
                SchemaDiff {
                    kind: "relation_subject_type_removed".into(),
                    definition_name: rel
                        .map(|r| r.parent_definition_name.clone())
                        .unwrap_or_default(),
                    relation_name: rel.map(|r| r.name.clone()).unwrap_or_default(),
                    ..Default::default()
                }
            }
            Diff::PermissionAdded(perm) => SchemaDiff {
                kind: "permission_added".into(),
                definition_name: perm.parent_definition_name.clone(),
                permission_name: perm.name.clone(),
                ..Default::default()
            },
            Diff::PermissionRemoved(perm) => SchemaDiff {
                kind: "permission_removed".into(),
                definition_name: perm.parent_definition_name.clone(),
                permission_name: perm.name.clone(),
                ..Default::default()
            },
            Diff::PermissionDocCommentChanged(perm) => SchemaDiff {
                kind: "permission_doc_comment_changed".into(),
                definition_name: perm.parent_definition_name.clone(),
                permission_name: perm.name.clone(),
                ..Default::default()
            },
            Diff::PermissionExprChanged(perm) => SchemaDiff {
                kind: "permission_expr_changed".into(),
                definition_name: perm.parent_definition_name.clone(),
                permission_name: perm.name.clone(),
                ..Default::default()
            },
            Diff::CaveatAdded(cav) => SchemaDiff {
                kind: "caveat_added".into(),
                caveat_name: cav.name.clone(),
                ..Default::default()
            },
            Diff::CaveatRemoved(cav) => SchemaDiff {
                kind: "caveat_removed".into(),
                caveat_name: cav.name.clone(),
                ..Default::default()
            },
            Diff::CaveatDocCommentChanged(cav) => SchemaDiff {
                kind: "caveat_doc_comment_changed".into(),
                caveat_name: cav.name.clone(),
                ..Default::default()
            },
            Diff::CaveatExprChanged(cav) => SchemaDiff {
                kind: "caveat_expr_changed".into(),
                caveat_name: cav.name.clone(),
                ..Default::default()
            },
            Diff::CaveatParameterAdded(param) => SchemaDiff {
                kind: "caveat_parameter_added".into(),
                caveat_name: param.parent_caveat_name.clone(),
                ..Default::default()
            },
            Diff::CaveatParameterRemoved(param) => SchemaDiff {
                kind: "caveat_parameter_removed".into(),
                caveat_name: param.parent_caveat_name.clone(),
                ..Default::default()
            },
            Diff::CaveatParameterTypeChanged(change) => {
                let param = change.parameter.as_ref();
                SchemaDiff {
                    kind: "caveat_parameter_type_changed".into(),
                    caveat_name: param
                        .map(|p| p.parent_caveat_name.clone())
                        .unwrap_or_default(),
                    ..Default::default()
                }
            }
        }
    } else {
        SchemaDiff {
            kind: "unknown".into(),
            ..Default::default()
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Note: constructor tests are removed because SpiceDBProtoClient::new
    // now attempts a real gRPC connection, which will fail in unit tests
    // without a running SpiceDB server.

    #[test]
    fn test_page_size_constants() {
        assert_eq!(DEFAULT_READ_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_LOOKUP_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_EXPORT_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_DELETE_PAGE_SIZE, 1_000);
        assert_eq!(DEFAULT_CHECK_BATCH_SIZE, 1_000);
        assert_eq!(DEFAULT_IMPORT_BATCH_SIZE, 1_000);
    }

    #[test]
    fn test_retry_constants() {
        assert_eq!(MAX_RETRIES, 3);
        assert_eq!(BASE_RETRY_DELAY_MS, 100);
    }

    /// Backoff must vary between runs -- assert the jitter exists, not an
    /// exact value. Without jitter every client in a fleet retries on the
    /// same schedule after a server restart (thundering herd).
    #[test]
    fn test_jittered_backoff_ms_varies_between_calls() {
        let cap_ms = (BASE_RETRY_DELAY_MS * 2u64.pow(2)).min(5000);
        let seen: std::collections::HashSet<u64> =
            (0..50).map(|_| jittered_backoff_ms(2)).collect();
        assert!(seen.len() > 1, "backoff should vary between calls");
        for v in seen {
            assert!(v <= cap_ms, "backoff {v} exceeded the cap {cap_ms}");
        }
    }

    // Pure-function coverage of build_check_items, complementing the
    // mock-server-backed C1-C4 tests in tests/check_context_test.rs: same
    // merge rule, exercised directly on the built proto items with no
    // network involved.
    #[test]
    fn test_build_check_items_merge_rule() {
        let sibling = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();
        let mut item_ctx = HashMap::new();
        item_ctx.insert("region".to_string(), serde_json::json!("eu"));
        let overridden = Relationship::new("document", "2", "view", "user", "bob", "")
            .unwrap()
            .with_check_context(item_ctx);

        let mut call_level = HashMap::new();
        call_level.insert("now".to_string(), serde_json::json!(42.0));
        call_level.insert("region".to_string(), serde_json::json!("us"));

        let items = build_check_items("view", &[sibling, overridden], Some(&call_level));
        assert_eq!(items.len(), 2);

        let sibling_ctx = items[0]
            .context
            .as_ref()
            .expect("sibling should have context");
        assert_eq!(sibling_ctx.fields.len(), 2);
        assert_eq!(
            sibling_ctx.fields.get("now").and_then(|v| v.kind.clone()),
            Some(prost_types::value::Kind::NumberValue(42.0))
        );
        assert_eq!(
            sibling_ctx
                .fields
                .get("region")
                .and_then(|v| v.kind.clone()),
            Some(prost_types::value::Kind::StringValue("us".to_string()))
        );

        let overridden_ctx = items[1]
            .context
            .as_ref()
            .expect("overridden item should have context");
        assert_eq!(
            overridden_ctx
                .fields
                .get("region")
                .and_then(|v| v.kind.clone()),
            Some(prost_types::value::Kind::StringValue("eu".to_string())),
            "item's region key must win over the call-level default"
        );
        assert_eq!(
            overridden_ctx
                .fields
                .get("now")
                .and_then(|v| v.kind.clone()),
            Some(prost_types::value::Kind::NumberValue(42.0)),
            "call-level now key (absent from the item) must be retained"
        );
    }

    #[test]
    fn test_build_check_items_neither_supplied_sets_no_context() {
        let r = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();
        let items = build_check_items("view", &[r], None);
        assert_eq!(items.len(), 1);
        assert!(items[0].context.is_none());
    }
}
