//! Canonical request-shaped operations. Every future and stream owns its transport handle.
use super::*;
use futures::StreamExt;
use std::future::Future;
use tokio::time::Instant;

tokio::task_local! { static DEADLINE: Option<Instant>; }
pub(super) fn remaining_timeout(fallback: Duration) -> Duration {
    DEADLINE
        .try_with(|d| {
            d.map(|d| d.saturating_duration_since(Instant::now()))
                .unwrap_or(fallback)
        })
        .unwrap_or(fallback)
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
    duration: Option<Duration>,
    future: impl Future<Output = Result<T, SpiceDBError>>,
) -> Result<T, SpiceDBError> {
    let end = deadline(duration)?;
    DEADLINE
        .scope(end, async move {
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
    duration: Option<Duration>,
    stream: impl Stream<Item = Result<T, SpiceDBError>> + Send + 'static,
) -> impl Stream<Item = Result<T, SpiceDBError>> + Send {
    async_stream::try_stream! {
        let end = deadline(duration)?;
        futures::pin_mut!(stream);
        loop {
            let next = DEADLINE
                .scope(end, async {
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
fn wire<T>(value: T) -> tonic::Request<T> {
    let mut request = tonic::Request::new(value);
    if let Ok(Some(end)) = DEADLINE.try_with(|d| *d) {
        request.set_timeout(end.saturating_duration_since(Instant::now()));
    }
    request
}

impl SpiceDBClient {
    /// Checks one question with the singular CheckPermission RPC. Tracing is opt-in.
    pub async fn check(&self, request: PermissionCheck) -> Result<CheckResult, SpiceDBError> {
        let options = request.options.clone().unwrap_or_default();
        bounded(Some(self.effective_timeout(options.timeout)), async {
            request.validate()?;
            let context = merge_check_context(options.context.as_ref(), request.context.as_ref());
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
                    .map(DebugInformation::from_proto),
                expiration: result
                    .optional_expires_at
                    .as_ref()
                    .and_then(timestamp_from_proto),
            })
        })
        .await
    }
    /// Checks ordered questions in chunks of at most 1,000 under one total budget.
    pub async fn check_bulk(
        &self,
        request: BulkCheckRequest,
    ) -> Result<Vec<CheckResult>, SpiceDBError> {
        bounded(
            Some(self.effective_timeout(request.options.timeout)),
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
                                results.push(check_result_from_bulk_item(item, &revision))
                            }
                            Some(proto::check_bulk_permissions_pair::Response::Error(status)) => {
                                let cause = error::from_grpc_status(tonic::Status::with_details(
                                    tonic::Code::from_i32(status.code),
                                    status.message.clone(),
                                    prost::Message::encode_to_vec(status).into(),
                                ));
                                return Err(SpiceDBError::BulkItem {
                                    index,
                                    source: Box::new(cause),
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
            Some(self.effective_timeout(request.options.timeout)),
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
                        request.options.timeout,
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                let (schema_text, revision) =
                    self.read_schema_impl(request.options.timeout).await?;
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                Ok(WriteSchemaResult {
                    revision: self
                        .write_schema_impl(&request.schema, request.options.timeout)
                        .await?
                        .into(),
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                request.consistency.validate()?;
                self.reflect_schema_impl(
                    &request.consistency,
                    request.options.timeout,
                    &request.options.filters,
                )
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                request.consistency.validate()?;
                required("definition_name", &request.definition_name)?;
                required("relation_name", &request.relation_name)?;
                let (permissions, revision) = self
                    .computable_permissions_impl(
                        &request.consistency,
                        &request.definition_name,
                        &request.relation_name,
                        request.options.timeout,
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                request.consistency.validate()?;
                required("definition_name", &request.definition_name)?;
                required("permission_name", &request.permission_name)?;
                let (relations, revision) = self
                    .dependent_relations_impl(
                        &request.consistency,
                        &request.definition_name,
                        &request.permission_name,
                        request.options.timeout,
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                request.consistency.validate()?;
                let (diffs, revision) = self
                    .diff_schema_impl(
                        &request.consistency,
                        &request.comparison_schema,
                        request.options.timeout,
                    )
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                request.consistency.validate()?;
                required("permission", &request.permission)?;
                self.expand_permission_tree_impl(
                    &request.consistency,
                    request.resource.object_type(),
                    request.resource.object_id(),
                    &request.permission,
                    request.options.timeout,
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                required("name", &request.name)?;
                self.experimental_register_relationship_counter_impl(
                    &request.name,
                    &request.filter,
                    request.options.timeout,
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                required("name", &request.name)?;
                self.experimental_count_relationships_impl(&request.name, request.options.timeout)
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                required("name", &request.name)?;
                self.experimental_unregister_relationship_counter_impl(
                    &request.name,
                    request.options.timeout,
                )
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                request.consistency.validate()?;
                required("resource_type", &request.resource_type)?;
                required("permission", &request.permission)?;
                self.experimental_roaring_lookup_resources_impl(
                    &request.consistency,
                    &request.resource_type,
                    &request.permission,
                    &request.subject,
                    request.options.timeout,
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
            Some(self.effective_timeout(request.options.timeout)),
            self.delete_page(
                &request.filter,
                request.limit.get(),
                &request.options.preconditions,
                request.options.cursor.as_deref(),
                request.options.transaction_metadata.as_ref(),
            ),
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
            Some(self.effective_timeout(request.options.timeout)),
            async {
                let mut cursor = request.options.cursor.clone();
                loop {
                    let mut page = self
                        .delete_page(
                            &request.filter,
                            request.options.page_size.get(),
                            &request.options.preconditions,
                            cursor.as_deref(),
                            request.options.transaction_metadata.as_ref(),
                        )
                        .await?;
                    let count = page.deleted_count;
                    page.deleted_count += progress.as_ref().map_or(0, |p| p.deleted_count);
                    cursor = page.cursor.clone().filter(|token| !token.is_empty());
                    progress = Some(page.clone());
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
            optional_allow_partial_deletions: true,
            optional_preconditions: preconditions_to_proto(preconditions)?,
            optional_transaction_metadata: metadata.map(check_context_to_proto),
            optional_cursor: cursor
                .filter(|s| !s.is_empty())
                .map(|s| proto::Cursor { token: s.into() }),
        };
        let response = self
            .proto
            .permissions
            .clone()
            .delete_relationships(wire(request))
            .await
            .map_err(error::from_grpc_status)?
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
    /// Dropping the returned stream immediately releases its RPC and pending pagination.
    pub fn read_relationships(
        &self,
        request: ReadRelationshipsRequest,
    ) -> impl Stream<Item = Result<Relationship, SpiceDBError>> + Send + 'static {
        let permissions = self.proto.permissions.clone();
        stream_budget(
            request.options.timeout,
            async_stream::try_stream! {
                request.consistency.validate()?;
                let filter = request.filter.to_proto()?;
                let mut consistency = request.consistency.to_proto();
                let mut cursor = request.options.cursor.map(|token| proto::Cursor { token });
                let mut remaining = request.options.limit.unwrap_or(u32::MAX);
                while remaining > 0 {
                    let limit = request.options.page_size.get().min(remaining);
                    let response = retry_call(|| async {
                        permissions
                            .clone()
                            .read_relationships(wire(proto::ReadRelationshipsRequest {
                                consistency: Some(consistency.clone()),
                                relationship_filter: Some(filter.clone()),
                                optional_limit: limit,
                                optional_cursor: cursor.clone(),
                            }))
                            .await
                    })
                    .await?;
                    let mut stream = response.into_inner();
                    let mut count = 0;
                    while let Some(response) = stream.message().await.map_err(error::from_grpc_status)? {
                        if let Some(t) = response.read_at {
                            consistency = crate::consistency::snapshot(t.token.into()).to_proto();
                        }
                        cursor = response.after_result_cursor;
                        count += 1;
                        remaining -= 1;
                        if let Some(r) = response.relationship {
                            yield Relationship::from_proto(&r);
                        }
                        if remaining == 0 {
                            break;
                        }
                    }
                    if count < limit {
                        break;
                    }
                    if cursor.is_none() {
                        Err(error::internal(
                            "read page omitted continuation cursor".into(),
                        ))?;
                    }
                }
            },
        )
    }
    pub fn lookup_resources(
        &self,
        request: LookupResourcesRequest,
    ) -> impl Stream<Item = Result<LookupResource, SpiceDBError>> + Send + 'static {
        let permissions = self.proto.permissions.clone();
        stream_budget(
            request.options.timeout,
            async_stream::try_stream! {
                request.consistency.validate()?;
                required("resource_type", &request.resource_type)?;
                required("permission", &request.permission)?;
                if let Some(c) = &request.options.context {
                    validate_context(c)?;
                }
                let mut consistency = request.consistency.to_proto();
                let mut cursor = request.options.cursor.map(|token| proto::Cursor { token });
                let mut remaining = request.options.limit.unwrap_or(u32::MAX);
                while remaining > 0 {
                    let previous_cursor = cursor.clone();
                    let limit = request.options.page_size.get().min(remaining);
                    let response = retry_call(|| async {
                        permissions
                            .clone()
                            .lookup_resources(wire(proto::LookupResourcesRequest {
                                consistency: Some(consistency.clone()),
                                resource_object_type: request.resource_type.clone(),
                                permission: request.permission.clone(),
                                subject: Some(request.subject.to_proto()),
                                context: request.options.context.as_ref().map(check_context_to_proto),
                                with_debug: request.options.debug == DebugMode::Enabled,
                                optional_limit: limit,
                                optional_cursor: cursor.clone(),
                            }))
                            .await
                    })
                    .await?;
                    let mut stream = response.into_inner();
                    let mut count = 0;
                    while let Some(response) = stream.message().await.map_err(error::from_grpc_status)? {
                        let revision: ZedToken = response
                            .looked_up_at
                            .map(|t| t.token.into())
                            .unwrap_or_default();
                        consistency = crate::consistency::snapshot(revision.clone()).to_proto();
                        cursor = response.after_result_cursor;
                        count += 1;
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
                        };
                        if remaining == 0 {
                            break;
                        }
                    }
                    if remaining == 0 || count < limit {
                        break;
                    }
                    if cursor.as_ref().is_none_or(|c| c.token.is_empty()) || cursor == previous_cursor {
                        Err(error::internal(
                            "lookup page did not advance continuation cursor".into(),
                        ))?;
                    }
                }
            },
        )
    }
    pub fn lookup_subjects(
        &self,
        request: LookupSubjectsRequest,
    ) -> impl Stream<Item = Result<LookupSubject, SpiceDBError>> + Send + 'static {
        let permissions = self.proto.permissions.clone();
        stream_budget(
            request.options.timeout,
            async_stream::try_stream! {
                request.consistency.validate()?;
                required("permission", &request.permission)?;
                required("subject_type", &request.subject_type)?;
                if let Some(c) = &request.options.context {
                    validate_context(c)?;
                }
                if let Some(r) = &request.options.subject_relation {
                    required("subject_relation", r)?;
                    if r == "..." {
                        Err(invalid("subject_relation", "use absence for direct"))?;
                    }
                }
                let response = retry_call(|| async {
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
                })
                .await?;
                let mut stream = response.into_inner();
                while let Some(response) = stream.message().await.map_err(error::from_grpc_status)? {
                    let mut subject = resolved_subject_from_proto(response.subject.as_ref());
                    #[allow(deprecated)]
                    if response.subject.is_none() {
                        subject = ResolvedSubject {
                            subject_id: response.subject_object_id,
                            permissionship: permissionship_from_proto(response.permissionship),
                            partial_caveat: partial_caveat_from_proto(response.partial_caveat_info.as_ref()),
                        };
                    }
                    let mut excluded_subjects: Vec<_> = response
                        .excluded_subjects
                        .iter()
                        .map(|s| resolved_subject_from_proto(Some(s)))
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
    pub fn export_relationships(
        &self,
        request: ExportRelationshipsRequest,
    ) -> impl Stream<Item = Result<Relationship, SpiceDBError>> + Send + 'static {
        let permissions = self.proto.permissions.clone();
        stream_budget(
            request.options.timeout,
            async_stream::try_stream! {
                request.consistency.validate()?;
                let filter = request.options.filter.map(|f| f.to_proto()).transpose()?;
                let consistency = request.consistency.to_proto();
                let mut cursor = request.options.cursor.map(|token| proto::Cursor { token });
                let mut remaining = request.options.limit.unwrap_or(u32::MAX);
                while remaining > 0 {
                    let limit = request.options.page_size.get().min(remaining);
                    let response = retry_call(|| async {
                        permissions
                            .clone()
                            .export_bulk_relationships(wire(proto::ExportBulkRelationshipsRequest {
                                consistency: Some(consistency.clone()),
                                optional_limit: limit,
                                optional_cursor: cursor.clone(),
                                optional_relationship_filter: filter.clone(),
                            }))
                            .await
                    })
                    .await?;
                    let mut stream = response.into_inner();
                    let mut count = 0;
                    while let Some(response) = stream.message().await.map_err(error::from_grpc_status)? {
                        cursor = response.after_result_cursor;
                        for relationship in response.relationships {
                            count += 1;
                            remaining -= 1;
                            yield Relationship::from_proto(&relationship);
                            if remaining == 0 {
                                break;
                            }
                        }
                        if remaining == 0 {
                            break;
                        }
                    }
                    if count < limit {
                        break;
                    }
                    if cursor.is_none() {
                        Err(error::internal(
                            "export page omitted continuation cursor".into(),
                        ))?;
                    }
                }
            },
        )
    }
    /// Watch has no default lifetime deadline and never restarts automatically.
    pub fn updates(
        &self,
        request: UpdatesRequest,
    ) -> impl Stream<Item = Result<WatchEvent, SpiceDBError>> + Send + 'static {
        let watch = self.proto.watch.clone();
        stream_budget(
            request.options.timeout,
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
                let mut stream = retry_call(|| async { watch.clone().watch(wire(request.clone())).await })
                    .await?
                    .into_inner();
                while let Some(response) = stream.message().await.map_err(error::from_grpc_status)? {
                    let updates = response
                        .updates
                        .iter()
                        .filter_map(|u| {
                            u.relationship.as_ref().map(|r| Update {
                                operation: match u.operation {
                                    1 => UpdateOperation::Create,
                                    2 => UpdateOperation::Touch,
                                    3 => UpdateOperation::Delete,
                                    _ => UpdateOperation::Unspecified,
                                },
                                relationship: Relationship::from_proto(r),
                            })
                        })
                        .collect();
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
    /// Backpressured upload in batches of 1,000. No detached producer or mutation retry.
    pub async fn import_relationships(
        &self,
        request: ImportRelationshipsRequest,
    ) -> Result<ImportRelationshipsResult, SpiceDBError> {
        bounded(request.options.timeout,async{
            let failure=std::sync::Arc::new(std::sync::Mutex::new(None));let producer_failure=failure.clone();
            let failed=std::sync::Arc::new(tokio::sync::Notify::new());let producer_failed=failed.clone();
            let source=request.relationships;
            let upload=async_stream::stream!{
                futures::pin_mut!(source);
                loop {
                    let mut relationships = Vec::with_capacity(DEFAULT_IMPORT_BATCH_SIZE);
                    while relationships.len() < DEFAULT_IMPORT_BATCH_SIZE {
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
            Ok(ImportRelationshipsResult{imported_count:response.map_err(error::from_grpc_status)?.into_inner().num_loaded})
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
        object: ObjectRef::from_proto(Some(&proto::ObjectReference {
            object_type: s.object_type,
            object_id: s.object_id,
        })),
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
                    object: ObjectRef::from_proto(Some(&proto::ObjectReference {
                        object_type: m.object_type,
                        object_id: m.object_id,
                    })),
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
            Some(self.effective_timeout(request.options.timeout)),
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
    pub fn watch_permission_sets(
        &self,
        request: WatchPermissionSetsRequest,
    ) -> impl Stream<Item = Result<PermissionSetEvent, SpiceDBError>> + Send + 'static {
        let service = self.proto.permission_sets.clone();
        stream_budget(
            request.options.timeout,
            async_stream::try_stream! {
                let value=materialize_proto::WatchPermissionSetsRequest{optional_starting_after:request.options.start_revision.map(|t|t.to_proto())};
                let mut stream=retry_call(||async{service.clone().watch_permission_sets(wire(value.clone())).await}).await?.into_inner();
                while let Some(response) = stream.message().await.map_err(error::from_grpc_status)? {
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
        stream_budget(
            request.options.timeout,
            async_stream::try_stream! {
                let value = materialize_proto::LookupPermissionSetsRequest {
                    limit: request.limit.get(),
                    optional_at_revision: request.options.at_revision.map(|t| t.to_proto()),
                    optional_starting_after_cursor: request.options.cursor.map(|c| c.to_proto()),
                };
                let mut stream = retry_call(|| async {
                    service
                        .clone()
                        .lookup_permission_sets(wire(value.clone()))
                        .await
                })
                .await?
                .into_inner();
                while let Some(response) = stream.message().await.map_err(error::from_grpc_status)? {
                    yield LookupPermissionSetsResult {
                        change: response.change.map(set_change),
                        cursor: response.cursor.map(PermissionSetCursor::from_proto),
                    };
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
            Some(self.effective_timeout(request.options.timeout)),
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
