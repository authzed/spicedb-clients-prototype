using Google.Protobuf.WellKnownTypes;
using V1 = Authzed.Api.V1;

namespace SpiceDB.Client.Protocol;

/// <summary>
/// Native↔protocol conversions, for tools that print or persist protocol-shaped data (JSON
/// output, backups, debug traces) and for code mixing the native client with
/// <see cref="SpiceDBClient.RawProto"/>. These are the conversions the client itself uses.
/// Converting a protocol value that the native type cannot represent throws
/// <see cref="InvalidArgumentException"/>; unknown server enum values map to safe
/// unspecified values, as they do on the wire.
/// </summary>
public static class ProtoConvert
{
    // ── Identity and relationships ─────────────────────────────────────────
    public static V1.ObjectReference ToProto(ObjectRef value) => Native.Required(value, nameof(value)).ToProto();
    public static ObjectRef FromProto(V1.ObjectReference value) => ObjectRef.FromProto(Native.Required(value, nameof(value)));
    public static V1.SubjectReference ToProto(SubjectRef value) => Native.Required(value, nameof(value)).ToProto();
    public static SubjectRef FromProto(V1.SubjectReference value) => SubjectRef.FromProto(Native.Required(value, nameof(value)));
    public static V1.Relationship ToProto(Relationship value) => Native.Required(value, nameof(value)).ToProto();
    public static Relationship FromProto(V1.Relationship value) => Relationship.FromProto(Native.Required(value, nameof(value)));
    public static V1.RelationshipUpdate ToProto(RelationshipUpdate value) => Transaction.ToProto(Native.Required(value, nameof(value)));
    public static RelationshipUpdate FromProto(V1.RelationshipUpdate value) => SpiceDBClient.UpdateFromProto(Native.Required(value, nameof(value)));

    // ── Filters, preconditions, consistency ────────────────────────────────
    public static V1.RelationshipFilter ToProto(Filter value) => Native.Required(value, nameof(value)).ToProto();
    public static Filter FromProto(V1.RelationshipFilter value)
    {
        Native.Required(value, nameof(value));
        var filter = new Filter(value.ResourceType);
        if (value.OptionalResourceId != "") filter = filter.WithResourceId(value.OptionalResourceId);
        if (value.OptionalResourceIdPrefix != "") filter = filter.WithResourceIdPrefix(value.OptionalResourceIdPrefix);
        if (value.OptionalRelation != "") filter = filter.WithRelation(value.OptionalRelation);
        if (value.OptionalSubjectFilter is { } subject)
        {
            filter = filter.WithSubjectType(subject.SubjectType);
            if (subject.OptionalSubjectId != "") filter = filter.WithSubjectId(subject.OptionalSubjectId);
            if (subject.OptionalRelation is { } relation)
                filter = filter.WithSubjectRelation(relation.Relation is "" or "..." ? SubjectRelationFilter.Direct : SubjectRelationFilter.Named(relation.Relation));
        }
        return filter;
    }
    public static V1.Precondition ToProto(Precondition value) => Native.Required(value, nameof(value)).ToProto();
    public static Precondition FromProto(V1.Precondition value) => new(
        Native.Required(value, nameof(value)).Operation switch
        {
            V1.Precondition.Types.Operation.MustMatch => PreconditionOperation.MustMatch,
            V1.Precondition.Types.Operation.MustNotMatch => PreconditionOperation.MustNotMatch,
            _ => throw new InvalidArgumentException("Precondition operation is unspecified."),
        }, FromProto(value.Filter ?? throw new InvalidArgumentException("Precondition filter is required.")));
    public static V1.Consistency ToProto(Consistency value) => Native.Required(value, nameof(value)).ToProto();
    public static Consistency FromProto(V1.Consistency value) => Native.Required(value, nameof(value)).RequirementCase switch
    {
        V1.Consistency.RequirementOneofCase.FullyConsistent => Consistency.Full(),
        V1.Consistency.RequirementOneofCase.MinimizeLatency => Consistency.MinLatency(),
        V1.Consistency.RequirementOneofCase.AtLeastAsFresh => Consistency.AtLeast(FromProto(value.AtLeastAsFresh)),
        V1.Consistency.RequirementOneofCase.AtExactSnapshot => Consistency.Snapshot(FromProto(value.AtExactSnapshot)),
        _ => throw new InvalidArgumentException("Consistency requirement is unspecified."),
    };
    public static V1.ZedToken ToProto(ZedToken value) => new() { Token = value.Value };
    public static ZedToken FromProto(V1.ZedToken? value) => new(value?.Token ?? "");

