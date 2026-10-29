//! Canonical request-shaped operations. Every future and stream owns its transport handle.
use super::*;
use crate::options::{CallOptions, Operation, RetryEvent, RetryPolicy};
use futures::StreamExt;
use std::future::Future;
use std::sync::Arc;
use tokio::time::Instant;

/// What one operation runs under: its total budget, transport escape hatch
/// and the client's retry policy. Built when the operation is called; the
/// deadline starts when execution begins.
#[derive(Clone)]
pub(crate) struct Plan {
    timeout: Option<Duration>,
    call: CallOptions,
    retry: Option<Arc<RetryPolicy>>,
    stream_retries: bool,
    // The client's per-operation policy, consulted by mutations.
    operation_retry: Option<Arc<RetryPolicy>>,
}

/// [`Plan`] with its deadline started, in scope for every RPC the operation
/// makes, so retries, chunks and pages share one budget.
#[derive(Clone)]
struct Exec {
    deadline: Option<Instant>,
    call: CallOptions,
    retry: Option<Arc<RetryPolicy>>,
    stream_retries: bool,
    // The client's per-operation policy, consulted by mutations.
    operation_retry: Option<Arc<RetryPolicy>>,
}

tokio::task_local! { static EXEC: Exec; }

impl Plan {
    fn start(&self) -> Result<Exec, SpiceDBError> {
        if let Some(policy) = &self.call.retry {
            policy.validate()?;
        }
        Ok(Exec {
            deadline: deadline(self.timeout)?,
            call: self.call.clone(),
            retry: self.retry.clone(),
            stream_retries: self.stream_retries,
            operation_retry: self.operation_retry.clone(),
        })
    }
}

impl SpiceDBClient {
    /// The most specific retry policy wins: the per-call policy, then the
    /// client's policy for this operation, then the client-wide policy. A
    /// per-call or per-operation policy also governs stream establishment.
    ///
    /// The client-wide policy (and its stream-retry mode) never reopens a
    /// watch: a watch reopened without a start revision begins at a later
    /// revision and silently skips the changes in between. Only a per-call or
    /// per-operation policy opts a watch in to reopening before its first
    /// event.
    pub(crate) fn plan(
        &self,
        operation: Operation,
        timeout: Option<Duration>,
        call: &CallOptions,
    ) -> Plan {
        let operation_retry = self.operation_retry.get(&operation).cloned();
        let is_watch = matches!(
            operation,
            Operation::Updates | Operation::WatchPermissionSets
        );
        let (retry, stream_retries) = match (&call.retry, &operation_retry) {
            (Some(policy), _) => (Some(Arc::new(policy.clone())), true),
            (None, Some(policy)) => (Some(policy.clone()), true),
            (None, None) => (
                self.retry.clone(),
                !is_watch && self.stream_retries == crate::options::StreamRetryMode::Enabled,
            ),
        };
        Plan {
            timeout,
            call: call.clone(),
            retry,
            stream_retries,
            operation_retry,
        }
    }
}

fn deadline(timeout: Option<Duration>) -> Result<Option<Instant>, SpiceDBError> {
    timeout
        .map(|t| {
            if t.is_zero() {
                Err(invalid("timeout", "must be positive"))
            } else {
                Instant::now()
                    .checked_add(t)
                    .ok_or_else(|| invalid("timeout", "out of range"))
            }
        })
        .transpose()
}
fn elapsed() -> SpiceDBError {
    SpiceDBError::DeadlineExceeded("total operation deadline exceeded".into())
}
async fn bounded<T>(
    plan: Plan,
    future: impl Future<Output = Result<T, SpiceDBError>>,
) -> Result<T, SpiceDBError> {
    let exec = plan.start()?;
    let end = exec.deadline;
    EXEC.scope(exec, async move {
        match end {
            Some(end) => tokio::time::timeout_at(end, future)
                .await
                .map_err(|_| elapsed())?,
            None => future.await,
        }
    })
    .await
}
fn stream_budget<T: Send + 'static>(
    plan: Plan,
    stream: impl Stream<Item = Result<T, SpiceDBError>> + Send + 'static,
) -> impl Stream<Item = Result<T, SpiceDBError>> + Send {
    async_stream::try_stream! {
        let exec = plan.start()?;
        let end = exec.deadline;
        futures::pin_mut!(stream);
        loop {
            let next = EXEC
                .scope(exec.clone(), async {
                    match end {
                        Some(end) => tokio::time::timeout_at(end, stream.next())
                            .await
                            .map_err(|_| elapsed()),
                        None => Ok(stream.next().await),
                    }
                })
                .await?;
            match next {
                Some(value) => yield value?,
                None => break,
            }
        }
    }
}

/// When one page of a finite paged read must have delivered its last item:
/// the client default deadline from the moment the page is requested, or
/// `None` when the default is disabled. The operation's total deadline (the
/// caller's own) still bounds the whole read, so the earlier of the two wins.
fn page_deadline(page_timeout: Option<Duration>) -> Option<Instant> {
    page_timeout.and_then(|t| Instant::now().checked_add(t))
}

/// Runs one step of a page (opening it, or reading its next item) within the
/// page's deadline, so a server that stalls mid-read fails the read instead
/// of hanging it.
async fn within_page<T>(
    page_end: Option<Instant>,
    future: impl Future<Output = Result<T, SpiceDBError>>,
) -> Result<T, SpiceDBError> {
    match page_end {
        Some(end) => tokio::time::timeout_at(end, future).await.map_err(|_| {
            SpiceDBError::DeadlineExceeded(
                "page deadline exceeded: the server did not finish the page within the \
                 client default deadline"
                    .into(),
            )
        })?,
        None => future.await,
    }
}

/// Receives a whole page within its deadline, before any item reaches the
/// caller, so the page deadline bounds only waiting on the server: the time
/// the caller then spends on each item never counts against it. A page holds
/// at most the page size, so the buffer is bounded. A failure part-way
/// through is returned with the items received before it, which the caller
/// gets first.
async fn receive_page<T>(
    page_end: Option<Instant>,
    stream: &mut tonic::Streaming<T>,
) -> (Vec<T>, Option<SpiceDBError>) {
    let mut page = Vec::new();
    loop {
        match within_page(page_end, next_message(stream)).await {
            Ok(Some(message)) => page.push(message),
            Ok(None) => return (page, None),
            Err(e) => return (page, Some(e)),
        }
    }
}

/// [`wire`] for a page request: the timeout sent is the earlier of the
/// operation's remaining budget and the page's.
fn wire_page<T>(value: T, page_end: Option<Instant>) -> tonic::Request<T> {
    let mut request = wire(value);
    if let Some(end) = page_end {
        let now = Instant::now();
        let page = end.saturating_duration_since(now);
        let total = EXEC
            .try_with(|exec| exec.deadline)
            .ok()
            .flatten()
            .map(|d| d.saturating_duration_since(now));
        request.set_timeout(total.map_or(page, |t| t.min(page)));
    }
    request
}

