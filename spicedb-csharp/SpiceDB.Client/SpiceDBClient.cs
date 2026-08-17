// SpiceDBClient is the idiomatic C# client for SpiceDB.

using System.Runtime.CompilerServices;
using Authzed.Api.SpiceDB.Proto;
using Authzed.Api.V1;
using Google.Protobuf.WellKnownTypes;
using Grpc.Core;
using Grpc.Net.Client;

// Exposes internal helpers (e.g. the proto -> native PermissionTree mapper)
// to the test assembly without making them part of the public API surface.
[assembly: InternalsVisibleTo("SpiceDB.Client.Tests")]

namespace SpiceDB.Client;

/// <summary>
/// The idiomatic SpiceDB client. Use <see cref="CreatePlaintext"/> or
/// <see cref="CreateSystemTls"/> to create one. Implements
/// <see cref="IAsyncDisposable"/> — the channel is disposed when the client is.
/// </summary>
public sealed class SpiceDBClient : IAsyncDisposable
{
    private const int DefaultReadPageSize = 512;
    private const int DefaultDeletePageSize = 1_000;
    private const int DefaultLookupPageSize = 512;
    private const int DefaultImportBatchSize = 1_000;
    private const int DefaultExportPageSize = 512;
    private const int DefaultCheckBatchSize = 1_000;
    private const int MaxRetryAttempts = 3;
    private static readonly TimeSpan InitialBackoff = TimeSpan.FromMilliseconds(100);

    private readonly SpiceDBProtoClient? _protoClient;
    private readonly PermissionsService.PermissionsServiceClient _permissions;
    private readonly SchemaService.SchemaServiceClient _schema;
    private readonly WatchService.WatchServiceClient _watch;
    private readonly ExperimentalService.ExperimentalServiceClient _experimental;

    private SpiceDBClient(SpiceDBProtoClient protoClient)
    {
        _protoClient = protoClient;
        _permissions = protoClient.Permissions;
        _schema = protoClient.Schema;
        _watch = protoClient.Watch;
        _experimental = protoClient.Experimental;
    }

    /// <summary>
    /// Test-only seam: constructs a client directly from (typically mocked)
    /// service clients, bypassing channel/proto-client construction entirely.
    /// Not part of the public API — exposed to the test assembly only via
    /// <see cref="System.Runtime.CompilerServices.InternalsVisibleToAttribute"/>.
    /// </summary>
    internal SpiceDBClient(
        PermissionsService.PermissionsServiceClient permissions,
        SchemaService.SchemaServiceClient schema,
        WatchService.WatchServiceClient watch,
        ExperimentalService.ExperimentalServiceClient experimental)
    {
        _protoClient = null;
        _permissions = permissions;
        _schema = schema;
        _watch = watch;
        _experimental = experimental;
    }

    /// <summary>
    /// Creates a client with a plaintext (insecure) connection. Use this for
    /// testing only — the lack of TLS is made obvious by the name.
    /// </summary>
    /// <exception cref="ArgumentException">Thrown when endpoint or presharedKey is empty.</exception>
    public static SpiceDBClient CreatePlaintext(string endpoint, string presharedKey)
    {
        ValidateArgs(endpoint, presharedKey);
        var protoClient = new SpiceDBProtoClient(endpoint, presharedKey, insecure: true);
        return new SpiceDBClient(protoClient);
    }

    /// <summary>
    /// Creates a client using the system's TLS certificate pool. Use this
    /// for production connections.
    /// </summary>
    /// <exception cref="ArgumentException">Thrown when endpoint or presharedKey is empty.</exception>
    public static SpiceDBClient CreateSystemTls(string endpoint, string presharedKey)
    {
        ValidateArgs(endpoint, presharedKey);
        var protoClient = new SpiceDBProtoClient(endpoint, presharedKey, insecure: false);
        return new SpiceDBClient(protoClient);
    }

    /// <summary>
    /// Creates a client from an existing <see cref="GrpcChannel"/>.
    /// This is the escape hatch for advanced configuration.
    /// </summary>
    public static SpiceDBClient CreateFromChannel(GrpcChannel channel, string presharedKey)
    {
        ArgumentNullException.ThrowIfNull(channel);
        if (string.IsNullOrEmpty(presharedKey))
            throw new ArgumentException("Preshared key must not be empty.", nameof(presharedKey));
        // Create a proto client wrapping the provided channel by using
        // a dummy endpoint — the channel is already configured.
        var protoClient = new SpiceDBProtoClient(channel, presharedKey);
        return new SpiceDBClient(protoClient);
    }

    private static void ValidateArgs(string endpoint, string presharedKey)
    {
        if (string.IsNullOrEmpty(endpoint))
            throw new ArgumentException("Endpoint must not be empty.", nameof(endpoint));
        if (string.IsNullOrEmpty(presharedKey))
            throw new ArgumentException("Preshared key must not be empty.", nameof(presharedKey));
    }

    public ValueTask DisposeAsync()
    {
        _protoClient?.Dispose();
        return ValueTask.CompletedTask;
    }

    // ──────────────────────────────────────────────────────────────────────
    // Checks — all via BulkCheckPermissions
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Performs a bulk permission check on the given relationships and returns
    /// a <see cref="CheckResult"/> for each relationship. All checks use
    /// BulkCheckPermissions under the hood.
    /// <para>
    /// <see cref="CheckResult.Permissionship"/> carries the server's
    /// four-valued answer — a <see cref="Client.Permissionship.ConditionalPermission"/>
    /// result means the server needed caveat context that was not supplied,
    /// and is NOT a grant. Prefer <see cref="CheckResult.HasPermission"/>
    /// over comparing <see cref="CheckResult.Permissionship"/> directly for
    /// the common case.
    /// </para>
    /// <para>
    /// This overload sends no call-level default caveat context. Any
    /// relationship built with <see cref="Relationship.WithCheckContext"/>
    /// still supplies its own per-item context through this method — see
    /// <see cref="CheckPermissionsWithContextAsync"/> for a call-level
    /// default and the exact per-key merge rule with per-item context.
    /// </para>
    /// </summary>
    public async Task<CheckResult[]> CheckPermissionsAsync(
        ConsistencyStrategy consistency,
        string permission,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships) =>
        await CheckPermissionsCoreAsync(consistency, permission, null, cancellationToken, relationships);

    /// <summary>
    /// <see cref="CheckPermissionsAsync"/> with a call-level default caveat
    /// context applied to every relationship in <paramref name="relationships"/>.
    /// Caveat context supplies named values (e.g. "now") that SpiceDB needs
    /// to evaluate a caveat expression encountered during the check; without
    /// it, a caveated match comes back as
    /// <see cref="Client.Permissionship.ConditionalPermission"/> instead of a
    /// grant, and <see cref="CheckResult.MissingContext"/> names what was
    /// needed.
    /// <para>
    /// A relationship built with <see cref="Relationship.WithCheckContext"/>
    /// overrides <paramref name="context"/> on a per-key basis for that one
    /// relationship: the item's own keys win on conflict, and any call-level
    /// keys the item doesn't specify are retained. For example, a call-level
    /// <c>{"now": 42, "region": "us"}</c> plus a per-item
    /// <c>{"region": "eu"}</c> sends <c>{"now": 42, "region": "eu"}</c> for
    /// that item, while a sibling item with no per-item context still gets
    /// the untouched call-level default. Pass a <c>null</c>
    /// <paramref name="context"/> for no call-level default (equivalent to
    /// calling <see cref="CheckPermissionsAsync"/>).
    /// </para>
    /// <para>
    /// The wire has no request-level context field — only
    /// <c>CheckBulkPermissionsRequestItem.context</c> (per item) — so
    /// <paramref name="context"/> is fanned out onto every item at
    /// request-build time. When neither the call-level nor an item's own
    /// context is supplied, no context field is set on that item's wire
    /// representation (never an empty <c>Struct</c>).
    /// </para>
    /// </summary>
    public async Task<CheckResult[]> CheckPermissionsWithContextAsync(
        ConsistencyStrategy consistency,
        string permission,
        IReadOnlyDictionary<string, object>? context,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships) =>
        await CheckPermissionsCoreAsync(consistency, permission, context, cancellationToken, relationships);