    // ── Requests ───────────────────────────────────────────────────────────
    public static V1.CheckPermissionRequest ToProto(PermissionCheck value, CheckOptions? options = null)
    {
        Native.Required(value, nameof(value));
        return new()
        {
            Resource = value.Resource.ToProto(),
            Permission = value.Permission,
            Subject = value.Subject.ToProto(),
            Consistency = value.Consistency.ToProto(),
            Context = SpiceDBClient.MergeCheckContext(options?.Context, value.Context),
            WithTracing = options?.Tracing == CheckTracing.Enabled,
        };
    }
    public static PermissionCheck FromProto(V1.CheckPermissionRequest value) => new(
        FromProto(Native.Required(value, nameof(value)).Resource), value.Permission, FromProto(value.Subject),
        FromProto(value.Consistency ?? throw new InvalidArgumentException("Consistency is required.")), Native.FromStruct(value.Context));
    /// <summary>
    /// A permission check as the bulk request item <see cref="SpiceDBClient.CheckBulkAsync"/>
    /// sends for it, with <paramref name="options"/>' context merged under the check's own.
    /// </summary>
    public static V1.CheckBulkPermissionsRequestItem ToCheckBulkRequestItem(PermissionCheck value, CheckOptions? options = null)
    {
        Native.Required(value, nameof(value));
        return new()
        {
            Resource = value.Resource.ToProto(),
            Permission = value.Permission,
            Subject = value.Subject.ToProto(),
            Context = SpiceDBClient.MergeCheckContext(options?.Context, value.Context),
        };
    }
    public static V1.WriteRelationshipsRequest ToProto(Transaction value, WriteOptions? options = null)
    {
        Native.Required(value, nameof(value));
        var request = new V1.WriteRelationshipsRequest { OptionalTransactionMetadata = Native.Struct(options?.Metadata) };
        request.Updates.AddRange(value.V1Updates); request.OptionalPreconditions.AddRange(value.V1Preconditions);
        return request;
    }