/// The `optional_limit` every request of a paged read sends: the page size,
/// fixed for the whole call (SpiceDB rejects a cursor sent with a different
/// limit), or in single-request mode the total limit alone (0 meaning
/// unbounded).
fn page_request_limit(
    paging: &crate::options::Paging,
    page_size: std::num::NonZeroU32,
    limit: Option<u32>,
) -> u32 {
    match paging {
        crate::options::Paging::SingleRequest => limit.unwrap_or(0),
        _ => page_size.get(),
    }
}

/// Fails a paged operation whose full page ended with a cursor that is
/// missing, empty, or equal to the one sent: requesting the next page with it
/// would fetch the same page again, forever.
fn ensure_cursor_advanced(
    what: &str,
    next: Option<&proto::Cursor>,
    sent: Option<&proto::Cursor>,
) -> Result<(), SpiceDBError> {
    match next {
        Some(c) if !c.token.is_empty() && Some(c) != sent => Ok(()),
        _ => Err(error::internal(format!(
            "{what} page did not advance continuation cursor"
        ))),
    }
}

/// Builds a request carrying the operation's remaining budget and the caller's
/// request metadata.
pub(super) fn wire<T>(value: T) -> tonic::Request<T> {
    let mut request = tonic::Request::new(value);
    let _ = EXEC.try_with(|exec| {
        *request.metadata_mut() = exec.call.metadata.clone();
        if let Some(end) = exec.deadline {
            request.set_timeout(end.saturating_duration_since(Instant::now()));
        }
    });
    request
}

fn capture() -> Option<crate::options::ResponseMetadata> {
    EXEC.try_with(|exec| exec.call.capture.clone())
        .ok()
        .flatten()
}

/// Records a response's metadata for [`CallOptions::with_response_metadata`].
/// tonic merges unary trailers into the response metadata, so both slots get it.
fn record_metadata(metadata: &tonic::metadata::MetadataMap, unary: bool) {
    if let Some(c) = capture() {
        c.set_headers(metadata);
        if unary {
            c.set_trailers(metadata);
        }
    }
}

/// Records a failed RPC's status metadata (headers and trailers) and maps it.
pub(super) fn status_error(status: tonic::Status) -> SpiceDBError {
    if let Some(c) = capture() {
        c.set_headers(status.metadata());
        c.set_trailers(status.metadata());
    }
    error::from_grpc_status(status)
}

/// Reads the next message of a server stream, recording its trailers when it
/// ends.
pub(super) async fn next_message<T>(
    stream: &mut tonic::Streaming<T>,
) -> Result<Option<T>, SpiceDBError> {
    match stream.message().await {
        Ok(Some(m)) => Ok(Some(m)),
        Ok(None) => {
            if let (Some(c), Ok(Some(trailers))) = (capture(), stream.trailers().await) {
                c.set_trailers(&trailers);
            }
            Ok(None)
        }
        Err(status) => Err(status_error(status)),
    }
}

/// One attempt, never replayed: mutations.
pub(crate) async fn call_once<F, Fut, T>(f: F) -> Result<tonic::Response<T>, SpiceDBError>
where
    F: Fn() -> Fut,
    Fut: Future<Output = Result<tonic::Response<T>, tonic::Status>>,
{
    attempt_loop(f, false, None).await
}

/// The retry policy for a mutation: none unless the caller opted in, which
/// only mutations that are idempotent on replay may do. A per-call opt-in
/// ([`CallOptions::with_retry_policy`]) on any other mutation is an error; a
/// client-wide per-operation policy applies only to the calls that are
/// idempotent (never-idempotent operations reject one at build time). Checked
/// before any request is sent.
pub(crate) fn mutation_policy(
    idempotent: bool,
    operation: &str,
) -> Result<Option<Arc<RetryPolicy>>, SpiceDBError> {
    let (call, per_operation) = EXEC
        .try_with(|e| (e.call.retry.clone(), e.operation_retry.clone()))
        .unwrap_or((None, None));
    let Some(call) = call else {
        return Ok(per_operation.filter(|p| idempotent && p.max_retries > 0));
    };
    match Some(call).filter(|p| p.max_retries > 0) {
        None => Ok(None),
        Some(_) if !idempotent => Err(invalid(
            "retry_policy",
            &format!(
                "{operation} cannot be retried: only touch-only writes without preconditions and schema writes are idempotent on replay"
            ),
        )),
        Some(policy) => Ok(Some(Arc::new(policy))),
    }
}

/// A mutation under the retry policy [`mutation_policy`] returned.
pub(crate) async fn mutation_call<F, Fut, T>(
    f: F,
    policy: Option<Arc<RetryPolicy>>,
) -> Result<tonic::Response<T>, SpiceDBError>
where
    F: Fn() -> Fut,
    Fut: Future<Output = Result<tonic::Response<T>, tonic::Status>>,
{
    attempt_loop(f, true, policy).await
}

/// An idempotent unary read under the client's retry policy.
pub(crate) async fn retry_call<F, Fut, T>(f: F) -> Result<tonic::Response<T>, SpiceDBError>
where
    F: Fn() -> Fut,
    Fut: Future<Output = Result<tonic::Response<T>, tonic::Status>>,
{
    let policy = EXEC
        .try_with(|e| e.retry.clone())
        .unwrap_or_else(|_| Some(Arc::new(RetryPolicy::default())));
    attempt_loop(f, true, policy).await
}

/// Opens a server stream (or fetches a page) under the retry policy, unless
/// stream-establishment retries are disabled. Nothing has been exposed yet.
pub(crate) async fn open_stream<F, Fut, T>(f: F) -> Result<tonic::Response<T>, SpiceDBError>
where
    F: Fn() -> Fut,
    Fut: Future<Output = Result<tonic::Response<T>, tonic::Status>>,
{
    let policy = EXEC
        .try_with(|e| e.retry.clone().filter(|_| e.stream_retries))
        .unwrap_or_else(|_| Some(Arc::new(RetryPolicy::default())));
    attempt_loop(f, false, policy).await
}

async fn attempt_loop<F, Fut, T>(
    f: F,
    unary: bool,
    policy: Option<Arc<RetryPolicy>>,
) -> Result<tonic::Response<T>, SpiceDBError>
where
    F: Fn() -> Fut,
    Fut: Future<Output = Result<tonic::Response<T>, tonic::Status>>,
{
    let mut attempt = 0u32;
    loop {
        match f().await {
            Ok(response) => {
                record_metadata(response.metadata(), unary);
                return Ok(response);
            }
            Err(status) => {
                let err = status_error(status);
                let Some(policy) = policy.as_ref() else {
                    return Err(err);
                };
                if attempt >= policy.max_retries || !policy.retryable(err.code()) {
                    return Err(err);
                }
                let delay = policy.delay(attempt);
                attempt += 1;
                if let Some(cb) = &policy.on_retry {
                    cb.call(&RetryEvent {
                        attempt,
                        error: err,
                        delay,
                    });
                }
                tokio::time::sleep(delay).await;
            }
        }
    }
}