    /// <summary>
    /// Shared implementation behind <see cref="CheckPermissionsAsync"/>,
    /// <see cref="CheckPermissionsWithContextAsync"/>,
    /// <see cref="CheckPermissionAsync"/>, <see cref="CheckAnyAsync"/>/
    /// <see cref="CheckAnyWithContextAsync"/>, and
    /// <see cref="CheckAllAsync"/>/<see cref="CheckAllWithContextAsync"/> —
    /// all of them are aggregates or pass-throughs over the same
    /// CheckBulkPermissions request, so the request-building, per-item
    /// context merge, and response-mapping logic lives here once.
    /// </summary>
    private async Task<CheckResult[]> CheckPermissionsCoreAsync(
        ConsistencyStrategy consistency,
        string permission,
        IReadOnlyDictionary<string, object>? context,
        CancellationToken cancellationToken,
        Relationship[] relationships)
    {
        ArgumentNullException.ThrowIfNull(consistency);
        if (string.IsNullOrEmpty(permission))
            throw new ArgumentException("Permission must not be empty.", nameof(permission));
        if (relationships.Length == 0)
            return [];

        var items = relationships.Select(r => CheckItemFromRel(r, permission, context)).ToList();

        var resp = await RetryAsync(async () =>
            await _permissions.CheckBulkPermissionsAsync(
                new CheckBulkPermissionsRequest
                {
                    Consistency = consistency.V1Consistency,
                    Items = { items },
                },
                cancellationToken: cancellationToken),
            cancellationToken);

        // checked_at lives once on the response and applies to every pair —
        // CheckBulkPermissionsResponseItem has no per-item token of its own.
        var checkedAt = resp.CheckedAt?.Token ?? "";

        var results = new CheckResult[resp.Pairs.Count];
        for (var i = 0; i < resp.Pairs.Count; i++)
        {
            var pair = resp.Pairs[i];
            if (pair.Error != null)
            {
                // pair.Error is a google.rpc.Status (numeric code + message),
                // not a thrown RpcException. Synthesize one so the per-item
                // error routes through the same ErrorMapper switch as every
                // other RPC in this client, instead of discarding the code
                // and throwing the base SpiceDBException.
                var status = new Status((StatusCode)pair.Error.Code, pair.Error.Message);
                throw ErrorMapper.ToSpiceDBException(new RpcException(status));
            }
            results[i] = ToCheckResult(pair.Item, checkedAt);
        }
        return results;
    }

    /// <summary>
    /// Checks a single permission and returns its <see cref="CheckResult"/>.
    /// <paramref name="context"/> is an optional caveat context supplied for
    /// this one check — see
    /// <see cref="CheckPermissionsWithContextAsync"/> for the merge rule
    /// with any per-item context on <paramref name="relationship"/> itself
    /// (via <see cref="Relationship.WithCheckContext"/>); for a single check
    /// this mostly reads as "either supplies context," but it keeps the same
    /// shape usable across all four check methods.
    /// </summary>
    public async Task<CheckResult> CheckPermissionAsync(
        ConsistencyStrategy consistency,
        string permission,
        Relationship relationship,
        CancellationToken cancellationToken = default,
        IReadOnlyDictionary<string, object>? context = null)
    {
        var results = await CheckPermissionsCoreAsync(consistency, permission, context, cancellationToken, [relationship]);
        return results[0];
    }

    /// <summary>
    /// Returns true if any of the given relationships have the permission
    /// outright. A <see cref="Client.Permissionship.ConditionalPermission"/>
    /// result does not count as granted — only
    /// <see cref="CheckResult.HasPermission"/> results are considered.
    /// </summary>
    public async Task<bool> CheckAnyAsync(
        ConsistencyStrategy consistency,
        string permission,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships)
    {
        var results = await CheckPermissionsCoreAsync(consistency, permission, null, cancellationToken, relationships);
        return results.Any(r => r.HasPermission);
    }

    /// <summary>
    /// <see cref="CheckAnyAsync"/> with a call-level default caveat context
    /// (see <see cref="CheckPermissionsWithContextAsync"/> for the merge
    /// rule with any per-item context).
    /// </summary>
    public async Task<bool> CheckAnyWithContextAsync(
        ConsistencyStrategy consistency,
        string permission,
        IReadOnlyDictionary<string, object>? context,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships)
    {
        var results = await CheckPermissionsCoreAsync(consistency, permission, context, cancellationToken, relationships);
        return results.Any(r => r.HasPermission);
    }

    /// <summary>
    /// Returns true if all of the given relationships have the permission
    /// outright. A <see cref="Client.Permissionship.ConditionalPermission"/>
    /// result does not count as granted — every result must satisfy
    /// <see cref="CheckResult.HasPermission"/> for this to return true.
    /// </summary>
    public async Task<bool> CheckAllAsync(
        ConsistencyStrategy consistency,
        string permission,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships)
    {
        var results = await CheckPermissionsCoreAsync(consistency, permission, null, cancellationToken, relationships);
        return results.All(r => r.HasPermission);
    }

    /// <summary>
    /// <see cref="CheckAllAsync"/> with a call-level default caveat context
    /// (see <see cref="CheckPermissionsWithContextAsync"/> for the merge
    /// rule with any per-item context).
    /// </summary>
    public async Task<bool> CheckAllWithContextAsync(
        ConsistencyStrategy consistency,
        string permission,
        IReadOnlyDictionary<string, object>? context,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships)
    {
        var results = await CheckPermissionsCoreAsync(consistency, permission, context, cancellationToken, relationships);
        return results.All(r => r.HasPermission);
    }

    // ──────────────────────────────────────────────────────────────────────
    // Relationships — Write, Read, Delete
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Commits a transaction of relationship mutations to SpiceDB, returning
    /// the revision at which the write occurred.
    /// </summary>
    public async Task<string> WriteAsync(
        Transaction transaction,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(transaction);

        var req = new WriteRelationshipsRequest();
        req.Updates.AddRange(transaction.V1Updates);
        if (transaction.Preconditions.Count > 0)
            req.OptionalPreconditions.AddRange(transaction.Preconditions);

        var resp = await RetryAsync(async () =>
            await _permissions.WriteRelationshipsAsync(
                req,
                cancellationToken: cancellationToken),
            cancellationToken);

        return resp.WrittenAt?.Token ?? "";
    }

    /// <summary>
    /// Returns an async enumerable of relationships matching the given filter.
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 relationships using the AfterResultCursor.
    /// <para>
    /// Stream/page ESTABLISHMENT is retried on transient errors (the same
    /// {UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED} predicate and backoff/attempt
    /// budget as unary calls), with the attempt budget reset for each new
    /// page. Once any item has been yielded from the current page's open
    /// stream, a transient error is mapped and rethrown instead of retried —
    /// retrying after a yield would risk re-delivering already-yielded items.
    /// </para>
    /// </summary>
    public async IAsyncEnumerable<Relationship> ReadRelationshipsAsync(
        ConsistencyStrategy consistency,
        Filter filter,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);
        ArgumentNullException.ThrowIfNull(filter);