    // ── Results ────────────────────────────────────────────────────────────
    public static V1.CheckPermissionResponse ToProto(CheckResult value)
    {
        Native.Required(value, nameof(value));
        var response = new V1.CheckPermissionResponse
        {
            CheckedAt = ToProto(value.CheckedAt),
            Permissionship = ToCheckPermissionship(value.Permissionship),
            DebugTrace = value.DebugTrace is null ? null : ToProto(value.DebugTrace),
            OptionalExpiresAt = value.Expiration is { } e ? Timestamp.FromDateTimeOffset(e) : null,
        };
        if (value.MissingContext.Count > 0) response.PartialCaveatInfo = new() { MissingRequiredContext = { value.MissingContext } };
        return response;
    }
    public static CheckResult FromProto(V1.CheckPermissionResponse value) => new(
        SpiceDBClient.ToCheckPermissionship(Native.Required(value, nameof(value)).Permissionship),
        value.PartialCaveatInfo?.MissingRequiredContext ?? Enumerable.Empty<string>(), FromProto(value.CheckedAt),
        DebugInformation.FromProto(value.DebugTrace), value.OptionalExpiresAt?.ToDateTimeOffset());
    /// <summary>
    /// Rebuilds the server's bulk-check response from the requests and the results
    /// <see cref="SpiceDBClient.CheckBulkAsync"/> returned for them, in the same order: one pair
    /// per item carrying its request item and either its result or, for a result with
    /// <see cref="CheckResult.Error"/> set (<see cref="BulkItemErrors.Returned"/>), its status
    /// (the server's own status, details included, when it sent one). Pass the call's
    /// <paramref name="options"/> so request items carry the same merged context. The response's
    /// checked-at is the last item's revision; a call over 1,000 items spans several requests,
    /// each with its own revision.
    /// </summary>
    public static V1.CheckBulkPermissionsResponse ToCheckBulkResponse(IReadOnlyList<PermissionCheck> requests, IReadOnlyList<CheckResult> results, CheckOptions? options = null)
    {
        Native.Required(requests, nameof(requests)); Native.Required(results, nameof(results));
        if (requests.Count != results.Count)
            throw new InvalidArgumentException($"check bulk: {requests.Count} requests but {results.Count} results.");
        var response = new V1.CheckBulkPermissionsResponse();
        for (var i = 0; i < requests.Count; i++)
        {
            var result = Native.Required(results[i], nameof(results));
            var pair = new V1.CheckBulkPermissionsPair { Request = ToCheckBulkRequestItem(requests[i], options) };
            if (result.Error is { } error) pair.Error = ToRpcStatus(error);
            else
            {
                var single = ToProto(result);
                pair.Item = new() { Permissionship = single.Permissionship, PartialCaveatInfo = single.PartialCaveatInfo, DebugTrace = single.DebugTrace };
            }
            if (!string.IsNullOrEmpty(result.CheckedAt.Value)) response.CheckedAt = ToProto(result.CheckedAt);
            response.Pairs.Add(pair);
        }
        return response;
    }
    private static Google.Rpc.Status ToRpcStatus(SpiceDBException error) =>
        error.InnerException is Grpc.Core.RpcException rpc && rpc.StatusCode == error.StatusCode && Grpc.Core.RpcExceptionExtensions.GetRpcStatus(rpc) is { } full
            ? full
            : new Google.Rpc.Status { Code = (int)error.StatusCode, Message = error.ServerMessage };
    public static V1.DebugInformation ToProto(DebugInformation value) => new()
    {
        SchemaUsed = Native.Required(value, nameof(value)).SchemaUsed,
        Check = value.Check is null ? null : ToProto(value.Check),
    };
    public static DebugInformation FromProto(V1.DebugInformation value) => DebugInformation.FromProto(Native.Required(value, nameof(value)))!;
    public static V1.CheckDebugTrace ToProto(CheckDebugTrace value)
    {
        Native.Required(value, nameof(value));
        var trace = new V1.CheckDebugTrace
        {
            Resource = value.Resource?.ToProto(),
            Permission = value.Permission,
            PermissionType = (V1.CheckDebugTrace.Types.PermissionType)(int)value.PermissionType,
            Subject = value.Subject?.ToProto(),
            Result = (V1.CheckDebugTrace.Types.Permissionship)(int)ToCheckPermissionship(value.Result),
            Duration = value.Duration is { } d ? Duration.FromTimeSpan(d) : null,
            OptionalExpiresAt = value.Expiration is { } e ? Timestamp.FromDateTimeOffset(e) : null,
            TraceOperationId = value.TraceOperationId,
            Source = value.Source,
        };
        if (value.CaveatEvaluation is { } c)
        {
            trace.CaveatEvaluationInfo = new() { Expression = c.Expression, Result = (V1.CaveatEvalInfo.Types.Result)(int)c.Result, Context = Native.Struct(c.Context), CaveatName = c.CaveatName };
            if (c.MissingContext.Count > 0) trace.CaveatEvaluationInfo.PartialCaveatInfo = new() { MissingRequiredContext = { c.MissingContext } };
        }
        if (value.SubProblems is { } subProblems) trace.SubProblems = new() { Traces = { subProblems.Select(ToProto) } };
        else if (value.WasCachedResult is { } cached) trace.WasCachedResult = cached;
        return trace;
    }
    public static V1.LookupResourcesResponse ToProto(LookupResource value)
    {
        Native.Required(value, nameof(value));
        return new()
        {
            ResourceObjectId = value.ResourceId,
            LookedUpAt = ToProto(value.LookedUpAt),
            Permissionship = ToLookupPermissionship(value.Permissionship),
            PartialCaveatInfo = value.PartialCaveat is { } p ? new() { MissingRequiredContext = { p.MissingRequiredContext } } : null,
            AfterResultCursor = string.IsNullOrEmpty(value.AfterResultCursor) ? null : new() { Token = value.AfterResultCursor },
        };
    }
    /// <summary>
    /// A looked-up subject as the server's response, including wildcard exclusions. It also
    /// fills the deprecated top-level fields (subject ID, excluded subject IDs, permissionship
    /// and partial caveat info) that SpiceDB still sends, so it matches the server's wire shape.
    /// </summary>
    public static V1.LookupSubjectsResponse ToProto(LookupSubject value)
    {
        Native.Required(value, nameof(value));
        var subject = ToProto(value.Subject);
        var response = new V1.LookupSubjectsResponse { LookedUpAt = ToProto(value.LookedUpAt), Subject = subject };
        response.ExcludedSubjects.AddRange(value.ExcludedSubjects.Select(ToProto));
#pragma warning disable CS0612 // deprecated fields SpiceDB still populates
        response.SubjectObjectId = subject.SubjectObjectId;
        response.Permissionship = subject.Permissionship;
        response.PartialCaveatInfo = subject.PartialCaveatInfo?.Clone();
        response.ExcludedSubjectIds.AddRange(value.ExcludedSubjects.Select(e => e.SubjectId));
#pragma warning restore CS0612
        return response;
    }
    public static LookupSubject FromProto(V1.LookupSubjectsResponse value) => SpiceDBClient.ToLookupSubject(Native.Required(value, nameof(value)));
    private static V1.ResolvedSubject ToProto(ResolvedSubject value) => new()
    {
        SubjectObjectId = value.SubjectId,
        Permissionship = ToLookupPermissionship(value.Permissionship),
        PartialCaveatInfo = value.PartialCaveat is { } p ? new() { MissingRequiredContext = { p.MissingRequiredContext } } : null,
    };
    public static V1.ExpandPermissionTreeResponse ToProto(ExpandResult value) => new()
    {
        TreeRoot = ToProto(Native.Required(value, nameof(value)).Tree),
        ExpandedAt = ToProto(value.Revision),
    };
    public static ExpandResult FromProto(V1.ExpandPermissionTreeResponse value) => new()
    {
        Tree = SpiceDBClient.ToPermissionTree(Native.Required(value, nameof(value)).TreeRoot),
        Revision = FromProto(value.ExpandedAt),
    };
    public static V1.PermissionRelationshipTree ToProto(PermissionTree value)
    {
        Native.Required(value, nameof(value));
        var tree = new V1.PermissionRelationshipTree { ExpandedObject = value.ExpandedObject?.ToProto(), ExpandedRelation = value.ExpandedRelation };
        if (value.Intermediate is { } i)
            tree.Intermediate = new() { Operation = (V1.AlgebraicSubjectSet.Types.Operation)(int)i.Operation, Children = { i.Children.Select(ToProto) } };
        else if (value.Leaf is { } l)
            tree.Leaf = new() { Subjects = { l.Subjects.Select(s => s.ToProto()) } };
        return tree;
    }
    public static V1.ReadRelationshipsResponse ToProto(ReadRelationship value) => new()
    {
        Relationship = Native.Required(value, nameof(value)).Relationship.ToProto(),
        ReadAt = ToProto(value.ReadAt),
        AfterResultCursor = string.IsNullOrEmpty(value.AfterResultCursor) ? null : new() { Token = value.AfterResultCursor },
    };
    public static ReadRelationship FromProto(V1.ReadRelationshipsResponse value) => new(
        Relationship.FromProto(Native.Required(value, nameof(value)).Relationship), value.AfterResultCursor?.Token, FromProto(value.ReadAt));
    public static V1.WriteRelationshipsResponse ToProto(WriteResult value) => new() { WrittenAt = ToProto(Native.Required(value, nameof(value)).Revision) };
    public static WriteResult FromProto(V1.WriteRelationshipsResponse value) => new(FromProto(Native.Required(value, nameof(value)).WrittenAt));

    private static V1.CheckPermissionResponse.Types.Permissionship ToCheckPermissionship(Permissionship value) => value switch
    {
        Permissionship.HasPermission => V1.CheckPermissionResponse.Types.Permissionship.HasPermission,
        Permissionship.NoPermission => V1.CheckPermissionResponse.Types.Permissionship.NoPermission,
        Permissionship.ConditionalPermission => V1.CheckPermissionResponse.Types.Permissionship.ConditionalPermission,
        _ => V1.CheckPermissionResponse.Types.Permissionship.Unspecified,
    };
    private static V1.LookupPermissionship ToLookupPermissionship(Permissionship value) => value switch
    {
        Permissionship.HasPermission => V1.LookupPermissionship.HasPermission,
        Permissionship.ConditionalPermission => V1.LookupPermissionship.ConditionalPermission,
        _ => V1.LookupPermissionship.Unspecified,
    };
}