impl SpiceDBClient {
    /// Checks one question with the singular CheckPermission RPC. Tracing is opt-in.
    pub async fn check(&self, request: PermissionCheck) -> Result<CheckResult, SpiceDBError> {
        let options = request.options.clone().unwrap_or_default();
        bounded(
            self.plan(
                Operation::Check,
                self.effective_timeout(options.timeout),
                &options.call,
            ),
            async {
                request.validate()?;
                let context =
                    merge_check_context(options.context.as_ref(), request.context.as_ref());
                if let Some(c) = &context {
                    validate_context(c)?;
                }
                let value = proto::CheckPermissionRequest {
                    consistency: Some(request.consistency.to_proto()),
                    resource: Some(request.resource.to_proto()),
                    permission: request.permission,
                    subject: Some(request.subject.to_proto()),
                    context: context.as_ref().map(check_context_to_proto),
                    with_tracing: options.tracing == TracingMode::Enabled,
                };
                let result = self
                    .retry(|| async {
                        self.proto
                            .permissions
                            .clone()
                            .check_permission(wire(value.clone()))
                            .await
                    })
                    .await?
                    .into_inner();
                require_permissionship(result.permissionship, "check")?;
                Ok(CheckResult {
                    permissionship: check_permissionship_from_proto(result.permissionship),
                    missing_context: result
                        .partial_caveat_info
                        .map(|c| c.missing_required_context)
                        .unwrap_or_default(),
                    checked_at: result
                        .checked_at
                        .map(|t| t.token.into())
                        .unwrap_or_default(),
                    debug_trace: result
                        .debug_trace
                        .as_ref()
                        .map(DebugInformation::from_proto)
                        .transpose()?,
                    expiration: result
                        .optional_expires_at
                        .as_ref()
                        .and_then(timestamp_from_proto),
                    error: None,
                })
            },
        )
        .await
    }
    /// Checks ordered questions in chunks of at most 1,000 under one total budget.
    pub async fn check_bulk(
        &self,
        request: BulkCheckRequest,
    ) -> Result<Vec<CheckResult>, SpiceDBError> {
        bounded(
            self.plan(
                Operation::CheckBulk,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                if let Some(c) = &request.options.context {
                    validate_context(c)?;
                }
                for item in &request.items {
                    item.validate()?;
                    if item.options.is_some() {
                        return Err(invalid(
                            "items.options",
                            "bulk execution options belong on BulkCheckRequest",
                        ));
                    }
                    if request
                        .items
                        .first()
                        .is_some_and(|first| first.consistency != item.consistency)
                    {
                        return Err(invalid(
                            "items.consistency",
                            "all bulk items must have identical consistency",
                        ));
                    }
                }
                let mut results = Vec::with_capacity(request.items.len());
                for (chunk_index, chunk) in
                    request.items.chunks(DEFAULT_CHECK_BATCH_SIZE).enumerate()
                {
                    let items: Vec<_> = chunk
                        .iter()
                        .map(|item| proto::CheckBulkPermissionsRequestItem {
                            resource: Some(item.resource.to_proto()),
                            permission: item.permission.clone(),
                            subject: Some(item.subject.to_proto()),
                            context: merge_check_context(
                                request.options.context.as_ref(),
                                item.context.as_ref(),
                            )
                            .as_ref()
                            .map(check_context_to_proto),
                        })
                        .collect();
                    let value = proto::CheckBulkPermissionsRequest {
                        consistency: Some(chunk[0].consistency.to_proto()),
                        items: items.clone(),
                        with_tracing: request.options.tracing == TracingMode::Enabled,
                    };
                    let response = self
                        .retry(|| async {
                            self.proto
                                .permissions
                                .clone()
                                .check_bulk_permissions(wire(value.clone()))
                                .await
                        })
                        .await?
                        .into_inner();
                    if response.pairs.len() != items.len() {
                        return Err(error::internal(format!(
                            "check_bulk_permissions returned {} pair(s) for {} request item(s)",
                            response.pairs.len(),
                            items.len()
                        )));
                    }
                    let revision = response.checked_at.map(|t| t.token).unwrap_or_default();
                    for (i, pair) in response.pairs.iter().enumerate() {
                        let index = chunk_index * DEFAULT_CHECK_BATCH_SIZE + i;
                        if pair.request.as_ref() != Some(&items[i]) {
                            return Err(error::internal(format!(
                                "check item {index}: missing or mismatched response request"
                            )));
                        }
                        match &pair.response {
                            Some(proto::check_bulk_permissions_pair::Response::Item(item)) => {
                                require_permissionship(
                                    item.permissionship,
                                    &format!("check item {index}:"),
                                )?;
                                results.push(check_result_from_bulk_item(item, &revision)?)
                            }
                            Some(proto::check_bulk_permissions_pair::Response::Error(status)) => {
                                let cause = error::from_grpc_status(tonic::Status::with_details(
                                    tonic::Code::from_i32(status.code),
                                    status.message.clone(),
                                    prost::Message::encode_to_vec(status).into(),
                                ));
                                let error = SpiceDBError::BulkItem {
                                    index,
                                    source: Box::new(cause),
                                };
                                if request.options.item_errors
                                    != crate::options::BulkItemErrors::Returned
                                {
                                    return Err(error);
                                }
                                results.push(CheckResult {
                                    permissionship: Permissionship::Unspecified,
                                    missing_context: Vec::new(),
                                    checked_at: revision.clone().into(),
                                    debug_trace: None,
                                    expiration: None,
                                    error: Some(error),
                                });
                            }
                            None => {
                                return Err(error::internal(format!(
                                    "check item {index}: malformed CheckBulkPermissionsPair"
                                )))
                            }
                        }
                    }
                }
                Ok(results)
            },
        )
        .await
    }
    /// Commits exactly one transaction, never replaying ambiguous mutations.
    pub async fn write(&self, request: WriteRequest) -> Result<WriteResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::Write,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                for (_, relationship) in request.transaction.updates() {
                    relationship.validate()?;
                }
                if let Some(c) = &request.options.transaction_metadata {
                    validate_context(c)?;
                }
                let revision = self
                    .write_impl(
                        &request.transaction,
                        request.options.transaction_metadata.as_ref(),
                    )
                    .await?;
                Ok(WriteResult {
                    revision: revision.into(),
                })
            },
        )
        .await
    }
    pub async fn read_schema(
        &self,
        request: ReadSchemaRequest,
    ) -> Result<ReadSchemaResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ReadSchema,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                let (schema_text, revision) = self.read_schema_impl().await?;
                Ok(ReadSchemaResult {
                    schema_text,
                    revision: revision.into(),
                })
            },
        )
        .await
    }
    pub async fn write_schema(
        &self,
        request: WriteSchemaRequest,
    ) -> Result<WriteSchemaResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::WriteSchema,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                Ok(WriteSchemaResult {
                    revision: self.write_schema_impl(&request.schema).await?.into(),
                })
            },
        )
        .await
    }
    pub async fn reflect_schema(
        &self,
        request: ReflectSchemaRequest,
    ) -> Result<ReflectSchemaResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ReflectSchema,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                request.consistency.validate()?;
                self.reflect_schema_impl(&request.consistency, &request.options.filters)
                    .await
            },
        )
        .await
    }
    pub async fn computable_permissions(
        &self,
        request: ComputablePermissionsRequest,
    ) -> Result<ComputablePermissionsResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ComputablePermissions,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                request.consistency.validate()?;
                required("definition_name", &request.definition_name)?;
                required("relation_name", &request.relation_name)?;
                let (permissions, revision) = self
                    .computable_permissions_impl(
                        &request.consistency,
                        &request.definition_name,
                        &request.relation_name,
                        request.options.definition_name_filter.as_deref(),
                    )
                    .await?;
                Ok(ComputablePermissionsResult {
                    permissions,
                    revision: revision.into(),
                })
            },
        )
        .await
    }
    pub async fn dependent_relations(
        &self,
        request: DependentRelationsRequest,
    ) -> Result<DependentRelationsResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::DependentRelations,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                request.consistency.validate()?;
                required("definition_name", &request.definition_name)?;
                required("permission_name", &request.permission_name)?;
                let (relations, revision) = self
                    .dependent_relations_impl(
                        &request.consistency,
                        &request.definition_name,
                        &request.permission_name,
                    )
                    .await?;
                Ok(DependentRelationsResult {
                    relations,
                    revision: revision.into(),
                })
            },
        )
        .await
    }
    pub async fn diff_schema(
        &self,
        request: DiffSchemaRequest,
    ) -> Result<DiffSchemaResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::DiffSchema,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                request.consistency.validate()?;
                let (diffs, revision) = self
                    .diff_schema_impl(&request.consistency, &request.comparison_schema)
                    .await?;
                Ok(DiffSchemaResult {
                    diffs,
                    revision: revision.into(),
                })
            },
        )
        .await
    }
    pub async fn expand_permission_tree(
        &self,
        request: ExpandPermissionTreeRequest,
    ) -> Result<ExpandPermissionTreeResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ExpandPermissionTree,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                request.consistency.validate()?;
                required("permission", &request.permission)?;
                self.expand_permission_tree_impl(
                    &request.consistency,
                    request.resource.object_type(),
                    request.resource.object_id(),
                    &request.permission,
                )
                .await
            },
        )
        .await
    }
    /// Experimental: register a counter. This mutation is never retried.
    pub async fn experimental_register_relationship_counter(
        &self,
        request: ExperimentalRegisterRelationshipCounterRequest,
    ) -> Result<ExperimentalRegisterRelationshipCounterResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ExperimentalRegisterRelationshipCounter,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                required("name", &request.name)?;
                self.experimental_register_relationship_counter_impl(
                    &request.name,
                    &request.filter,
                )
                .await?;
                Ok(ExperimentalRegisterRelationshipCounterResult {})
            },
        )
        .await
    }
    /// Experimental: read a counter or its explicit still-calculating state.
    pub async fn experimental_count_relationships(
        &self,
        request: ExperimentalCountRelationshipsRequest,
    ) -> Result<ExperimentalCountRelationshipsResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ExperimentalCountRelationships,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                required("name", &request.name)?;
                self.experimental_count_relationships_impl(&request.name)
                    .await
            },
        )
        .await
    }
    /// Experimental: unregister a counter. This mutation is never retried.
    pub async fn experimental_unregister_relationship_counter(
        &self,
        request: ExperimentalUnregisterRelationshipCounterRequest,
    ) -> Result<ExperimentalUnregisterRelationshipCounterResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ExperimentalUnregisterRelationshipCounter,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                required("name", &request.name)?;
                self.experimental_unregister_relationship_counter_impl(&request.name)
                    .await?;
                Ok(ExperimentalUnregisterRelationshipCounterResult {})
            },
        )
        .await
    }
    /// Experimental: returns an opaque roaring64 bitmap of resource IDs.
    pub async fn experimental_roaring_lookup_resources(
        &self,
        request: ExperimentalRoaringLookupResourcesRequest,
    ) -> Result<ExperimentalRoaringLookupResourcesResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ExperimentalRoaringLookupResources,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                request.consistency.validate()?;
                required("resource_type", &request.resource_type)?;
                required("permission", &request.permission)?;
                self.experimental_roaring_lookup_resources_impl(
                    &request.consistency,
                    &request.resource_type,
                    &request.permission,
                    &request.subject,
                )
                .await
            },
        )
        .await
    }
    /// Deletes at most the explicit limit with one RPC. Partial success is returned unchanged.
    pub async fn delete_relationships(
        &self,
        request: DeleteRequest,
    ) -> Result<DeleteResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::DeleteRelationships,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                mutation_policy(false, "delete relationships")?;
                let allow_partial =
                    request.options.partial != crate::options::DeletePartial::Disallowed;
                if allow_partial && request.limit.is_none() {
                    return Err(invalid(
                        "limit",
                        "a positive limit is required unless partial deletion is disallowed",
                    ));
                }
                self.delete_page(
                    &request.filter,
                    request.limit.map_or(0, |l| l.get()),
                    allow_partial,
                    &request.options.preconditions,
                    request.options.cursor.as_deref(),
                    request.options.transaction_metadata.as_ref(),
                )
                .await
            },
        )
        .await
    }
    /// Deletes all matches in pages under one budget. Successful pages are not atomic together.
    /// Dropping this future cannot return progress; persist bounded calls for resumable cancellation.
    /// Deletes in transactional batches; the whole operation is not atomic.
    /// Completed batches remain committed if a later batch fails.
    pub async fn delete_all_relationships_non_atomic(
        &self,
        request: DeleteAllRequest,
    ) -> Result<DeleteResult, SpiceDBError> {
        let mut progress: Option<DeleteResult> = None;
        let result = bounded(
            self.plan(
                Operation::DeleteAllRelationshipsNonAtomic,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                mutation_policy(false, "delete relationships")?;
                let mut cursor = request.options.cursor.clone();
                loop {
                    let mut page = self
                        .delete_page(
                            &request.filter,
                            request.options.page_size.get(),
                            true,
                            &request.options.preconditions,
                            cursor.as_deref(),
                            request.options.transaction_metadata.as_ref(),
                        )
                        .await?;
                    let count = page.deleted_count;
                    page.deleted_count += progress.as_ref().map_or(0, |p| p.deleted_count);
                    cursor = page.cursor.clone().filter(|token| !token.is_empty());
                    progress = Some(page.clone());
                    if let Some(callback) = &request.options.progress {
                        callback.call(&page);
                    }
                    if page.complete {
                        return Ok(page);
                    }
                    if count == 0 {
                        return Err(error::internal("delete-all made no progress".into()));
                    }
                }
            },
        )
        .await;
        result.map_err(|source| match source {
            // A received but unrecognized progress value still acknowledges this
            // page's count. Preserve it together with earlier acknowledged pages,
            // and stop without guessing whether another mutation is appropriate.
            SpiceDBError::DeleteProgress {
                progress: mut page,
                source,
            } => {
                page.deleted_count += progress.as_ref().map_or(0, |p| p.deleted_count);
                SpiceDBError::DeleteProgress {
                    progress: page,
                    source,
                }
            }
            source => match progress {
                Some(progress) => SpiceDBError::DeleteProgress {
                    progress,
                    source: Box::new(source),
                },
                None => source,
            },
        })
    }
    async fn delete_page(
        &self,
        filter: &Filter,
        limit: u32,
        allow_partial: bool,
        preconditions: &[Precondition],
        cursor: Option<&str>,
        metadata: Option<&Context>,
    ) -> Result<DeleteResult, SpiceDBError> {
        if let Some(c) = metadata {
            validate_context(c)?;
        }
        let request = proto::DeleteRelationshipsRequest {
            relationship_filter: Some(filter.to_proto()?),
            optional_limit: limit,
            optional_allow_partial_deletions: allow_partial,
            optional_preconditions: preconditions_to_proto(preconditions)?,
            optional_transaction_metadata: metadata.map(check_context_to_proto),
            optional_cursor: cursor
                .filter(|s| !s.is_empty())
                .map(|s| proto::Cursor { token: s.into() }),
        };
        let response = call_once(|| async {
            self.proto
                .permissions
                .clone()
                .delete_relationships(wire(request.clone()))
                .await
        })
        .await?
        .into_inner();
        let wire_progress = response.deletion_progress;
        let progress = DeleteResult {
            revision: response
                .deleted_at
                .map(|t| t.token.into())
                .unwrap_or_default(),
            deleted_count: response.relationships_deleted_count,
            complete: response.deletion_progress
                == proto::delete_relationships_response::DeletionProgress::Complete as i32,
            cursor: response.after_result_cursor.map(|c| c.token),
        };
        match wire_progress {
            value
                if value
                    == proto::delete_relationships_response::DeletionProgress::Complete as i32
                    || value
                        == proto::delete_relationships_response::DeletionProgress::Partial
                            as i32 =>
            {
                Ok(progress)
            }
            value => Err(SpiceDBError::DeleteProgress {
                progress,
                source: Box::new(error::internal(format!(
                    "unknown deletion progress value: {value}"
                ))),
            }),
        }
    }
}