        Cursor? cursor = null;
        while (true)
        {
            var req = new ReadRelationshipsRequest
            {
                Consistency = consistency.V1Consistency,
                RelationshipFilter = filter.ToProto(),
                OptionalLimit = DefaultReadPageSize,
            };
            if (cursor != null)
                req.OptionalCursor = cursor;

            uint count = 0;
            var attempt = 0;
            var backoff = InitialBackoff;
            var pageComplete = false;

            while (!pageComplete)
            {
                AsyncServerStreamingCall<ReadRelationshipsResponse> call;
                try
                {
                    call = _permissions.ReadRelationships(req, cancellationToken: cancellationToken);
                }
                catch (RpcException ex) when (attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
                {
                    attempt++;
                    await Task.Delay(backoff, cancellationToken);
                    backoff *= 2;
                    continue;
                }
                catch (RpcException ex)
                {
                    throw ErrorMapper.ToSpiceDBException(ex);
                }

                using var stream = call;
                uint yielded = 0;

                while (true)
                {
                    ReadRelationshipsResponse? resp = null;
                    bool hasNext;
                    try
                    {
                        hasNext = await stream.ResponseStream.MoveNext(cancellationToken);
                        if (hasNext)
                            resp = stream.ResponseStream.Current;
                    }
                    // Establishment retry only: safe while nothing has been
                    // yielded yet from this page's current open stream.
                    catch (RpcException ex) when (yielded == 0 && attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
                    {
                        attempt++;
                        break;
                    }
                    catch (RpcException ex)
                    {
                        throw ErrorMapper.ToSpiceDBException(ex);
                    }

                    if (!hasNext)
                    {
                        pageComplete = true;
                        break;
                    }

                    count++;
                    yielded++;
                    cursor = resp!.AfterResultCursor;
                    yield return Relationship.FromProto(resp.Relationship);
                }

                if (!pageComplete)
                {
                    await Task.Delay(backoff, cancellationToken);
                    backoff *= 2;
                }
            }

            if (count < DefaultReadPageSize)
                yield break;
        }
    }

    /// <summary>
    /// Deletes all relationships matching the given filter. Large result sets
    /// are automatically paged in batches of 1,000 (override with
    /// <paramref name="limit"/>). Returns the revision of the final deletion.
    /// <para>
    /// <paramref name="mustMatch"/>/<paramref name="mustNotMatch"/> add
    /// preconditions that guard the delete: if a precondition fails, the
    /// server rejects that call and deletes nothing for it. Preconditions are
    /// a per-request proto field, so when a delete spans multiple pages
    /// (i.e. more matches than the page size), they are re-evaluated by the
    /// server on every page — there is no "check-once, apply-to-all-pages"
    /// semantics. This means a delete that starts successfully can still fail
    /// partway through if the guarded state changes between pages, after
    /// earlier pages have already been deleted. For a single-shot,
    /// all-or-nothing guarded delete, pair the precondition with a
    /// <paramref name="limit"/> large enough to cover every matching
    /// relationship in one call.
    /// </para>
    /// <para>
    /// Mirrors spicedb-go's <c>WithDeleteMustMatch</c>/
    /// <c>WithDeleteMustNotMatch</c>/<c>WithDeleteLimit</c>
    /// (client/relationships.go). Additive — existing
    /// <c>DeleteRelationshipsAsync(filter)</c> calls are unaffected: no
    /// preconditions, 1,000-item page size, partial deletions allowed, same
    /// as before.
    /// </para>
    /// </summary>
    public async Task<string> DeleteRelationshipsAsync(
        Filter filter,
        IReadOnlyList<Filter>? mustMatch = null,
        IReadOnlyList<Filter>? mustNotMatch = null,
        uint? limit = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(filter);

        var preconditions = new List<Precondition>();
        if (mustMatch != null)
        {
            foreach (var f in mustMatch)
                preconditions.Add(Transaction.BuildPrecondition(Precondition.Types.Operation.MustMatch, f));
        }
        if (mustNotMatch != null)
        {
            foreach (var f in mustNotMatch)
                preconditions.Add(Transaction.BuildPrecondition(Precondition.Types.Operation.MustNotMatch, f));
        }

        uint pageSize = limit ?? DefaultDeletePageSize;

        string revision = "";
        while (true)
        {
            var req = new DeleteRelationshipsRequest
            {
                RelationshipFilter = filter.ToProto(),
                OptionalLimit = pageSize,
                OptionalAllowPartialDeletions = true,
            };
            if (preconditions.Count > 0)
                req.OptionalPreconditions.AddRange(preconditions);

            var resp = await RetryAsync(async () =>
                await _permissions.DeleteRelationshipsAsync(
                    req,
                    cancellationToken: cancellationToken),
                cancellationToken);

            revision = resp.DeletedAt?.Token ?? "";

            if (resp.DeletionProgress == DeleteRelationshipsResponse.Types.DeletionProgress.Complete)
                return revision;
        }
    }

    // ──────────────────────────────────────────────────────────────────────
    // Lookups
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Returns an async enumerable of resources of the given type that the
    /// subject has the specified permission on. Each result carries the
    /// permissionship (full grant vs conditional on caveat context) and, for
    /// conditional results, which caveat context was missing. Cursors are
    /// handled transparently with 512-item pages.
    /// <para>
    /// Stream/page ESTABLISHMENT is retried on transient errors, with the
    /// attempt budget reset for each new page. Once any item has been
    /// yielded from the current page's open stream, a transient error is
    /// mapped and rethrown instead of retried — see
    /// <see cref="ReadRelationshipsAsync"/> for the full rationale.
    /// </para>
    /// </summary>
    public async IAsyncEnumerable<LookupResource> LookupResourcesAsync(
        ConsistencyStrategy consistency,
        string resourceType,
        string permission,
        string subjectType,
        string subjectID,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        Cursor? cursor = null;
        while (true)
        {
            var req = new LookupResourcesRequest
            {
                Consistency = consistency.V1Consistency,
                ResourceObjectType = resourceType,
                Permission = permission,
                Subject = new SubjectReference
                {
                    Object = new ObjectReference
                    {
                        ObjectType = subjectType,
                        ObjectId = subjectID,
                    },
                },
                OptionalLimit = DefaultLookupPageSize,
            };
            if (cursor != null)
                req.OptionalCursor = cursor;

            int count = 0;
            var attempt = 0;
            var backoff = InitialBackoff;
            var pageComplete = false;

            while (!pageComplete)
            {
                AsyncServerStreamingCall<LookupResourcesResponse> call;
                try
                {
                    call = _permissions.LookupResources(req, cancellationToken: cancellationToken);
                }
                catch (RpcException ex) when (attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
                {
                    attempt++;
                    await Task.Delay(backoff, cancellationToken);
                    backoff *= 2;
                    continue;
                }
                catch (RpcException ex)
                {
                    throw ErrorMapper.ToSpiceDBException(ex);
                }

                using var stream = call;
                int yielded = 0;

                while (true)
                {
                    LookupResourcesResponse? resp = null;
                    bool hasNext;
                    try
                    {
                        hasNext = await stream.ResponseStream.MoveNext(cancellationToken);
                        if (hasNext)
                            resp = stream.ResponseStream.Current;
                    }
                    catch (RpcException ex) when (yielded == 0 && attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
                    {
                        attempt++;
                        break;
                    }
                    catch (RpcException ex)
                    {
                        throw ErrorMapper.ToSpiceDBException(ex);
                    }

                    if (!hasNext)
                    {
                        pageComplete = true;
                        break;
                    }

                    count++;
                    yielded++;
                    cursor = resp!.AfterResultCursor;
                    yield return new LookupResource
                    {
                        ResourceID = resp.ResourceObjectId,
                        Permissionship = ToPermissionship(resp.Permissionship),
                        PartialCaveat = ToPartialCaveatInfo(resp.PartialCaveatInfo),
                        LookedUpAt = resp.LookedUpAt?.Token ?? "",
                    };
                }

                if (!pageComplete)
                {
                    await Task.Delay(backoff, cancellationToken);
                    backoff *= 2;
                }
            }

            if (count < DefaultLookupPageSize)
                yield break;
        }
    }

    /// <summary>
    /// Returns an async enumerable of subjects of the given type that have
    /// the specified permission on the resource. Unlike LookupResources,
    /// LookupSubjects does not currently support cursor-based pagination in
    /// SpiceDB and streams all results in a single server-streaming call.
    /// <para>
    /// When a yielded <see cref="LookupSubject.Subject"/> is the wildcard
    /// "*", the server has granted the permission to every subject of
    /// <paramref name="subjectType"/> EXCEPT those listed in
    /// <see cref="LookupSubject.ExcludedSubjects"/>. Callers MUST check
    /// <see cref="LookupSubject.ExcludedSubjects"/> before treating a
    /// wildcard match as a blanket grant, or they risk granting access to
    /// subjects the server explicitly excluded.
    /// </para>
    /// <para>
    /// Since there is one stream (no pages), the retry-on-transient-error
    /// behavior applies to that single ESTABLISHMENT only: once any subject
    /// has been yielded, a transient error is mapped and rethrown instead of
    /// retried — see <see cref="ReadRelationshipsAsync"/> for the full
    /// rationale.
    /// </para>
    /// </summary>
    public async IAsyncEnumerable<LookupSubject> LookupSubjectsAsync(
        ConsistencyStrategy consistency,
        string resourceType,
        string resourceID,
        string permission,
        string subjectType,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        var req = new LookupSubjectsRequest
        {
            Consistency = consistency.V1Consistency,
            Resource = new ObjectReference
            {
                ObjectType = resourceType,
                ObjectId = resourceID,
            },
            Permission = permission,
            SubjectObjectType = subjectType,
        };

        var attempt = 0;
        var backoff = InitialBackoff;
        var yielded = 0;

        // Establishment-retry loop: LookupSubjects has no cursor support, so
        // there's a single logical stream — retry re-opens it (not a page).
        // Once anything has been yielded, retrying is never safe.
        while (true)
        {
            AsyncServerStreamingCall<LookupSubjectsResponse> call;
            try
            {
                call = _permissions.LookupSubjects(req, cancellationToken: cancellationToken);
            }
            catch (RpcException ex) when (yielded == 0 && attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
            {
                attempt++;
                await Task.Delay(backoff, cancellationToken);
                backoff *= 2;
                continue;
            }
            catch (RpcException ex)
            {
                throw ErrorMapper.ToSpiceDBException(ex);
            }

            using var stream = call;

            while (true)
            {
                LookupSubjectsResponse? resp = null;
                bool hasNext;
                try
                {
                    hasNext = await stream.ResponseStream.MoveNext(cancellationToken);
                    if (hasNext)
                        resp = stream.ResponseStream.Current;
                }
                catch (RpcException ex) when (yielded == 0 && attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
                {
                    attempt++;
                    break;
                }
                catch (RpcException ex)
                {
                    throw ErrorMapper.ToSpiceDBException(ex);
                }

                if (!hasNext)
                    yield break;

                yielded++;
                yield return ToLookupSubject(resp!);
            }

            // Reached only via the transient-retry break above.
            await Task.Delay(backoff, cancellationToken);
            backoff *= 2;
        }
    }

    // ──────────────────────────────────────────────────────────────────────
    // Schema
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Returns the current SpiceDB schema and the revision it was read at.
    /// </summary>
    public async Task<(string Schema, string Revision)> ReadSchemaAsync(
        CancellationToken cancellationToken = default)
    {
        var resp = await RetryAsync(async () =>
            await _schema.ReadSchemaAsync(
                new ReadSchemaRequest(),
                cancellationToken: cancellationToken),
            cancellationToken);

        return (resp.SchemaText, resp.ReadAt?.Token ?? "");
    }

    /// <summary>
    /// Writes a new schema to SpiceDB, returning the revision.
    /// </summary>
    public async Task<string> WriteSchemaAsync(
        string schema,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(schema))
            throw new ArgumentException("Schema must not be empty.", nameof(schema));

        var resp = await RetryAsync(async () =>
            await _schema.WriteSchemaAsync(
                new WriteSchemaRequest { Schema = schema },
                cancellationToken: cancellationToken),
            cancellationToken);

        return resp.WrittenAt?.Token ?? "";
    }

    /// <summary>
    /// Returns the definitions and caveats in the current schema.
    /// </summary>
    public async Task<ReflectSchemaResult> ReflectSchemaAsync(
        ConsistencyStrategy consistency,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        var resp = await RetryAsync(async () =>
            await _schema.ReflectSchemaAsync(
                new ReflectSchemaRequest { Consistency = consistency.V1Consistency },
                cancellationToken: cancellationToken),
            cancellationToken);

        var result = new ReflectSchemaResult
        {
            Revision = resp.ReadAt?.Token ?? "",
        };

        foreach (var def in resp.Definitions)
        {
            var sd = new SchemaDefinition
            {
                Name = def.Name,
                Comment = def.Comment,
                Relations = def.Relations.Select(r => new SchemaRelation
                {
                    Name = r.Name,
                    Comment = r.Comment,
                    ParentDefinitionName = r.ParentDefinitionName,
                }).ToList(),
                Permissions = def.Permissions.Select(p => new SchemaPermission
                {
                    Name = p.Name,
                    Comment = p.Comment,
                    ParentDefinitionName = p.ParentDefinitionName,
                }).ToList(),
            };
            result.Definitions.Add(sd);
        }

        foreach (var cav in resp.Caveats)
        {
            var sc = new SchemaCaveat
            {
                Name = cav.Name,
                Comment = cav.Comment,
                Expression = cav.Expression,
                Parameters = cav.Parameters.Select(p => new SchemaCaveatParameter
                {
                    Name = p.Name,
                    Type = p.Type,
                    ParentCaveatName = p.ParentCaveatName,
                }).ToList(),
            };
            result.Caveats.Add(sc);
        }

        return result;
    }

    /// <summary>
    /// Returns the permissions that are computable for the given relation on a definition.
    /// </summary>
    public async Task<(IReadOnlyList<RelationReference> Permissions, string Revision)> ComputablePermissionsAsync(
        ConsistencyStrategy consistency,
        string definitionName,
        string relationName,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        var resp = await RetryAsync(async () =>
            await _schema.ComputablePermissionsAsync(
                new ComputablePermissionsRequest
                {
                    Consistency = consistency.V1Consistency,
                    DefinitionName = definitionName,
                    RelationName = relationName,
                },
                cancellationToken: cancellationToken),
            cancellationToken);

        var refs = resp.Permissions.Select(p => new RelationReference
        {
            DefinitionName = p.DefinitionName,
            RelationName = p.RelationName,
            IsPermission = p.IsPermission,
        }).ToList();

        return (refs, resp.ReadAt?.Token ?? "");
    }

    /// <summary>
    /// Returns the relations that the given permission depends on.
    /// </summary>
    public async Task<(IReadOnlyList<RelationReference> Relations, string Revision)> DependentRelationsAsync(
        ConsistencyStrategy consistency,
        string definitionName,
        string permissionName,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        var resp = await RetryAsync(async () =>
            await _schema.DependentRelationsAsync(
                new DependentRelationsRequest
                {
                    Consistency = consistency.V1Consistency,
                    DefinitionName = definitionName,
                    PermissionName = permissionName,
                },
                cancellationToken: cancellationToken),
            cancellationToken);

        var refs = resp.Relations.Select(r => new RelationReference
        {
            DefinitionName = r.DefinitionName,
            RelationName = r.RelationName,
            IsPermission = r.IsPermission,
        }).ToList();

        return (refs, resp.ReadAt?.Token ?? "");
    }

    /// <summary>
    /// Compares the current schema against the given comparison schema,
    /// returning the differences.
    /// </summary>
    public async Task<(IReadOnlyList<SchemaDiff> Diffs, string Revision)> DiffSchemaAsync(
        ConsistencyStrategy consistency,
        string comparisonSchema,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        var resp = await RetryAsync(async () =>
            await _schema.DiffSchemaAsync(
                new DiffSchemaRequest
                {
                    Consistency = consistency.V1Consistency,
                    ComparisonSchema = comparisonSchema,
                },
                cancellationToken: cancellationToken),
            cancellationToken);

        var diffs = resp.Diffs.Select(SchemaDiff.FromProto).ToList();
        return (diffs, resp.ReadAt?.Token ?? "");
    }

    // ──────────────────────────────────────────────────────────────────────
    // Expand
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Expands the permission tree for the given resource and permission,
    /// returning the full tree of subjects with access.
    /// </summary>
    public async Task<ExpandResult> ExpandPermissionTreeAsync(
        ConsistencyStrategy consistency,
        string resourceType,
        string resourceID,
        string permission,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        var resp = await RetryAsync(async () =>
            await _permissions.ExpandPermissionTreeAsync(
                new ExpandPermissionTreeRequest
                {
                    Consistency = consistency.V1Consistency,
                    Resource = new ObjectReference
                    {
                        ObjectType = resourceType,
                        ObjectId = resourceID,
                    },
                    Permission = permission,
                },
                cancellationToken: cancellationToken),
            cancellationToken);

        return new ExpandResult
        {
            Tree = ToPermissionTree(resp.TreeRoot),
            Revision = resp.ExpandedAt?.Token ?? "",
        };
    }

    // ──────────────────────────────────────────────────────────────────────
    // Bulk Import / Export
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Streams relationships to SpiceDB for bulk import, returning the number
    /// of relationships loaded. Relationships are automatically batched into
    /// chunks of 1,000.
    /// </summary>
    public async Task<ulong> ImportRelationshipsAsync(
        IAsyncEnumerable<Relationship> relationships,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(relationships);

        AsyncClientStreamingCall<ImportBulkRelationshipsRequest, ImportBulkRelationshipsResponse> stream;
        try
        {
            stream = _permissions.ImportBulkRelationships(cancellationToken: cancellationToken);
        }
        catch (RpcException ex)
        {
            throw ErrorMapper.ToSpiceDBException(ex);
        }

        using (stream)
        {
            try
            {
                var batch = new List<Authzed.Api.V1.Relationship>(DefaultImportBatchSize);

                await foreach (var rel in relationships.WithCancellation(cancellationToken))
                {
                    batch.Add(rel.ToProto());
                    if (batch.Count >= DefaultImportBatchSize)
                    {
                        var req = new ImportBulkRelationshipsRequest();
                        req.Relationships.AddRange(batch);
                        await stream.RequestStream.WriteAsync(req, cancellationToken);
                        batch.Clear();
                    }
                }

                if (batch.Count > 0)
                {
                    var req = new ImportBulkRelationshipsRequest();
                    req.Relationships.AddRange(batch);
                    await stream.RequestStream.WriteAsync(req, cancellationToken);
                }

                await stream.RequestStream.CompleteAsync();
                var resp = await stream;
                return resp.NumLoaded;
            }
            catch (RpcException ex)
            {
                throw ErrorMapper.ToSpiceDBException(ex);
            }
        }
    }

    /// <summary>
    /// Returns an async enumerable over all relationships matching the optional
    /// filter, streamed from SpiceDB in bulk. Cursors are handled transparently
    /// with 512-item pages.
    /// <para>
    /// Stream/page ESTABLISHMENT is retried on transient errors, with the
    /// attempt budget reset for each new page. Once any relationship has been
    /// yielded from the current page's open stream, a transient error is
    /// mapped and rethrown instead of retried — see
    /// <see cref="ReadRelationshipsAsync"/> for the full rationale.
    /// </para>
    /// </summary>
    public async IAsyncEnumerable<Relationship> ExportRelationshipsAsync(
        ConsistencyStrategy consistency,
        Filter? filter = null,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        Cursor? cursor = null;
        while (true)
        {
            var req = new ExportBulkRelationshipsRequest
            {
                Consistency = consistency.V1Consistency,
                OptionalLimit = DefaultExportPageSize,
            };
            if (cursor != null)
                req.OptionalCursor = cursor;
            if (filter != null)
                req.OptionalRelationshipFilter = filter.ToProto();

            int pageCount = 0;
            var attempt = 0;
            var backoff = InitialBackoff;
            var pageComplete = false;

            while (!pageComplete)
            {
                AsyncServerStreamingCall<ExportBulkRelationshipsResponse> call;
                try
                {
                    call = _permissions.ExportBulkRelationships(req, cancellationToken: cancellationToken);
                }
                catch (RpcException ex) when (attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
                {
                    attempt++;
                    await Task.Delay(backoff, cancellationToken);
                    backoff *= 2;
                    continue;
                }
                catch (RpcException ex)
                {
                    throw ErrorMapper.ToSpiceDBException(ex);
                }

                using var stream = call;
                int yielded = 0;

                while (true)
                {
                    ExportBulkRelationshipsResponse? resp = null;
                    bool hasNext;
                    try
                    {
                        hasNext = await stream.ResponseStream.MoveNext(cancellationToken);
                        if (hasNext)
                            resp = stream.ResponseStream.Current;
                    }
                    catch (RpcException ex) when (yielded == 0 && attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
                    {
                        attempt++;
                        break;
                    }
                    catch (RpcException ex)
                    {
                        throw ErrorMapper.ToSpiceDBException(ex);
                    }

                    if (!hasNext)
                    {
                        pageComplete = true;
                        break;
                    }

                    cursor = resp!.AfterResultCursor;
                    foreach (var r in resp.Relationships)
                    {
                        pageCount++;
                        yielded++;
                        yield return Relationship.FromProto(r);
                    }
                }

                if (!pageComplete)
                {
                    await Task.Delay(backoff, cancellationToken);
                    backoff *= 2;
                }
            }

            if (pageCount < DefaultExportPageSize)
                yield break;
        }
    }

    // ──────────────────────────────────────────────────────────────────────
    // Watch
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Returns an async enumerable of relationship changes from SpiceDB's watch
    /// API, starting from the given revision.
    /// <para>
    /// Watch ESTABLISHMENT is retried on transient errors — but only up until
    /// the first update is yielded. Once anything has been yielded from the
    /// current watch stream, a transient error is mapped and rethrown instead
    /// of retried; retrying mid-watch would risk re-delivering already-seen
    /// updates (or silently skipping ones the caller never saw), and there is
    /// no cursor to safely resume from mid-stream. See
    /// <see cref="ReadRelationshipsAsync"/> for the full rationale.
    /// </para>
    /// </summary>
    public async IAsyncEnumerable<RelationshipUpdate> UpdatesAsync(
        IEnumerable<string>? objectTypes = null,
        string? startRevision = null,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        var req = new WatchRequest();
        if (objectTypes != null)
            req.OptionalObjectTypes.AddRange(objectTypes);
        if (!string.IsNullOrEmpty(startRevision))
            req.OptionalStartCursor = new ZedToken { Token = startRevision };

        var attempt = 0;
        var backoff = InitialBackoff;
        var yielded = 0;

        // Establishment-retry loop: retry re-opening the watch only while
        // nothing has been yielded yet — never mid-watch.
        while (true)
        {
            AsyncServerStreamingCall<WatchResponse> call;
            try
            {
                call = _watch.Watch(req, cancellationToken: cancellationToken);
            }
            catch (RpcException ex) when (yielded == 0 && attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
            {
                attempt++;
                await Task.Delay(backoff, cancellationToken);
                backoff *= 2;
                continue;
            }
            catch (RpcException ex)
            {
                throw ErrorMapper.ToSpiceDBException(ex);
            }

            using var stream = call;

            while (true)
            {
                WatchResponse? resp = null;
                bool hasNext;
                try
                {
                    hasNext = await stream.ResponseStream.MoveNext(cancellationToken);
                    if (hasNext)
                        resp = stream.ResponseStream.Current;
                }
                catch (RpcException ex) when (yielded == 0 && attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
                {
                    attempt++;
                    break;
                }
                catch (RpcException ex)
                {
                    throw ErrorMapper.ToSpiceDBException(ex);
                }

                if (!hasNext)
                    yield break;

                foreach (var update in resp!.Updates)
                {
                    yielded++;
                    yield return UpdateFromProto(update);
                }
            }

            // Reached only via the transient-retry break above.
            await Task.Delay(backoff, cancellationToken);
            backoff *= 2;
        }
    }

    // ──────────────────────────────────────────────────────────────────────
    // Experimental — Relationship Counters
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Registers a named counter that tracks relationships matching the given
    /// filter. The counter is computed asynchronously by SpiceDB.
    /// <para>
    /// <b>Experimental:</b> This API may change without following the backwards
    /// compatibility mandate.
    /// </para>
    /// </summary>
    public async Task ExperimentalRegisterRelationshipCounterAsync(
        string name,
        Filter filter,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(name))
            throw new ArgumentException("Name must not be empty.", nameof(name));
        ArgumentNullException.ThrowIfNull(filter);

        await RetryAsync(async () =>
            await _experimental.ExperimentalRegisterRelationshipCounterAsync(
                new ExperimentalRegisterRelationshipCounterRequest
                {
                    Name = name,
                    RelationshipFilter = filter.ToProto(),
                },
                cancellationToken: cancellationToken),
            cancellationToken);
    }

    /// <summary>
    /// Reads the value of a previously registered relationship counter. Returns
    /// the count result (null if still calculating) and whether the counter is
    /// still being calculated.
    /// <para>
    /// <b>Experimental:</b> This API may change without following the backwards
    /// compatibility mandate.
    /// </para>
    /// </summary>
    public async Task<(CountResult? Result, bool StillCalculating)> ExperimentalCountRelationshipsAsync(
        string name,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(name))
            throw new ArgumentException("Name must not be empty.", nameof(name));

        var resp = await RetryAsync(async () =>
            await _experimental.ExperimentalCountRelationshipsAsync(
                new ExperimentalCountRelationshipsRequest { Name = name },
                cancellationToken: cancellationToken),
            cancellationToken);

        if (resp.CounterStillCalculating)
            return (null, true);

        var cv = resp.ReadCounterValue;
        return (new CountResult
        {
            RelationshipCount = cv.RelationshipCount,
            Revision = cv.ReadAt?.Token ?? "",
        }, false);
    }

    /// <summary>
    /// Removes a previously registered relationship counter.
    /// <para>
    /// <b>Experimental:</b> This API may change without following the backwards
    /// compatibility mandate.
    /// </para>
    /// </summary>
    public async Task ExperimentalUnregisterRelationshipCounterAsync(
        string name,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(name))
            throw new ArgumentException("Name must not be empty.", nameof(name));

        await RetryAsync(async () =>
            await _experimental.ExperimentalUnregisterRelationshipCounterAsync(
                new ExperimentalUnregisterRelationshipCounterRequest { Name = name },
                cancellationToken: cancellationToken),
            cancellationToken);
    }

    // ──────────────────────────────────────────────────────────────────────
    // Internals
    // ──────────────────────────────────────────────────────────────────────

    private static CheckBulkPermissionsRequestItem CheckItemFromRel(
        Relationship r, string permission, IReadOnlyDictionary<string, object>? callLevelContext)
    {
        var item = new CheckBulkPermissionsRequestItem
        {
            Resource = new ObjectReference
            {
                ObjectType = r.ResourceType,
                ObjectId = r.ResourceID,
            },
            Permission = permission,
            Subject = new SubjectReference
            {
                Object = new ObjectReference
                {
                    ObjectType = r.SubjectType,
                    ObjectId = r.SubjectID,
                },
                OptionalRelation = r.SubjectRelation,
            },
        };

        var context = MergeCheckContext(callLevelContext, r.CheckContext);
        if (context != null)
            item.Context = context;

        return item;
    }

    /// <summary>
    /// Merges call-level and per-item check-time caveat context per the
    /// key-level merge rule: the item's own keys win on conflict, and any
    /// call-level keys the item doesn't specify are retained. Returns
    /// <c>null</c> (no context field to set on the wire) when both are
    /// null/empty — deliberately never an empty <see cref="Struct"/>, so a
    /// caller who supplies no context at all sees no <c>context</c> field on
    /// the built request. Internal — exposed to the test assembly via
    /// InternalsVisibleTo; not part of the public API.
    /// </summary>
    internal static Struct? MergeCheckContext(
        IReadOnlyDictionary<string, object>? callLevel,
        IReadOnlyDictionary<string, object>? item)
    {
        if ((callLevel == null || callLevel.Count == 0) && (item == null || item.Count == 0))
            return null;

        var merged = new Struct();
        if (callLevel != null)
        {
            foreach (var (key, value) in callLevel)
                merged.Fields[key] = ToProtoValue(value);
        }
        if (item != null)
        {
            // Applied after callLevel so item keys overwrite matching
            // call-level keys — this IS the "item wins" half of the merge
            // rule. Call-level keys the item doesn't mention are untouched
            // by this loop, which is the "call-level retained" half.
            foreach (var (key, value) in item)
                merged.Fields[key] = ToProtoValue(value);
        }

        return merged;
    }

    /// <summary>
    /// Converts a native .NET value into a protobuf Struct <see cref="Value"/>
    /// for check-time caveat context. Unlike <see cref="Relationship.ToProto"/>'s
    /// write-time CaveatContext conversion (which stringifies every value),
    /// this preserves numeric/bool/null/nested shapes so caveat expressions
    /// that compare typed parameters (e.g. a schema's <c>now &lt; 100</c>
    /// against an <c>int</c>) evaluate correctly instead of comparing against
    /// a string SpiceDB would reject or fail to coerce.
    /// </summary>
    internal static Value ToProtoValue(object? value) => value switch
    {
        null => Value.ForNull(),
        bool b => Value.ForBool(b),
        string s => Value.ForString(s),
        sbyte or byte or short or ushort or int or uint or long or ulong or float or double or decimal =>
            Value.ForNumber(Convert.ToDouble(value)),
        IReadOnlyDictionary<string, object> nested => Value.ForStruct(ToProtoStructFrom(nested)),
        System.Collections.IEnumerable list => Value.ForList(ToProtoValueList(list)),
        _ => Value.ForString(value.ToString() ?? ""),
    };

    private static Struct ToProtoStructFrom(IReadOnlyDictionary<string, object> dict)
    {
        var s = new Struct();
        foreach (var (key, value) in dict)
            s.Fields[key] = ToProtoValue(value);
        return s;
    }

    private static Value[] ToProtoValueList(System.Collections.IEnumerable list)
    {
        var values = new List<Value>();
        foreach (var item in list)
            values.Add(ToProtoValue(item));
        return values.ToArray();
    }

    /// <summary>
    /// Maps the proto LookupPermissionship enum to its native equivalent.
    /// Unrecognized values map to Permissionship.Unspecified. Internal —
    /// exposed to the test assembly via InternalsVisibleTo; not part of the
    /// public API.
    /// </summary>
    internal static Permissionship ToPermissionship(Authzed.Api.V1.LookupPermissionship v) => v switch
    {
        Authzed.Api.V1.LookupPermissionship.HasPermission => Permissionship.HasPermission,
        Authzed.Api.V1.LookupPermissionship.ConditionalPermission => Permissionship.ConditionalPermission,
        _ => Permissionship.Unspecified,
    };

    /// <summary>
    /// Maps a proto PartialCaveatInfo to its native equivalent. A null input
    /// maps to null.
    /// </summary>
    internal static PartialCaveatInfo? ToPartialCaveatInfo(Authzed.Api.V1.PartialCaveatInfo? v)
    {
        if (v == null)
            return null;
        return new PartialCaveatInfo { MissingRequiredContext = v.MissingRequiredContext.ToList() };
    }

    /// <summary>
    /// Maps the proto CheckPermissionResponse.Types.Permissionship enum
    /// (which, unlike LookupPermissionship, has a NoPermission value in
    /// addition to Unspecified/HasPermission/ConditionalPermission) to its
    /// native equivalent. Unrecognized values map to
    /// Permissionship.Unspecified. Internal — exposed to the test assembly
    /// via InternalsVisibleTo; not part of the public API.
    /// </summary>
    internal static Permissionship ToCheckPermissionship(CheckPermissionResponse.Types.Permissionship v) => v switch
    {
        CheckPermissionResponse.Types.Permissionship.HasPermission => Permissionship.HasPermission,
        CheckPermissionResponse.Types.Permissionship.NoPermission => Permissionship.NoPermission,
        CheckPermissionResponse.Types.Permissionship.ConditionalPermission => Permissionship.ConditionalPermission,
        _ => Permissionship.Unspecified,
    };

    /// <summary>
    /// Maps one successful CheckBulkPermissionsResponseItem pair to a native
    /// CheckResult. CheckBulkPermissionsResponseItem carries the same
    /// permissionship/partial-caveat-info fields as CheckPermissionResponse,
    /// but has no per-item checked_at of its own — the token lives once on
    /// the enclosing CheckBulkPermissionsResponse and applies to every pair
    /// in it, so callers pass it in as <paramref name="checkedAt"/> to
    /// propagate it onto each result.
    /// </summary>
    internal static CheckResult ToCheckResult(CheckBulkPermissionsResponseItem item, string checkedAt) =>
        new()
        {
            Permissionship = ToCheckPermissionship(item.Permissionship),
            MissingContext = item.PartialCaveatInfo != null
                ? item.PartialCaveatInfo.MissingRequiredContext.ToList()
                : [],
            CheckedAt = checkedAt,
        };

    /// <summary>
    /// Maps a proto ResolvedSubject to its native equivalent. A null input
    /// maps to a zero-value ResolvedSubject (empty SubjectID), which callers
    /// use as the trigger for falling back to deprecated response-level
    /// fields.
    /// </summary>
    internal static ResolvedSubject ToResolvedSubject(Authzed.Api.V1.ResolvedSubject? v)
    {
        if (v == null)
            return new ResolvedSubject();
        return new ResolvedSubject
        {
            SubjectID = v.SubjectObjectId,
            Permissionship = ToPermissionship(v.Permissionship),
            PartialCaveat = ToPartialCaveatInfo(v.PartialCaveatInfo),
        };
    }

    /// <summary>
    /// Maps a LookupSubjectsResponse to its native LookupSubject, including
    /// the deprecated-field fallbacks for both the resolved subject
    /// (`subject_object_id`/`permissionship`/`partial_caveat_info`) and the
    /// excluded subjects (`excluded_subject_ids`) for servers that don't yet
    /// populate the non-deprecated `subject`/`excluded_subjects` fields.
    /// </summary>
    internal static LookupSubject ToLookupSubject(LookupSubjectsResponse resp)
    {
        var subject = ToResolvedSubject(resp.Subject);
        if (string.IsNullOrEmpty(subject.SubjectID))
        {
#pragma warning disable CS0612 // deprecated proto fields — explicit fallback for older servers
            subject = new ResolvedSubject
            {
                SubjectID = resp.SubjectObjectId,
                Permissionship = ToPermissionship(resp.Permissionship),
                PartialCaveat = ToPartialCaveatInfo(resp.PartialCaveatInfo),
            };
#pragma warning restore CS0612
        }

        List<ResolvedSubject> excluded = [];
        if (resp.ExcludedSubjects.Count > 0)
        {
            excluded = resp.ExcludedSubjects.Select(ToResolvedSubject).ToList();
        }
        else
        {
#pragma warning disable CS0612 // deprecated proto field — explicit fallback for older servers
            if (resp.ExcludedSubjectIds.Count > 0)
                excluded = resp.ExcludedSubjectIds.Select(id => new ResolvedSubject { SubjectID = id }).ToList();
#pragma warning restore CS0612
        }

        return new LookupSubject
        {
            Subject = subject,
            ExcludedSubjects = excluded,
            LookedUpAt = resp.LookedUpAt?.Token ?? "",
        };
    }

    /// <summary>
    /// Recursively maps a proto PermissionRelationshipTree to its native
    /// representation. A null input maps to a zero-value PermissionTree.
    /// Internal — exposed to the test assembly via InternalsVisibleTo for
    /// direct field-by-field verification; not part of the public API.
    /// </summary>
    internal static PermissionTree ToPermissionTree(PermissionRelationshipTree? t)
    {
        if (t == null)
            return new PermissionTree();

        var tree = new PermissionTree
        {
            ExpandedObject = new ObjectRef
            {
                ObjectType = t.ExpandedObject?.ObjectType ?? "",
                ObjectID = t.ExpandedObject?.ObjectId ?? "",
            },
            ExpandedRelation = t.ExpandedRelation ?? "",
        };

        switch (t.TreeTypeCase)
        {
            case PermissionRelationshipTree.TreeTypeOneofCase.Intermediate:
                tree = tree with
                {
                    Intermediate = new IntermediateNode
                    {
                        Operation = ToTreeOperation(t.Intermediate.Operation),
                        Children = t.Intermediate.Children.Select(ToPermissionTree).ToList(),
                    },
                };
                break;
            case PermissionRelationshipTree.TreeTypeOneofCase.Leaf:
                tree = tree with
                {
                    Leaf = new LeafNode
                    {
                        Subjects = t.Leaf.Subjects.Select(s => new SubjectRef
                        {
                            SubjectType = s.Object?.ObjectType ?? "",
                            SubjectID = s.Object?.ObjectId ?? "",
                            OptionalRelation = s.OptionalRelation ?? "",
                        }).ToList(),
                    },
                };
                break;
        }

        return tree;
    }

    /// <summary>
    /// Maps the proto algebraic set operation to its native equivalent.
    /// </summary>
    private static TreeOperation ToTreeOperation(AlgebraicSubjectSet.Types.Operation op) => op switch
    {
        AlgebraicSubjectSet.Types.Operation.Union => TreeOperation.Union,
        AlgebraicSubjectSet.Types.Operation.Intersection => TreeOperation.Intersection,
        AlgebraicSubjectSet.Types.Operation.Exclusion => TreeOperation.Exclusion,
        _ => TreeOperation.Unspecified,
    };

    private static RelationshipUpdate UpdateFromProto(Authzed.Api.V1.RelationshipUpdate pu)
    {
        var op = pu.Operation switch
        {
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Create => UpdateOperation.Create,
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch => UpdateOperation.Touch,
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Delete => UpdateOperation.Delete,
            _ => UpdateOperation.Touch,
        };
        return new RelationshipUpdate
        {
            Operation = op,
            Relationship = Relationship.FromProto(pu.Relationship),
        };
    }

    /// <summary>
    /// Retries an async operation with exponential backoff for transient gRPC errors.
    /// </summary>
    private static async Task<T> RetryAsync<T>(
        Func<Task<T>> operation,
        CancellationToken cancellationToken)
    {
        var backoff = InitialBackoff;
        for (var attempt = 0; ; attempt++)
        {
            try
            {
                return await operation();
            }
            catch (RpcException ex) when (attempt < MaxRetryAttempts && ErrorMapper.IsTransient(ex))
            {
                await Task.Delay(backoff, cancellationToken);
                backoff *= 2;
            }
            catch (RpcException ex)
            {
                throw ErrorMapper.ToSpiceDBException(ex);
            }
        }
    }
}

// ──────────────────────────────────────────────────────────────────────
// Supporting types for schema operations
// ──────────────────────────────────────────────────────────────────────

/// <summary>Represents a definition in a SpiceDB schema.</summary>
public sealed record SchemaDefinition
{
    public string Name { get; init; } = "";
    public string Comment { get; init; } = "";
    public List<SchemaRelation> Relations { get; init; } = [];
    public List<SchemaPermission> Permissions { get; init; } = [];
}

/// <summary>Represents a relation within a schema definition.</summary>
public sealed record SchemaRelation
{
    public string Name { get; init; } = "";
    public string Comment { get; init; } = "";
    public string ParentDefinitionName { get; init; } = "";
}

/// <summary>Represents a permission within a schema definition.</summary>
public sealed record SchemaPermission
{
    public string Name { get; init; } = "";
    public string Comment { get; init; } = "";
    public string ParentDefinitionName { get; init; } = "";
}

/// <summary>Represents a caveat defined in a SpiceDB schema.</summary>
public sealed record SchemaCaveat
{
    public string Name { get; init; } = "";
    public string Comment { get; init; } = "";
    public string Expression { get; init; } = "";
    public List<SchemaCaveatParameter> Parameters { get; init; } = [];
}

/// <summary>Represents a parameter of a caveat.</summary>
public sealed record SchemaCaveatParameter
{
    public string Name { get; init; } = "";
    public string Type { get; init; } = "";
    public string ParentCaveatName { get; init; } = "";
}

/// <summary>Holds the result of a schema reflection call.</summary>
public sealed record ReflectSchemaResult
{
    public List<SchemaDefinition> Definitions { get; init; } = [];
    public List<SchemaCaveat> Caveats { get; init; } = [];
    public string Revision { get; init; } = "";
}

/// <summary>Identifies a relation or permission on a definition.</summary>
public sealed record RelationReference
{
    public string DefinitionName { get; init; } = "";
    public string RelationName { get; init; } = "";
    public bool IsPermission { get; init; }
}

/// <summary>Represents a single difference between two schemas.</summary>
public sealed record SchemaDiff
{
    /// <summary>Human-readable description of the diff type.</summary>
    public string Kind { get; init; } = "";
    public string DefinitionName { get; init; } = "";
    public string RelationName { get; init; } = "";
    public string PermissionName { get; init; } = "";
    public string CaveatName { get; init; } = "";

    internal static SchemaDiff FromProto(ReflectionSchemaDiff d)
    {
        // Map each oneof case to a SchemaDiff with the appropriate Kind
        if (d.DefinitionAdded != null)
            return new SchemaDiff { Kind = "definition_added", DefinitionName = d.DefinitionAdded.Name };
        if (d.DefinitionRemoved != null)
            return new SchemaDiff { Kind = "definition_removed", DefinitionName = d.DefinitionRemoved.Name };
        if (d.DefinitionDocCommentChanged != null)
            return new SchemaDiff { Kind = "definition_doc_comment_changed", DefinitionName = d.DefinitionDocCommentChanged.Name };
        if (d.RelationAdded != null)
            return new SchemaDiff { Kind = "relation_added", DefinitionName = d.RelationAdded.ParentDefinitionName, RelationName = d.RelationAdded.Name };
        if (d.RelationRemoved != null)
            return new SchemaDiff { Kind = "relation_removed", DefinitionName = d.RelationRemoved.ParentDefinitionName, RelationName = d.RelationRemoved.Name };
        if (d.RelationDocCommentChanged != null)
            return new SchemaDiff { Kind = "relation_doc_comment_changed", DefinitionName = d.RelationDocCommentChanged.ParentDefinitionName, RelationName = d.RelationDocCommentChanged.Name };
        if (d.RelationSubjectTypeAdded != null)
            return new SchemaDiff { Kind = "relation_subject_type_added", DefinitionName = d.RelationSubjectTypeAdded.Relation.ParentDefinitionName, RelationName = d.RelationSubjectTypeAdded.Relation.Name };
        if (d.RelationSubjectTypeRemoved != null)
            return new SchemaDiff { Kind = "relation_subject_type_removed", DefinitionName = d.RelationSubjectTypeRemoved.Relation.ParentDefinitionName, RelationName = d.RelationSubjectTypeRemoved.Relation.Name };
        if (d.PermissionAdded != null)
            return new SchemaDiff { Kind = "permission_added", DefinitionName = d.PermissionAdded.ParentDefinitionName, PermissionName = d.PermissionAdded.Name };
        if (d.PermissionRemoved != null)
            return new SchemaDiff { Kind = "permission_removed", DefinitionName = d.PermissionRemoved.ParentDefinitionName, PermissionName = d.PermissionRemoved.Name };
        if (d.PermissionDocCommentChanged != null)
            return new SchemaDiff { Kind = "permission_doc_comment_changed", DefinitionName = d.PermissionDocCommentChanged.ParentDefinitionName, PermissionName = d.PermissionDocCommentChanged.Name };
        if (d.PermissionExprChanged != null)
            return new SchemaDiff { Kind = "permission_expr_changed", DefinitionName = d.PermissionExprChanged.ParentDefinitionName, PermissionName = d.PermissionExprChanged.Name };
        if (d.CaveatAdded != null)
            return new SchemaDiff { Kind = "caveat_added", CaveatName = d.CaveatAdded.Name };
        if (d.CaveatRemoved != null)
            return new SchemaDiff { Kind = "caveat_removed", CaveatName = d.CaveatRemoved.Name };
        if (d.CaveatDocCommentChanged != null)
            return new SchemaDiff { Kind = "caveat_doc_comment_changed", CaveatName = d.CaveatDocCommentChanged.Name };
        if (d.CaveatExprChanged != null)
            return new SchemaDiff { Kind = "caveat_expr_changed", CaveatName = d.CaveatExprChanged.Name };
        if (d.CaveatParameterAdded != null)
            return new SchemaDiff { Kind = "caveat_parameter_added", CaveatName = d.CaveatParameterAdded.ParentCaveatName };
        if (d.CaveatParameterRemoved != null)
            return new SchemaDiff { Kind = "caveat_parameter_removed", CaveatName = d.CaveatParameterRemoved.ParentCaveatName };
        if (d.CaveatParameterTypeChanged != null)
            return new SchemaDiff { Kind = "caveat_parameter_type_changed", CaveatName = d.CaveatParameterTypeChanged.Parameter.ParentCaveatName };

        return new SchemaDiff { Kind = "unknown" };
    }
}

/// <summary>Holds the result of a permission tree expansion.</summary>
public sealed record ExpandResult
{
    /// <summary>The root of the expanded permission tree.</summary>
    public PermissionTree Tree { get; init; } = new();
    public string Revision { get; init; } = "";
}

/// <summary>Holds the result of a relationship count operation.</summary>
public sealed record CountResult
{
    public ulong RelationshipCount { get; init; }
    public string Revision { get; init; } = "";
}