impl SpiceDBClient {
    /// Reads matching relationships, paging internally. Each item carries its
    /// after-result cursor and its response's read-at revision.
    ///
    /// Every page repeats the caller's consistency and page size: SpiceDB
    /// hashes both into each cursor and rejects a cursor sent with different
    /// arguments, and the cursor itself pins later pages to the first page's
    /// revision. A total limit is enforced client-side. To resume, repeat the
    /// same request and page size with `with_cursor(item.after_result_cursor)`.
    /// Dropping the returned stream immediately releases its RPC and pending
    /// pagination.
    pub fn read_relationships(
        &self,
        request: ReadRelationshipsRequest,
    ) -> impl Stream<Item = Result<ReadRelationship, SpiceDBError>> + Send + 'static {
        let permissions = self.proto.permissions.clone();
        let page_timeout = self.default_timeout;
        stream_budget(
            self.plan(
                Operation::ReadRelationships,
                request.options.timeout,
                &request.options.call,
            ),
            async_stream::try_stream! {
                request.consistency.validate()?;
                let filter = request.filter.to_proto()?;
                let consistency = request.consistency.to_proto();
                let single = request.options.paging == crate::options::Paging::SingleRequest;
                let page_size = page_request_limit(&request.options.paging, request.options.page_size, request.options.limit);
                let mut cursor = request.options.cursor.map(|token| proto::Cursor { token });
                let mut remaining = request.options.limit.unwrap_or(u32::MAX);
                while remaining > 0 {
                    let previous_cursor = cursor.clone();
                    let page_end = page_deadline(page_timeout);
                    let response = within_page(page_end, open_stream(|| async {
                        permissions
                            .clone()
                            .read_relationships(wire_page(proto::ReadRelationshipsRequest {
                                consistency: Some(consistency.clone()),
                                relationship_filter: Some(filter.clone()),
                                optional_limit: page_size,
                                optional_cursor: cursor.clone(),
                            }, page_end))
                            .await
                    }))
                    .await?;
                    let mut stream = response.into_inner();
                    let mut count = 0;
                    let (page, failure) = receive_page(page_end, &mut stream).await;
                    for response in page {
                        cursor = response.after_result_cursor.clone();
                        count += 1;
                        // A response without its relationship is not skipped:
                        // a skipped item makes a full page look short and
                        // silently ends the read early.
                        let relationship = Relationship::from_response(
                            response
                                .relationship
                                .as_ref()
                                .ok_or_else(|| missing_in_response("relationship"))?,
                        )?;
                        remaining -= 1;
                        yield ReadRelationship {
                            relationship,
                            after_result_cursor: response.after_result_cursor.map(|c| c.token),
                            read_at: response.read_at.map(|t| t.token.into()).unwrap_or_default(),
                        };
                        if remaining == 0 {
                            break;
                        }
                    }
                    if let Some(e) = failure.filter(|_| remaining > 0) {
                        Err(e)?;
                    }
                    if single || remaining == 0 || count < page_size {
                        break;
                    }
                    ensure_cursor_advanced("read", cursor.as_ref(), previous_cursor.as_ref())?;
                }
            },
        )
    }
    /// Looks up resources, paging internally with the caller's consistency and
    /// page size on every page (see [`read_relationships`](Self::read_relationships)).
    /// Each item carries its after-result cursor and looked-up-at revision.
    pub fn lookup_resources(
        &self,
        request: LookupResourcesRequest,
    ) -> impl Stream<Item = Result<LookupResource, SpiceDBError>> + Send + 'static {
        let permissions = self.proto.permissions.clone();
        let page_timeout = self.default_timeout;
        stream_budget(
            self.plan(
                Operation::LookupResources,
                request.options.timeout,
                &request.options.call,
            ),
            async_stream::try_stream! {
                request.consistency.validate()?;
                required("resource_type", &request.resource_type)?;
                required("permission", &request.permission)?;
                if let Some(c) = &request.options.context {
                    validate_context(c)?;
                }
                let consistency = request.consistency.to_proto();
                let single = request.options.paging == crate::options::Paging::SingleRequest;
                let page_size = page_request_limit(&request.options.paging, request.options.page_size, request.options.limit);
                let mut cursor = request.options.cursor.map(|token| proto::Cursor { token });
                let mut remaining = request.options.limit.unwrap_or(u32::MAX);
                while remaining > 0 {
                    let previous_cursor = cursor.clone();
                    let page_end = page_deadline(page_timeout);
                    let response = within_page(page_end, open_stream(|| async {
                        permissions
                            .clone()
                            .lookup_resources(wire_page(proto::LookupResourcesRequest {
                                consistency: Some(consistency.clone()),
                                resource_object_type: request.resource_type.clone(),
                                permission: request.permission.clone(),
                                subject: Some(request.subject.to_proto()),
                                context: request.options.context.as_ref().map(check_context_to_proto),
                                with_debug: request.options.debug == DebugMode::Enabled,
                                optional_limit: page_size,
                                optional_cursor: cursor.clone(),
                            }, page_end))
                            .await
                    }))
                    .await?;
                    let mut stream = response.into_inner();
                    let mut count = 0;
                    let (page, failure) = receive_page(page_end, &mut stream).await;
                    for response in page {
                        let revision: ZedToken = response
                            .looked_up_at
                            .map(|t| t.token.into())
                            .unwrap_or_default();
                        cursor = response.after_result_cursor.clone();
                        count += 1;
                        if response.resource_object_id.is_empty() {
                            Err(missing_in_response("resource object ID"))?;
                        }
                        require_permissionship(response.permissionship, "lookup resources result")?;
                        if request.options.permissionship_filter == LookupPermissionshipFilter::HasPermissionOnly
                            && response.permissionship != proto::LookupPermissionship::HasPermission as i32 {
                            continue;
                        }
                        remaining -= 1;
                        yield LookupResource {
                            resource_id: response.resource_object_id,
                            permissionship: permissionship_from_proto(response.permissionship),
                            partial_caveat: partial_caveat_from_proto(response.partial_caveat_info.as_ref()),
                            looked_up_at: revision,
                            after_result_cursor: response.after_result_cursor.map(|c| c.token),
                        };
                        if remaining == 0 {
                            break;
                        }
                    }
                    if let Some(e) = failure.filter(|_| remaining > 0) {
                        Err(e)?;
                    }
                    if single || remaining == 0 || count < page_size {
                        break;
                    }
                    ensure_cursor_advanced("lookup", cursor.as_ref(), previous_cursor.as_ref())?;
                }
            },
        )
    }
    pub fn lookup_subjects(
        &self,
        request: LookupSubjectsRequest,
    ) -> impl Stream<Item = Result<LookupSubject, SpiceDBError>> + Send + 'static {
        let permissions = self.proto.permissions.clone();
        let page_timeout = self.default_timeout;
        stream_budget(
            self.plan(
                Operation::LookupSubjects,
                request.options.timeout,
                &request.options.call,
            ),
            async_stream::try_stream! {
                request.consistency.validate()?;
                required("permission", &request.permission)?;
                required("subject_type", &request.subject_type)?;
                if let Some(c) = &request.options.context {
                    validate_context(c)?;
                }
                if let Some(r) = &request.options.subject_relation {
                    required("subject_relation", r)?;
                }
                // One unpaged request returns every subject, so the default
                // deadline bounds opening the stream and each wait for the
                // server's next message, never the time the caller spends on
                // an item. It is not sent as the request's timeout, which
                // would end the stream while the caller works.
                let response = within_page(page_deadline(page_timeout), open_stream(|| async {
                    permissions
                        .clone()
                        .lookup_subjects(wire(proto::LookupSubjectsRequest {
                            consistency: Some(request.consistency.to_proto()),
                            resource: Some(request.resource.to_proto()),
                            permission: request.permission.clone(),
                            subject_object_type: request.subject_type.clone(),
                            optional_subject_relation: request
                                .options
                                .subject_relation
                                .clone()
                                .unwrap_or_default(),
                            context: request.options.context.as_ref().map(check_context_to_proto),
                            optional_concrete_limit: request.options.limit.unwrap_or(0),
                            optional_cursor: None,
                            wildcard_option: match request.options.wildcard_option {
                                WildcardOption::Include => 1,
                                WildcardOption::Exclude => 2,
                            },
                        }))
                        .await
                }))
                .await?;
                let mut stream = response.into_inner();
                while let Some(response) =
                    within_page(page_deadline(page_timeout), next_message(&mut stream)).await?
                {
                    // Servers that predate `subject` send the deprecated
                    // response-level fields instead; with neither, the
                    // response names no subject and is not skipped.
                    #[allow(deprecated)]
                    let subject = match response.subject.as_ref() {
                        Some(s) => resolved_subject_from_proto(s),
                        None => ResolvedSubject {
                            subject_id: response.subject_object_id.clone(),
                            permissionship: permissionship_from_proto(response.permissionship),
                            partial_caveat: partial_caveat_from_proto(response.partial_caveat_info.as_ref()),
                        },
                    };
                    if subject.subject_id.is_empty() {
                        Err(missing_in_response("lookup subject"))?;
                    }
                    #[allow(deprecated)]
                    let wire_permissionship = response
                        .subject
                        .as_ref()
                        .map_or(response.permissionship, |s| s.permissionship);
                    require_permissionship(wire_permissionship, "lookup subjects result")?;
                    let mut excluded_subjects: Vec<_> = response
                        .excluded_subjects
                        .iter()
                        .map(resolved_subject_from_proto)
                        .collect();
                    #[allow(deprecated)]
                    if excluded_subjects.is_empty() {
                        excluded_subjects = response
                            .excluded_subject_ids
                            .into_iter()
                            .map(|subject_id| ResolvedSubject {
                                subject_id,
                                permissionship: Permissionship::Unspecified,
                                partial_caveat: None,
                            })
                            .collect();
                    }
                    yield LookupSubject {
                        subject,
                        excluded_subjects,
                        looked_up_at: response
                            .looked_up_at
                            .map(|t| t.token.into())
                            .unwrap_or_default(),
                    };
                }
            },
        )
    }
    /// Exports relationships, paging with the caller's consistency and page
    /// size on every page. Each item carries its response's after-result
    /// cursor (shared by every item of that response; exports report no
    /// per-response revision). Resume by repeating the request and page size
    /// with `with_cursor`.
    pub fn export_relationships(
        &self,
        request: ExportRelationshipsRequest,
    ) -> impl Stream<Item = Result<ExportedRelationship, SpiceDBError>> + Send + 'static {
        let permissions = self.proto.permissions.clone();
        let page_timeout = self.default_timeout;
        stream_budget(
            self.plan(
                Operation::ExportRelationships,
                request.options.timeout,
                &request.options.call,
            ),
            async_stream::try_stream! {
                request.consistency.validate()?;
                let filter = request.options.filter.map(|f| f.to_proto()).transpose()?;
                let consistency = request.consistency.to_proto();
                let page_size = request.options.page_size.get();
                let mut cursor = request.options.cursor.map(|token| proto::Cursor { token });
                let mut remaining = request.options.limit.unwrap_or(u32::MAX);
                while remaining > 0 {
                    let previous_cursor = cursor.clone();
                    let page_end = page_deadline(page_timeout);
                    let response = within_page(page_end, open_stream(|| async {
                        permissions
                            .clone()
                            .export_bulk_relationships(wire_page(proto::ExportBulkRelationshipsRequest {
                                consistency: Some(consistency.clone()),
                                optional_limit: page_size,
                                optional_cursor: cursor.clone(),
                                optional_relationship_filter: filter.clone(),
                            }, page_end))
                            .await
                    }))
                    .await?;
                    let mut stream = response.into_inner();
                    let mut count = 0;
                    let (page, failure) = receive_page(page_end, &mut stream).await;
                    for response in page {
                        cursor = response.after_result_cursor.clone();
                        let after = response.after_result_cursor.map(|c| c.token);
                        for relationship in response.relationships {
                            count += 1;
                            remaining -= 1;
                            yield ExportedRelationship {
                                relationship: Relationship::from_response(&relationship)?,
                                after_result_cursor: after.clone(),
                            };
                            if remaining == 0 {
                                break;
                            }
                        }
                        if remaining == 0 {
                            break;
                        }
                    }
                    if let Some(e) = failure.filter(|_| remaining > 0) {
                        Err(e)?;
                    }
                    if remaining == 0 || count < page_size {
                        break;
                    }
                    ensure_cursor_advanced("export", cursor.as_ref(), previous_cursor.as_ref())?;
                }
            },
        )
    }
    /// Watch has no default lifetime deadline and never restarts automatically.
    ///
    /// The client-wide retry policy (including the default and
    /// [`StreamRetryMode`]) never reopens a watch: a
    /// watch reopened without a start revision would begin at a later
    /// revision and silently skip the changes in between, so a failure to
    /// open it is returned. A per-call
    /// ([`CallOptions::with_retry_policy`](crate::CallOptions::with_retry_policy))
    /// or per-operation (`Operation::Updates`) retry policy opts in to
    /// reopening before the first event. Once an event has been yielded the
    /// watch is never reopened; resume with `with_start_revision`.
    pub fn updates(
        &self,
        request: UpdatesRequest,
    ) -> impl Stream<Item = Result<WatchEvent, SpiceDBError>> + Send + 'static {
        let watch = self.proto.watch.clone();
        stream_budget(
            self.plan(
                Operation::Updates,
                request.options.timeout,
                &request.options.call,
            ),
            async_stream::try_stream! {
                if !request.object_types.is_empty() && !request.options.filters.is_empty() {
                    Err(invalid(
                        "watch filters",
                        "object_types and relationship filters cannot be combined",
                    ))?;
                }
                let filters = request
                    .options
                    .filters
                    .iter()
                    .map(Filter::to_proto)
                    .collect::<Result<Vec<_>, _>>()?;
                let request = proto::WatchRequest {
                    optional_object_types: request.object_types,
                    optional_start_cursor: request.options.start_revision.map(|t| t.to_proto()),
                    optional_relationship_filters: filters,
                    optional_update_kinds: {
                        let mut kinds = Vec::new();
                        if request.options.checkpoints == CheckpointMode::Include || request.options.content == WatchContent::RelationshipsAndSchema {
                            kinds.push(proto::WatchKind::IncludeRelationshipUpdates as i32);
                        }
                        if request.options.checkpoints == CheckpointMode::Include {
                            kinds.push(proto::WatchKind::IncludeCheckpoints as i32);
                        }
                        if request.options.content == WatchContent::RelationshipsAndSchema {
                            kinds.push(proto::WatchKind::IncludeSchemaUpdates as i32);
                        }
                        kinds
                    },
                };
                let mut stream = open_stream(|| async { watch.clone().watch(wire(request.clone())).await })
                    .await?
                    .into_inner();
                while let Some(response) = next_message(&mut stream).await? {
                    // An update without its relationship fails the watch
                    // rather than silently dropping a change.
                    let updates = response
                        .updates
                        .iter()
                        .map(|u| {
                            Ok::<_, SpiceDBError>(Update {
                                operation: match u.operation {
                                    1 => UpdateOperation::Create,
                                    2 => UpdateOperation::Touch,
                                    3 => UpdateOperation::Delete,
                                    _ => UpdateOperation::Unspecified,
                                },
                                relationship: Relationship::from_response(
                                    u.relationship
                                        .as_ref()
                                        .ok_or_else(|| missing_in_response("watch update relationship"))?,
                                )?,
                            })
                        })
                        .collect::<Result<Vec<_>, _>>()?;
                    yield WatchEvent {
                        updates,
                        changes_through: response
                            .changes_through
                            .map(|t| t.token.into())
                            .unwrap_or_default(),
                        is_checkpoint: response.is_checkpoint,
                        schema_updated: response.schema_updated,
                        full_revision_metadata: response
                            .full_revision_metadata
                            .iter()
                            .map(context_from_proto)
                            .collect(),
                        transaction_metadata: response
                            .optional_transaction_metadata
                            .as_ref()
                            .map(context_from_proto),
                    };
                }
            },
        )
    }
    /// Backpressured upload in batches of 1,000 relationships per message
    /// (configurable with `ImportRelationshipsOptions::with_batch_size`). One
    /// call is one stream and one transaction. When the server aborts the
    /// stream (for example `AlreadyExists` for a duplicate), the server's
    /// final status is returned. No detached producer or mutation retry.
    pub async fn import_relationships(
        &self,
        request: ImportRelationshipsRequest,
    ) -> Result<ImportRelationshipsResult, SpiceDBError> {
        bounded(self.plan(Operation::ImportRelationships, request.options.timeout, &request.options.call), async {
            mutation_policy(false, "import relationships")?;
            let failure=std::sync::Arc::new(std::sync::Mutex::new(None));let producer_failure=failure.clone();
            let failed=std::sync::Arc::new(tokio::sync::Notify::new());let producer_failed=failed.clone();
            let source=request.relationships;
            let batch_size=request.options.batch_size.map_or(DEFAULT_IMPORT_BATCH_SIZE, |n| n.get()) as usize;
            let upload=async_stream::stream!{
                futures::pin_mut!(source);
                loop {
                    let mut relationships = Vec::with_capacity(batch_size);
                    while relationships.len() < batch_size {
                        match source.next().await {
                            Some(Ok(r)) => match r.validate() {
                                Ok(()) => relationships.push(r.to_proto()),
                                Err(e) => {
                                    *producer_failure.lock().unwrap() = Some(e);
                                    break;
                                }
                            },
                            Some(Err(e)) => {
                                *producer_failure.lock().unwrap() = Some(e);
                                break;
                            }
                            None => break,
                        }
                    }
                    if producer_failure.lock().unwrap().is_some() {
                        producer_failed.notify_one();
                        std::future::pending::<()>().await;
                    }
                    if relationships.is_empty() {
                        break;
                    }
                    yield proto::ImportBulkRelationshipsRequest { relationships };
                }
            };
            let mut service=self.proto.permissions.clone();
            let response=tokio::select!{ biased; _=failed.notified()=>return Err(failure.lock().unwrap().take().expect("producer notified failure")), response=service.import_bulk_relationships(wire(upload))=>response };
            if let Some(error)=failure.lock().unwrap().take(){return Err(error);}
            let response = response.map_err(status_error)?;
            record_metadata(response.metadata(), true);
            Ok(ImportRelationshipsResult{imported_count:response.into_inner().num_loaded})
        }).await
    }
}

impl PermissionSetCursor {
    fn to_proto(&self) -> materialize_proto::Cursor {
        materialize_proto::Cursor {
            limit: self.limit,
            token: self.revision.as_ref().map(ZedToken::to_proto),
            starting_index: self.starting_index,
            completed_members: self.completed_members,
            starting_key: self.starting_key.clone(),
            cursor: self.cursor.clone(),
        }
    }
    fn from_proto(c: materialize_proto::Cursor) -> Self {
        Self {
            limit: c.limit,
            revision: c.token.map(|t| t.token.into()),
            starting_index: c.starting_index,
            completed_members: c.completed_members,
            starting_key: c.starting_key,
            cursor: c.cursor,
        }
    }
}
fn set_reference(s: materialize_proto::SetReference) -> SetReference {
    SetReference {
        object: ObjectRef::from_proto(&proto::ObjectReference {
            object_type: s.object_type,
            object_id: s.object_id,
        }),
        permission_or_relation: s.permission_or_relation,
    }
}
fn set_change(c: materialize_proto::PermissionSetChange) -> PermissionSetChange {
    PermissionSetChange {
        revision: c.at_revision.map(|t| t.token.into()).unwrap_or_default(),
        operation: match c.operation {
            1 => SetOperation::Added,
            2 => SetOperation::Removed,
            _ => SetOperation::Unspecified,
        },
        parent_set: c.parent_set.map(set_reference),
        child: c.child.map(|child| match child {
            materialize_proto::permission_set_change::Child::ChildSet(s) => {
                PermissionSetChild::Set(set_reference(s))
            }
            materialize_proto::permission_set_change::Child::ChildMember(m) => {
                PermissionSetChild::Member(MemberReference {
                    object: ObjectRef::from_proto(&proto::ObjectReference {
                        object_type: m.object_type,
                        object_id: m.object_id,
                    }),
                    permission_or_relation: if m.optional_permission_or_relation.is_empty() {
                        None
                    } else {
                        Some(m.optional_permission_or_relation)
                    },
                })
            }
        }),
    }
}
impl SpiceDBClient {
    /// Experimental Materialize filter count; this RPC has no consistency field.
    pub async fn experimental_count_relationships_by_filter(
        &self,
        request: ExperimentalCountRelationshipsByFilterRequest,
    ) -> Result<ExperimentalCountRelationshipsByFilterResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::ExperimentalCountRelationshipsByFilter,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                let filter = request.filter.to_proto()?;
                let response = self
                    .retry(|| async {
                        self.proto
                            .materialize_relationships
                            .clone()
                            .experimental_count_relationships_by_filter(wire(
                                materialize_proto::ExperimentalCountRelationshipsByFilterRequest {
                                    relationship_filter: Some(filter.clone()),
                                },
                            ))
                            .await
                    })
                    .await?
                    .into_inner();
                Ok(ExperimentalCountRelationshipsByFilterResult {
                    relationship_count: response.relationship_count,
                    revision: response.read_at.map(|t| t.token.into()).unwrap_or_default(),
                })
            },
        )
        .await
    }
    /// Experimental Materialize stream. Drop to release; resume explicitly using the last revision.
    ///
    /// Like [`updates`](Self::updates), it is never reopened under the
    /// client-wide retry policy; only a per-call or per-operation
    /// (`Operation::WatchPermissionSets`) policy opts in to reopening before
    /// the first event.
    pub fn watch_permission_sets(
        &self,
        request: WatchPermissionSetsRequest,
    ) -> impl Stream<Item = Result<PermissionSetEvent, SpiceDBError>> + Send + 'static {
        let service = self.proto.permission_sets.clone();
        stream_budget(
            self.plan(
                Operation::WatchPermissionSets,
                request.options.timeout,
                &request.options.call,
            ),
            async_stream::try_stream! {
                let value=materialize_proto::WatchPermissionSetsRequest{optional_starting_after:request.options.start_revision.map(|t|t.to_proto())};
                let mut stream=open_stream(||async{service.clone().watch_permission_sets(wire(value.clone())).await}).await?.into_inner();
                while let Some(response) = next_message(&mut stream).await? {
                    use materialize_proto::watch_permission_sets_response::Response;
                    yield match response.response {
                        Some(Response::Change(c)) => PermissionSetEvent::Change(set_change(c)),
                        Some(Response::CompletedRevision(t)) => {
                            PermissionSetEvent::CompletedRevision(t.token.into())
                        }
                        Some(Response::LookupPermissionSetsRequired(r)) => PermissionSetEvent::LookupRequired {
                            revision: r
                                .required_lookup_at
                                .map(|t| t.token.into())
                                .unwrap_or_default(),
                        },
                        Some(Response::BreakingSchemaChange(c)) => PermissionSetEvent::BreakingSchemaChange {
                            revision: c.change_at.map(|t| t.token.into()).unwrap_or_default(),
                            affected_permissions: c
                                .affected_permissions
                                .into_iter()
                                .map(|p| WatchedPermission {
                                    resource_type: p.resource_type,
                                    permission: p.permission,
                                    subject_type: p.subject_type,
                                    subject_relation: if p.optional_subject_relation.is_empty() {
                                        None
                                    } else {
                                        Some(p.optional_subject_relation)
                                    },
                                })
                                .collect(),
                        },
                        None => PermissionSetEvent::Unknown,
                    };
                }
            },
        )
    }
    /// Experimental: returns one bounded permission-set page with every native resume cursor field.
    pub fn lookup_permission_sets(
        &self,
        request: LookupPermissionSetsRequest,
    ) -> impl Stream<Item = Result<LookupPermissionSetsResult, SpiceDBError>> + Send + 'static {
        let service = self.proto.permission_sets.clone();
        let page_timeout = self.default_timeout;
        stream_budget(
            self.plan(
                Operation::LookupPermissionSets,
                request.options.timeout,
                &request.options.call,
            ),
            async_stream::try_stream! {
                let value = materialize_proto::LookupPermissionSetsRequest {
                    limit: request.limit.get(),
                    optional_at_revision: request.options.at_revision.map(|t| t.to_proto()),
                    optional_starting_after_cursor: request.options.cursor.map(|c| c.to_proto()),
                };
                let page_end = page_deadline(page_timeout);
                let mut stream = within_page(page_end, open_stream(|| async {
                    service
                        .clone()
                        .lookup_permission_sets(wire_page(value.clone(), page_end))
                        .await
                }))
                .await?
                .into_inner();
                let (page, failure) = receive_page(page_end, &mut stream).await;
                for response in page {
                    yield LookupPermissionSetsResult {
                        change: response.change.map(set_change),
                        cursor: response.cursor.map(PermissionSetCursor::from_proto),
                    };
                }
                if let Some(e) = failure {
                    Err(e)?;
                }
            },
        )
    }
    /// Experimental: signed download URLs and their precise snapshot revision.
    pub async fn download_permission_sets(
        &self,
        request: DownloadPermissionSetsRequest,
    ) -> Result<DownloadPermissionSetsResult, SpiceDBError> {
        bounded(
            self.plan(
                Operation::DownloadPermissionSets,
                self.effective_timeout(request.options.timeout),
                &request.options.call,
            ),
            async {
                let value = materialize_proto::DownloadPermissionSetsRequest {
                    optional_at_revision: request.options.at_revision.map(|t| t.to_proto()),
                };
                let response = self
                    .retry(|| async {
                        self.proto
                            .permission_sets
                            .clone()
                            .download_permission_sets(wire(value.clone()))
                            .await
                    })
                    .await?
                    .into_inner();
                Ok(DownloadPermissionSetsResult {
                    files: response
                        .files
                        .into_iter()
                        .map(|f| PermissionSetFile {
                            name: f.name,
                            url: f.url,
                        })
                        .collect(),
                    timestamp: response.timestamp.as_ref().and_then(timestamp_from_proto),
                    revision: response
                        .at_revision
                        .map(|t| t.token.into())
                        .unwrap_or_default(),
                })
            },
        )
        .await
    }
}
