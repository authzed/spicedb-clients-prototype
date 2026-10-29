using System.Globalization;
using Authzed.Api.SpiceDB.Proto;
using System.Runtime.CompilerServices;
using Google.Protobuf.WellKnownTypes;
using Google.Rpc;
using Grpc.Core;
using System.Text.Json.Serialization;

namespace SpiceDB.Client.Testing;

/// <summary>
/// An in-memory test double for <see cref="SpiceDBClient"/>, implementing the client's full
/// surface, <see cref="ISpiceDBClient"/> (and so <see cref="ISpiceDBRelationshipClient"/> and
/// <see cref="ISpiceDBSchemaClient"/>), with the same signatures. It stores relationships and applies SpiceDB's write semantics: preconditions
/// (<c>MustMatch</c>/<c>MustNotMatch</c>), CREATE conflicts (<see cref="AlreadyExistsException"/>),
/// TOUCH upserts and idempotent DELETE, all atomically per write; an empty write checks its
/// preconditions and returns the current revision, as SpiceDB does. Imports are one
/// transaction (a duplicate fails the whole import with <see cref="AlreadyExistsException"/>).
/// Filtered reads, exports and bounded or complete deletes use <see cref="Filter.Matches"/>, and
/// reads and exports honor <c>ResumeCursor</c> and <c>Limit</c>. Schema writes store the text
/// without compiling it; <see cref="ReflectSchemaAsync"/> returns the result registered with
/// <see cref="SetReflectSchemaResult"/> (an empty result without one).
/// <para>
/// A check resolves in a fixed order: a result or error registered for exactly that question
/// (<see cref="SetCheckResult"/>, <see cref="SetCheckError"/>); then the check resolver
/// (<see cref="SetCheckResolver"/>, or <see cref="GrantAllChecks"/>), unless it declines; then
/// the stored relationships, where an unexpired relationship on the resource whose relation is
/// the permission's name and whose subject matches exactly, or by a direct wildcard
/// (<c>user:*</c>), grants it (conditionally when the relationship has a caveat, which is not
/// evaluated; an uncaveated match wins); anything else
/// is <see cref="Permissionship.NoPermission"/>. The double does not evaluate a schema.
/// <see cref="LookupResourcesAsync"/> and <see cref="LookupSubjectsAsync"/> enumerate the objects
/// of the requested type that appear in the stored relationships (as resource or subject, in
/// order of first appearance) and return those the same resolution grants, reporting
/// conditional results as conditional with their missing context and honoring the permissionship filter, the limit and,
/// for resources, the double's cursors (<see cref="CursorAfter"/>).
/// </para>
/// <see cref="CheckAnyAsync"/> and <see cref="CheckAllAsync"/> reduce a bulk check, as the client
/// does, and <see cref="DisposeAsync"/> does nothing. Operations without in-memory semantics
/// (watch, schema introspection and diff, expansion and the experimental operations)
/// are recorded and consult the fault hook, then fail with <see cref="StatusCode.Unimplemented"/>
/// in the real client's error shape; <see cref="RawProto"/> throws
/// <see cref="InvalidOperationException"/>, as a client without a raw transport does.
/// <para>
/// Errors have the same types, codes and message shape as the real client's for the same
/// failure: server failures carry the server's message in both <see cref="Exception.Message"/>
/// and <see cref="SpiceDBException.ServerMessage"/> (and SpiceDB's <c>ErrorInfo</c> reason where
/// SpiceDB attaches one), and local validation is an <see cref="InvalidArgumentException"/> whose
/// server message is its message. Per-call options are plain values the double reads directly:
/// <see cref="TransportOptions.ResponseMetadata"/> sinks receive the metadata set with
/// <see cref="SetResponseMetadata"/>, and a mutation given a <c>RetryPolicy</c> with retries is
/// rejected as the real client rejects it. Thread-safe.
/// </para>
/// <para>
/// Every call is recorded (<see cref="Calls"/>, <see cref="CallsOf"/>) with its arguments and
/// options, so tests can assert on what was sent without a strict mock. Faults can be injected
/// at defined points (<see cref="SetFaultHook"/>, <see cref="FailNext"/>): before any
/// operation, before each import batch, before an import commits, and before each read,
/// export or lookup item (an import-batch fault carries the batch's relationships), so failure paths
/// need no fake gRPC server. <see cref="CursorAfter"/> builds the resume cursors reads and
/// exports return, and <see cref="SetReadSchemaRevision"/> emulates servers whose schema
/// reads report no revision. Build injected errors with
/// <see cref="ServerError"/> to match the real client's.
/// </para>
/// </summary>
public sealed class InMemorySpiceDBClient : ISpiceDBClient
{
    private const string NoSchema = "no schema has been defined; please call WriteSchema to start";
    private readonly object _lock = new();
    private readonly List<Relationship> _relationships = [];
    private readonly Dictionary<string, (CheckResult? Result, SpiceDBException? Error)> _checks = new(StringComparer.Ordinal);
    private long _revision;
    private string? _schema;
    private ReflectSchemaResult? _reflect;
    private Metadata? _headers;
    private Metadata? _trailers;
    private readonly List<RecordedCall> _calls = [];
    private readonly Dictionary<SpiceDBOperation, int> _counts = [];
    private Func<Fault, Exception?>? _hook;
    private Func<PermissionCheck, CheckResult?>? _resolver;
    private bool _omitSchemaRevision;

    /// <summary>Every recorded call, in order.</summary>
    public IReadOnlyList<RecordedCall> Calls { get { lock (_lock) return _calls.ToArray(); } }

    /// <summary>The recorded calls of one operation, in order.</summary>
    public IReadOnlyList<RecordedCall> CallsOf(SpiceDBOperation operation)
    {
        lock (_lock) return _calls.Where(c => c.Operation == operation).ToArray();
    }

    /// <summary>
    /// Installs <paramref name="hook"/>, which runs at every <see cref="Fault"/> point; a non-null
    /// exception is thrown by the operation at that point, and a failed write or import applies
    /// nothing. Build errors with <see cref="ServerError"/> to match the real client's. The hook
    /// must not call this double. Pass null to remove it.
    /// </summary>
    public void SetFaultHook(Func<Fault, Exception?>? hook)
    {
        lock (_lock) _hook = hook;
    }

    /// <summary>Makes the next call of <paramref name="operation"/> fail with <paramref name="error"/> before doing anything; later calls succeed. Replaces any fault hook.</summary>
    public void FailNext(SpiceDBOperation operation, Exception error)
    {
        Native.Required(error, nameof(error));
        var failed = 0;
        SetFaultHook(f => f.Stage == FaultStage.Call && f.Call.Operation == operation && Interlocked.Exchange(ref failed, 1) == 0 ? error : null);
    }

    /// <summary>
    /// The exception the real client throws when SpiceDB answers with <paramref name="code"/>
    /// and <paramref name="message"/> (and, where SpiceDB attaches one, an <c>ErrorInfo</c>
    /// <paramref name="reason"/>), for fault hooks.
    /// </summary>
    public static SpiceDBException ServerError(StatusCode code, string message, string? reason = null)
    {
        if (reason is null) return SpiceDBException.Create(code, message);
        var status = new Google.Rpc.Status { Code = (int)code, Message = message };
        status.Details.Add(Any.Pack(new ErrorInfo { Reason = reason, Domain = "authzed.com" }));
        return SpiceDBException.FromRpcException(status.ToRpcException());
    }

    /// <summary>
    /// The resume cursor that continues a read or export after its first <paramref name="n"/>
    /// matching relationships (in insertion order): the cursor this double puts on item
    /// <paramref name="n"/> (counting from 1). The format, the invariant decimal
    /// <paramref name="n"/>, is stable for tests.
    /// </summary>
    public static string CursorAfter(int n) =>
        n >= 0 ? n.ToString(CultureInfo.InvariantCulture) : throw new InvalidArgumentException("n must not be negative.");

    /// <summary>
    /// Selects whether <see cref="ReadSchemaAsync"/> reports a revision. With
    /// <see cref="SchemaRevision.Omitted"/> its <see cref="SchemaResult.Revision"/> is empty, as
    /// some hosted SpiceDB deployments return, so code that falls back to another revision
    /// source can be tested. The default is <see cref="SchemaRevision.Included"/>.
    /// </summary>
    public void SetReadSchemaRevision(SchemaRevision mode)
    {
        lock (_lock) _omitSchemaRevision = mode == SchemaRevision.Omitted;
    }

    /// <summary>A snapshot of the stored relationships, in insertion order.</summary>
    public IReadOnlyList<Relationship> Relationships { get { lock (_lock) return _relationships.ToArray(); } }

    /// <summary>The revision of the last write or delete, or of the empty store.</summary>
    public ZedToken Revision { get { lock (_lock) return Token(); } }

    /// <summary>Registers the permissionship a check of exactly this resource, permission and subject returns.</summary>
    public void SetCheckResult(ObjectRef resource, string permission, SubjectRef subject, Permissionship permissionship)
    {
        lock (_lock) _checks[Key(resource, permission, subject)] = (new CheckResult(permissionship, [], Token()), null);
    }

    /// <summary>Registers an error that a check of exactly this resource, permission and subject fails with.</summary>
    public void SetCheckError(ObjectRef resource, string permission, SubjectRef subject, SpiceDBException error)
    {
        Native.Required(error, nameof(error));
        lock (_lock) _checks[Key(resource, permission, subject)] = (null, error);
    }

    /// <summary>
    /// Installs a check resolver, consulted for every check (and every check a lookup makes) that
    /// no <see cref="SetCheckResult"/> or <see cref="SetCheckError"/> registration answers, before
    /// the stored relationships. It receives the check with the call's
    /// <see cref="CheckOptions.Context"/> (or <see cref="LookupOptions.Context"/>) merged into
    /// the check's context, the check's own keys winning, so tests over IDs generated at run time
    /// need no per-ID registration. A returned result answers the check (its
    /// <see cref="CheckResult.CheckedAt"/> is replaced by the double's revision); return
    /// <see cref="Permissionship.ConditionalPermission"/> with missing context to emulate a
    /// caveat the caller did not satisfy. Null declines and falls through to the stored
    /// relationships. The resolver answers and never fails: inject failures with
    /// <see cref="SetFaultHook"/>, <see cref="FailNext"/> or <see cref="SetCheckError"/>. It runs
    /// with the double locked and must not call it. Pass null to remove it.
    /// </summary>
    public void SetCheckResolver(Func<PermissionCheck, CheckResult?>? resolver)
    {
        lock (_lock) _resolver = resolver;
    }

    /// <summary>
    /// A check resolver that grants every check:
    /// <c>store.SetCheckResolver(InMemorySpiceDBClient.GrantAll)</c>, or <see cref="GrantAllChecks"/>.
    /// </summary>
    public static CheckResult? GrantAll(PermissionCheck check) => new(Permissionship.HasPermission, [], default);

    /// <summary>Installs <see cref="GrantAll"/> as the check resolver, replacing any other.</summary>
    public void GrantAllChecks() => SetCheckResolver(GrantAll);

    /// <summary>Registers the result <see cref="ReflectSchemaAsync"/> returns (stamped with the current revision).</summary>
    public void SetReflectSchemaResult(ReflectSchemaResult result)
    {
        Native.Required(result, nameof(result));
        lock (_lock) _reflect = result;
    }

    /// <summary>
    /// Sets the response headers and trailers every operation reports to a
    /// <see cref="TransportOptions.ResponseMetadata"/> sink. Snapshotted.
    /// </summary>
    public void SetResponseMetadata(Metadata? headers, Metadata? trailers)
    {
        lock (_lock) { _headers = Copy(headers); _trailers = Copy(trailers); }
    }

    public Task<CheckResult> CheckAsync(PermissionCheck request, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        Begin(SpiceDBOperation.Check, options, options?.RetryPolicy, cancellationToken, request);
        if (request is null) throw new InvalidArgumentException("Request is required.");
        lock (_lock)
        {
            Respond(options?.Transport);
            var (result, error) = Lookup(WithCallContext(request, options?.Context));
            if (error is not null) throw error;
            return Task.FromResult(result!);
        }
    }

    public Task<IReadOnlyList<CheckResult>> CheckBulkAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        Begin(SpiceDBOperation.CheckBulk, options, options?.RetryPolicy, cancellationToken, requests);
        if (requests is null) throw new InvalidArgumentException("Requests are required.");
        var items = requests.ToArray();
        if (items.Any(x => x is null)) throw new InvalidArgumentException("Requests must not contain null.");
        if (items.Any(x => x.Consistency != items[0].Consistency)) throw new InvalidArgumentException("Bulk requests must have equal consistency strategies and tokens.");
        var results = new List<CheckResult>(items.Length);
        lock (_lock)
        {
            Respond(options?.Transport);
            for (var i = 0; i < items.Length; i++)
            {
                var (result, error) = Lookup(WithCallContext(items[i], options?.Context));
                if (error is not null)
                {
                    var itemError = SpiceDBException.Create(error.StatusCode, error.ServerMessage);
                    itemError.InputIndex = i;
                    if (options?.ItemErrors != BulkItemErrors.Returned) throw itemError;
                    results.Add(new CheckResult(itemError, Token()));
                }
                else results.Add(result!);
            }
        }
        return Task.FromResult<IReadOnlyList<CheckResult>>(results.AsReadOnly());
    }

    public Task<WriteResult> WriteAsync(Transaction transaction, WriteOptions? options = null, CancellationToken cancellationToken = default)
    {
        Begin(SpiceDBOperation.Write, options, options?.RetryPolicy, cancellationToken, transaction);
        if (transaction is null) throw new InvalidArgumentException("Transaction is required.");
        SpiceDBClient.MutationRetry(options?.RetryPolicy, transaction.IsIdempotent, "Write");
        lock (_lock)
        {
            Respond(options?.Transport);
            CheckPreconditions(transaction.Preconditions);
            if (transaction.Count == 0) return Task.FromResult(new WriteResult(Token()));
            var next = new List<Relationship>(_relationships);
            foreach (var update in transaction.Updates)
            {
                var index = next.FindIndex(r => SameTuple(r, update.Relationship));
                switch (update.Operation)
                {
                    case UpdateOperation.Create when index >= 0:
                        throw AlreadyExisted(update.Relationship);
                    case UpdateOperation.Create:
                        next.Add(update.Relationship); break;
                    case UpdateOperation.Touch when index >= 0:
                        next[index] = update.Relationship; break;
                    case UpdateOperation.Touch:
                        next.Add(update.Relationship); break;
                    case UpdateOperation.Delete when index >= 0:
                        next.RemoveAt(index); break;
                    case UpdateOperation.Delete:
                        break;
                    default:
                        throw new InvalidArgumentException("Relationship update operation is unspecified.");
                }
            }
            _relationships.Clear(); _relationships.AddRange(next);
            _revision++;
            return Task.FromResult(new WriteResult(Token()));
        }
    }

    public Task<DeleteResult> DeleteRelationshipsAsync(Filter filter, int? limit, DeleteOptions? options = null, CancellationToken cancellationToken = default)
    {
        Begin(SpiceDBOperation.DeleteRelationships, options, options?.RetryPolicy, cancellationToken, filter, limit);
        SpiceDBClient.MutationRetry(options?.RetryPolicy, false, "DeleteRelationships");
        return Task.FromResult(Delete(filter, limit, options));
    }

    /// <summary>One bounded delete, shared by both delete operations without recording a call.</summary>
    private DeleteResult Delete(Filter filter, int? limit, DeleteOptions? options)
    {
        var partial = options?.Partial != PartialDeletion.Disallowed;
        if (limit is { } l && l <= 0) throw new InvalidArgumentException("limit must be positive.");
        if (limit is null && partial) throw new InvalidArgumentException("limit is required unless Partial is PartialDeletion.Disallowed.");
        Native.Required(filter, nameof(filter)).ToProto();
        lock (_lock)
        {
            Respond(options?.Transport);
            CheckPreconditions((options?.MustMatch ?? []).Select(f => new Precondition(PreconditionOperation.MustMatch, f))
                .Concat((options?.MustNotMatch ?? []).Select(f => new Precondition(PreconditionOperation.MustNotMatch, f))).ToArray());
            var matches = _relationships.Where(filter.Matches).ToList();
            if (!partial && limit is { } max && matches.Count > max)
                throw ServerError(StatusCode.InvalidArgument, $"found more than {max} relationships to be deleted and partial deletion was not requested",
                    "ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE");
            var deleted = limit is { } bound && partial ? matches.Take(bound).ToList() : matches;
            foreach (var r in deleted) _relationships.Remove(r);
            _revision++;
            var completed = deleted.Count == matches.Count;
            return new DeleteResult(Token(), (ulong)deleted.Count, completed, completed ? null : "mem-delete");
        }
    }

    public Task<DeleteResult> DeleteAllRelationshipsNonAtomicAsync(Filter filter, DeleteAllOptions? options = null, CancellationToken cancellationToken = default)
    {
        Begin(SpiceDBOperation.DeleteAllRelationshipsNonAtomic, options, options?.RetryPolicy, cancellationToken, filter);
        SpiceDBClient.MutationRetry(options?.RetryPolicy, false, "DeleteAllRelationshipsNonAtomic");
        var size = options?.PageSize ?? 1000;
        if (size <= 0) throw new InvalidArgumentException("PageSize must be positive.");
        Native.Required(filter, nameof(filter));
        var pageOptions = new DeleteOptions { MustMatch = options?.MustMatch ?? [], MustNotMatch = options?.MustNotMatch ?? [], Transport = options?.Transport };
        ulong total = 0;
        while (true)
        {
            cancellationToken.ThrowIfCancellationRequested();
            var page = Delete(filter, size, pageOptions);
            total += page.DeletedCount;
            options?.Progress?.Report(page with { DeletedCount = total });
            if (page.Completed) return Task.FromResult(page with { DeletedCount = total });
        }
    }

    public async IAsyncEnumerable<ReadRelationship> ReadRelationshipsAsync(Consistency consistency, Filter filter, ReadOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        var call = Begin(SpiceDBOperation.ReadRelationships, options, options?.RetryPolicy, cancellationToken, consistency, filter);
        Positive(options?.PageSize ?? 512, "PageSize");
        var limit = Positive(options?.Limit ?? int.MaxValue, "Limit");
        Native.Required(consistency, nameof(consistency));
        Native.Required(filter, nameof(filter)).ToProto();
        Relationship[] snapshot; ZedToken readAt;
        lock (_lock) { snapshot = _relationships.Where(filter.Matches).ToArray(); readAt = Token(); Respond(options?.Transport); }
        var start = Start(options?.ResumeCursor);
        for (var i = start; i < snapshot.Length && i - start < limit; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            await Task.Yield();
            Inject(call, FaultStage.Item, i - start);
            yield return new(snapshot[i], (i + 1).ToString(CultureInfo.InvariantCulture), readAt);
        }
    }

    /// <summary>
    /// Adds every relationship in one transaction, as SpiceDB does: if any already exists (or
    /// repeats within the import), nothing is added and the import fails with
    /// <see cref="AlreadyExistsException"/>.
    /// </summary>
    public async Task<ulong> ImportRelationshipsAsync(IAsyncEnumerable<Relationship> relationships, ImportOptions? options = null, CancellationToken cancellationToken = default)
    {
        var call = Begin(SpiceDBOperation.ImportRelationships, options, options?.RetryPolicy, cancellationToken, relationships);
        var batchSize = Positive(options?.BatchSize ?? 1000, "BatchSize");
        SpiceDBClient.MutationRetry(options?.RetryPolicy, false, "ImportRelationships");
        var batch = new List<Relationship>();
        await foreach (var relationship in Native.Required(relationships, nameof(relationships)).WithCancellation(cancellationToken))
        {
            Native.Required(relationship, nameof(relationship)).ToProto();
            batch.Add(relationship);
            if (batch.Count % batchSize == 0) InjectBatch(call, batch, batchSize);
        }
        if (batch.Count % batchSize != 0) InjectBatch(call, batch, batchSize);
        Inject(call, FaultStage.ImportCommit);
        lock (_lock)
        {
            Respond(options?.Transport);
            var next = new List<Relationship>(_relationships);
            foreach (var relationship in batch)
            {
                if (next.Any(r => SameTuple(r, relationship)))
                    throw AlreadyExisted(relationship);
                next.Add(relationship);
            }
            _relationships.Clear(); _relationships.AddRange(next);
            _revision++;
            return (ulong)batch.Count;
        }
    }

    /// <summary>Yields the relationships matching <paramref name="filter"/> in insertion order, honoring <c>ResumeCursor</c> and <c>Limit</c>.</summary>
    public async IAsyncEnumerable<ExportedRelationship> ExportRelationshipsAsync(Consistency consistency, Filter filter, ExportOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        var call = Begin(SpiceDBOperation.ExportRelationships, options, options?.RetryPolicy, cancellationToken, consistency, filter);
        Positive(options?.PageSize ?? 512, "PageSize");
        var limit = Positive(options?.Limit ?? int.MaxValue, "Limit");
        Native.Required(consistency, nameof(consistency));
        Native.Required(filter, nameof(filter)).ToProto();
        Relationship[] snapshot;
        lock (_lock) { snapshot = _relationships.Where(filter.Matches).ToArray(); Respond(options?.Transport); }
        var start = Start(options?.ResumeCursor);
        for (var i = start; i < snapshot.Length && i - start < limit; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            await Task.Yield();
            Inject(call, FaultStage.Item, i - start);
            yield return new(snapshot[i], (i + 1).ToString(CultureInfo.InvariantCulture));
        }
    }

    /// <summary>Returns the schema last written, or fails with <see cref="NotFoundException"/> as SpiceDB does.</summary>
    public Task<SchemaResult> ReadSchemaAsync(CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        Begin(SpiceDBOperation.ReadSchema, options, options?.RetryPolicy, cancellationToken);
        options?.Validate();
        lock (_lock)
        {
            Respond(options?.Transport);
            if (_schema is null) throw ServerError(StatusCode.NotFound, NoSchema);
            return Task.FromResult(new SchemaResult(_schema, _omitSchemaRevision ? new ZedToken("") : Token()));
        }
    }

    /// <summary>Stores the schema text. It is not compiled or validated.</summary>
    public Task<ZedToken> WriteSchemaAsync(string schema, CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        Begin(SpiceDBOperation.WriteSchema, options, options?.RetryPolicy, cancellationToken, schema);
        options?.Validate();
        if (string.IsNullOrEmpty(schema)) throw new InvalidArgumentException("Schema must not be empty.");
        lock (_lock)
        {
            Respond(options?.Transport);
            _schema = schema;
            _revision++;
            return Task.FromResult(Token());
        }
    }

    /// <summary>Returns the result registered with <see cref="SetReflectSchemaResult"/>, stamped with the current revision; without one, an empty result, as SpiceDB returns for an empty schema.</summary>
    public Task<ReflectSchemaResult> ReflectSchemaAsync(Consistency consistency, CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        Begin(SpiceDBOperation.ReflectSchema, options, options?.RetryPolicy, cancellationToken, consistency);
        options?.Validate(reflect: true);
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");
        lock (_lock)
        {
            Respond(options?.Transport);
            return Task.FromResult((_reflect ?? new ReflectSchemaResult()) with { Revision = Token() });
        }
    }

    /// <summary>Reduces a bulk check (recorded as <see cref="SpiceDBOperation.CheckBulk"/>), as the client does: true when any result has permission.</summary>
    public async Task<bool> CheckAnyAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default) =>
        (await CheckBulkAsync(requests, options, cancellationToken)).Any(r => r.HasPermission);

    /// <summary>Reduces a bulk check (recorded as <see cref="SpiceDBOperation.CheckBulk"/>), as the client does: true when there are results and all have permission.</summary>
    public async Task<bool> CheckAllAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        var results = await CheckBulkAsync(requests, options, cancellationToken);
        return results.Count > 0 && results.All(r => r.HasPermission);
    }

    /// <summary>Throws <see cref="InvalidOperationException"/>, as a client without a raw transport does; the double has no protocol client.</summary>
    public SpiceDBProtoClient RawProto() => throw new InvalidOperationException("The in-memory client has no raw transport.");

    /// <summary>Does nothing; the double holds no transport.</summary>
    public ValueTask DisposeAsync() => ValueTask.CompletedTask;

    /// <summary>
    /// Yields the resources of <paramref name="resourceType"/> that appear in the stored
    /// relationships (as resource or subject, in order of first appearance) and that check
    /// resolution grants <paramref name="permission"/> to <paramref name="subject"/>; conditional
    /// grants are reported as conditional. Honors <see cref="LookupOptions.PermissionshipFilter"/>,
    /// <see cref="LookupOptions.Limit"/> (counting yielded results) and
    /// <see cref="LookupOptions.ResumeCursor"/>: item n (counting granted results from 1, whether or
    /// not the filter yields them) carries <see cref="CursorAfter"/>(n). A registered check error
    /// for a candidate fails the lookup.
    /// </summary>
    public async IAsyncEnumerable<LookupResource> LookupResourcesAsync(Consistency consistency, string resourceType, string permission, SubjectRef subject, LookupOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        var call = Begin(SpiceDBOperation.LookupResources, options, options?.RetryPolicy, cancellationToken, consistency, resourceType, permission, subject);
        if (options?.SubjectRelation is not null) throw new InvalidArgumentException("LookupResources uses the supplied SubjectRef relation.");
        Positive(options?.PageSize ?? 512, "PageSize");
        var limit = Positive(options?.Limit ?? int.MaxValue, "Limit");
        Native.Required(consistency, nameof(consistency));
        Native.Required(resourceType, nameof(resourceType));
        Native.Required(permission, nameof(permission));
        Native.Required(subject, nameof(subject));
        (string Id, CheckResult Result)[] granted; ZedToken at;
        lock (_lock)
        {
            Respond(options?.Transport);
            at = Token();
            granted = Granted(ObjectsOf(resourceType).Where(id => id != "*"),
                id => new PermissionCheck(new ObjectRef(resourceType, id), permission, subject, consistency, options?.Context));
        }
        var onlyGrants = options?.PermissionshipFilter == LookupPermissionshipFilter.HasPermissionOnly;
        var yielded = 0;
        for (var i = Start(options?.ResumeCursor); i < granted.Length && yielded < limit; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (onlyGrants && granted[i].Result.Permissionship != Permissionship.HasPermission) continue;
            await Task.Yield();
            Inject(call, FaultStage.Item, yielded);
            yielded++;
            yield return new LookupResource
            {
                ResourceId = granted[i].Id,
                Permissionship = granted[i].Result.Permissionship,
                PartialCaveat = PartialCaveat(granted[i].Result),
                LookedUpAt = at,
                AfterResultCursor = CursorAfter(i + 1),
            };
        }
    }

    /// <summary>
    /// Yields the subjects of <paramref name="subjectType"/> (with <see cref="LookupOptions.SubjectRelation"/>,
    /// direct when null or <c>...</c>) that appear in the stored relationships (as resource or
    /// subject, in order of first appearance, including a wildcard <c>*</c>) and that check
    /// resolution grants <paramref name="permission"/> on <paramref name="resource"/>; conditional
    /// grants are reported as conditional. Honors <see cref="LookupOptions.Limit"/> and rejects the
    /// options the real client rejects for this operation. A registered check error for a
    /// candidate fails the lookup.
    /// </summary>
    public async IAsyncEnumerable<LookupSubject> LookupSubjectsAsync(Consistency consistency, ObjectRef resource, string permission, string subjectType, LookupOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        var call = Begin(SpiceDBOperation.LookupSubjects, options, options?.RetryPolicy, cancellationToken, consistency, resource, permission, subjectType);
        if (options is not null && options.PermissionshipFilter != LookupPermissionshipFilter.All) throw new InvalidArgumentException("LookupSubjects does not support PermissionshipFilter.");
        if (options?.Debug == LookupDebug.Enabled || options?.PageSize is not null || options?.Paging is Paging.SingleRequest) throw new InvalidArgumentException("LookupSubjects does not support Debug, PageSize or Paging; upstream concrete pagination is unimplemented.");
        var limit = Positive(options?.Limit ?? int.MaxValue, "Limit");
        Native.Required(consistency, nameof(consistency));
        Native.Required(resource, nameof(resource));
        Native.Required(permission, nameof(permission));
        Native.Required(subjectType, nameof(subjectType));
        var relation = options?.SubjectRelation is null or "" ? null : options.SubjectRelation;
        (string Id, CheckResult Result)[] granted; ZedToken at;
        lock (_lock)
        {
            Respond(options?.Transport);
            at = Token();
            granted = Granted(ObjectsOf(subjectType),
                id => new PermissionCheck(resource, permission, new SubjectRef(new ObjectRef(subjectType, id), relation), consistency, options?.Context));
        }
        for (var i = 0; i < granted.Length && i < limit; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            await Task.Yield();
            Inject(call, FaultStage.Item, i);
            yield return new LookupSubject
            {
                Subject = new ResolvedSubject
                {
                    SubjectId = granted[i].Id,
                    Permissionship = granted[i].Result.Permissionship,
                    PartialCaveat = PartialCaveat(granted[i].Result),
                },
                LookedUpAt = at,
            };
        }
    }

    // The remaining operations have no in-memory semantics. Each is recorded and consults the
    // fault hook, then fails with Unimplemented in the real client's error shape.

    public IAsyncEnumerable<WatchEvent> UpdatesAsync(IReadOnlyList<string> objectTypes, ZedToken? startRevision, WatchOptions? options = null, CancellationToken cancellationToken = default) =>
        UnimplementedStream<WatchEvent>(SpiceDBOperation.Updates, options, options?.RetryPolicy, cancellationToken, objectTypes, startRevision);

    public Task<ComputablePermissionsResult> ComputablePermissionsAsync(Consistency consistency, string definitionName, string relationName, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<ComputablePermissionsResult>(SpiceDBOperation.ComputablePermissions, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(computable: true), consistency, definitionName, relationName);

    public Task<DependentRelationsResult> DependentRelationsAsync(Consistency consistency, string definitionName, string permissionName, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<DependentRelationsResult>(SpiceDBOperation.DependentRelations, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(), consistency, definitionName, permissionName);

    public Task<DiffSchemaResult> DiffSchemaAsync(Consistency consistency, string comparisonSchema, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<DiffSchemaResult>(SpiceDBOperation.DiffSchema, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(), consistency, comparisonSchema);

    public Task<ExpandResult> ExpandPermissionTreeAsync(Consistency consistency, ObjectRef resource, string permission, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<ExpandResult>(SpiceDBOperation.ExpandPermissionTree, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(), consistency, resource, permission);

    public Task ExperimentalRegisterRelationshipCounterAsync(string name, Filter filter, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<object?>(SpiceDBOperation.ExperimentalRegisterRelationshipCounter, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(), name, filter);

    public Task<CountResult> ExperimentalCountRelationshipsAsync(string name, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<CountResult>(SpiceDBOperation.ExperimentalCountRelationships, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(), name);

    public Task ExperimentalUnregisterRelationshipCounterAsync(string name, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<object?>(SpiceDBOperation.ExperimentalUnregisterRelationshipCounter, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(), name);

    public Task<RoaringLookupResourcesResult> ExperimentalRoaringLookupResourcesAsync(Consistency consistency, string resourceType, string permission, SubjectRef subject, LookupOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<RoaringLookupResourcesResult>(SpiceDBOperation.ExperimentalRoaringLookupResources, options, options?.RetryPolicy, cancellationToken, null, consistency, resourceType, permission, subject);

    public Task<CountResult> ExperimentalCountRelationshipsByFilterAsync(Filter filter, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<CountResult>(SpiceDBOperation.ExperimentalCountRelationshipsByFilter, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(), filter);

    public IAsyncEnumerable<PermissionSetEvent> ExperimentalWatchPermissionSetsAsync(ZedToken? startRevision, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        UnimplementedStream<PermissionSetEvent>(SpiceDBOperation.ExperimentalWatchPermissionSets, options, options?.RetryPolicy, cancellationToken, startRevision);

    public IAsyncEnumerable<LookupPermissionSet> ExperimentalLookupPermissionSetsAsync(int limit, ZedToken? atRevision, PermissionSetLookupOptions? options = null, CancellationToken cancellationToken = default) =>
        UnimplementedStream<LookupPermissionSet>(SpiceDBOperation.ExperimentalLookupPermissionSets, options, options?.RetryPolicy, cancellationToken, limit, atRevision);

    public Task<DownloadPermissionSetsResult> ExperimentalDownloadPermissionSetsAsync(ZedToken? atRevision, CallOptions? options = null, CancellationToken cancellationToken = default) =>
        Unimplemented<DownloadPermissionSetsResult>(SpiceDBOperation.ExperimentalDownloadPermissionSets, options, options?.RetryPolicy, cancellationToken, () => options?.Validate(), atRevision);

    /// <summary>
    /// Records the call, runs the fault hook and validates the options, then fails as SpiceDB
    /// does for an operation it does not implement. Every failure surfaces from the returned
    /// task, as from the client's async methods.
    /// </summary>
    private async Task<T> Unimplemented<T>(SpiceDBOperation operation, object? options, RetryPolicy? retry, CancellationToken cancellationToken, Action? validate, params object?[] arguments)
    {
        Begin(operation, options, retry, cancellationToken, arguments);
        validate?.Invoke();
        await Task.Yield();
        throw NotImplementedHere(operation);
    }

    /// <summary>The stream form of <see cref="Unimplemented{T}"/>: fails on the first move, before any item.</summary>
    private async IAsyncEnumerable<T> UnimplementedStream<T>(SpiceDBOperation operation, object? options, RetryPolicy? retry, [EnumeratorCancellation] CancellationToken cancellationToken, params object?[] arguments)
    {
        Begin(operation, options, retry, cancellationToken, arguments);
        await Task.Yield();
        throw NotImplementedHere(operation);
#pragma warning disable CS0162 // The unreachable yield makes this an async iterator.
        yield break;
#pragma warning restore CS0162
    }

    private static SpiceDBException NotImplementedHere(SpiceDBOperation operation) =>
        ServerError(StatusCode.Unimplemented, $"{operation} is not implemented by InMemorySpiceDBClient");

    /// <summary>Records a call and runs the <see cref="FaultStage.Call"/> hook, then validates the per-call retry policy.</summary>
    private RecordedCall Begin(SpiceDBOperation operation, object? options, RetryPolicy? retry, CancellationToken cancellationToken, params object?[] arguments)
    {
        cancellationToken.ThrowIfCancellationRequested();
        RecordedCall call;
        lock (_lock)
        {
            _counts.TryGetValue(operation, out var index);
            _counts[operation] = index + 1;
            call = new(operation, index, arguments, options);
            _calls.Add(call);
        }
        Inject(call, FaultStage.Call);
        retry?.Validated();
        return call;
    }

    /// <summary>Runs the fault hook, if any, at one point of a call, throwing what it returns.</summary>
    private void Inject(RecordedCall call, FaultStage stage, int item = 0, IReadOnlyList<Relationship>? batch = null)
    {
        Func<Fault, Exception?>? hook;
        lock (_lock) hook = _hook;
        if (hook?.Invoke(new Fault(call, stage, item) { Batch = batch ?? [] }) is { } error) throw error;
    }

    /// <summary>Runs the <see cref="FaultStage.ImportBatch"/> hook for the batch ending the relationships buffered so far, with its contents.</summary>
    private void InjectBatch(RecordedCall call, List<Relationship> buffered, int batchSize)
    {
        var index = (buffered.Count - 1) / batchSize;
        var start = index * batchSize;
        Inject(call, FaultStage.ImportBatch, index, buffered.GetRange(start, buffered.Count - start).AsReadOnly());
    }

    private static int Positive(int value, string field) => value > 0 ? value : throw new InvalidArgumentException($"{field} must be positive.");

    private static int Start(string? cursor) =>
        string.IsNullOrEmpty(cursor) ? 0
        : int.TryParse(cursor, NumberStyles.None, CultureInfo.InvariantCulture, out var start) ? start
        : throw ServerError(StatusCode.InvalidArgument, "invalid cursor");

    /// <summary>Fills the caller's response-metadata sink, as the real client does for the operation's last RPC.</summary>
    private void Respond(TransportOptions? transport)
    {
        if (transport?.ResponseMetadata is not { } sink) return;
        sink.Headers = Copy(_headers) ?? new Metadata();
        sink.Trailers = Copy(_trailers) ?? new Metadata();
    }

    private static Metadata? Copy(Metadata? value)
    {
        if (value is null) return null;
        var copy = new Metadata();
        foreach (var entry in value) copy.Add(entry);
        return copy;
    }

    // SpiceDB's message for both a CREATE conflict and an import duplicate.
    private static SpiceDBException AlreadyExisted(Relationship relationship) => ServerError(StatusCode.AlreadyExists,
        $"could not CREATE relationship `{relationship}`, as it already existed. If this is persistent, please switch to TOUCH operations or specify a precondition",
        "ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP");

    /// <summary>
    /// Resolves a check: the registered result or error, then the resolver, then the stored
    /// relationships, then no permission. Callers hold the lock.
    /// </summary>
    private (CheckResult? Result, SpiceDBException? Error) Lookup(PermissionCheck request)
    {
        if (_checks.TryGetValue(Key(request.Resource, request.Permission, request.Subject), out var registered)) return registered;
        if (_resolver?.Invoke(request) is { } answer)
            return (new CheckResult(answer.Permissionship, answer.MissingContext, Token(), answer.DebugTrace, answer.Expiration), null);
        return (new CheckResult(Stored(request), [], Token()), null);
    }

    /// <summary>The check with the call's context merged into its own; the check's keys win.</summary>
    private static PermissionCheck WithCallContext(PermissionCheck request, IReadOnlyDictionary<string, object?>? callContext)
    {
        if (request is null || callContext is null || callContext.Count == 0) return request!;
        var merged = new Dictionary<string, object?>(callContext);
        foreach (var pair in request.Context ?? new Dictionary<string, object?>()) merged[pair.Key] = pair.Value;
        return request.WithContext(merged);
    }

    /// <summary>
    /// The permissionship the stored relationships give: an unexpired relationship on the
    /// resource, with the permission's name as its relation and a subject matching exactly or by a
    /// direct wildcard, grants it; a caveated one grants it conditionally (caveats are not
    /// evaluated).
    /// </summary>
    private Permissionship Stored(PermissionCheck request)
    {
        var now = DateTimeOffset.UtcNow;
        var result = Permissionship.NoPermission;
        foreach (var r in _relationships)
        {
            if (r.Resource != request.Resource || r.Relation != request.Permission) continue;
            if (r.Expiration is { } expiration && expiration <= now) continue;
            var subject = request.Subject;
            var matches = r.Subject == subject
                || (r.SubjectId == "*" && r.SubjectRelation is null && subject.Relation is null && r.SubjectType == subject.Object.ObjectType);
            if (!matches) continue;
            if (r.CaveatName is null) return Permissionship.HasPermission;
            result = Permissionship.ConditionalPermission;
        }
        return result;
    }

    /// <summary>The distinct IDs of objects of <paramref name="type"/> in the stored relationships, as resource or subject, in order of first appearance.</summary>
    private IEnumerable<string> ObjectsOf(string type)
    {
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var r in _relationships)
        {
            if (r.ResourceType == type && seen.Add(r.ResourceId)) yield return r.ResourceId;
            if (r.SubjectType == type && seen.Add(r.SubjectId)) yield return r.SubjectId;
        }
    }

    /// <summary>The candidates check resolution grants (unconditionally or conditionally); a candidate's check error fails the lookup.</summary>
    private (string Id, CheckResult Result)[] Granted(IEnumerable<string> candidates, Func<string, PermissionCheck> check)
    {
        var granted = new List<(string, CheckResult)>();
        foreach (var id in candidates.ToArray())
        {
            var (result, error) = Lookup(check(id));
            if (error is not null) throw error;
            if (result!.Permissionship is Permissionship.HasPermission or Permissionship.ConditionalPermission)
                granted.Add((id, result));
        }
        return granted.ToArray();
    }

    /// <summary>A conditional result's missing context, as the real client reports it on lookup results.</summary>
    private static PartialCaveatInfo? PartialCaveat(CheckResult result) =>
        result.Permissionship == Permissionship.ConditionalPermission ? new PartialCaveatInfo { MissingRequiredContext = result.MissingContext } : null;

    private void CheckPreconditions(IReadOnlyList<Precondition> preconditions)
    {
        foreach (var p in preconditions)
        {
            var matched = _relationships.Any(p.Filter.Matches);
            if (matched != (p.Operation == PreconditionOperation.MustMatch))
                throw ServerError(StatusCode.FailedPrecondition, $"unable to satisfy write precondition `{p}`", "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE");
        }
    }

    private static bool SameTuple(Relationship a, Relationship b) =>
        a.Resource == b.Resource && a.Relation == b.Relation && a.Subject == b.Subject;

    private static string Key(ObjectRef resource, string permission, SubjectRef subject) =>
        $"{Native.Required(resource, nameof(resource))}#{Native.Required(permission, nameof(permission))}@{Native.Required(subject, nameof(subject))}";

    private ZedToken Token() => new("mem-" + _revision.ToString(CultureInfo.InvariantCulture));
}

/// <summary>One call recorded by <see cref="InMemorySpiceDBClient"/>.</summary>
/// <param name="Operation">The operation called.</param>
/// <param name="Index">Counts calls of this operation, starting at 0.</param>
/// <param name="Arguments">The call's arguments in signature order, without the options and cancellation token.</param>
/// <param name="Options">The call's options object as passed (for example a <see cref="ReadOptions"/>), or null.</param>
public sealed record RecordedCall(SpiceDBOperation Operation, int Index, IReadOnlyList<object?> Arguments, object? Options)
{
    /// <summary>The first argument of type <typeparamref name="T"/>, such as the <see cref="Filter"/> of a read.</summary>
    public T? Argument<T>() => Arguments.OfType<T>().FirstOrDefault();

    /// <summary>The options as <typeparamref name="T"/>, or null when none were passed.</summary>
    public T? OptionsAs<T>() where T : class => Options as T;
}

/// <summary>Where in an operation a fault hook runs.</summary>
[JsonConverter(typeof(JsonStringEnumConverter<FaultStage>))]
public enum FaultStage
{
    /// <summary>Before the operation does anything, including before a stream yields its first item.</summary>
    Call,
    /// <summary>Before import batch <see cref="Fault.Item"/> (0-based) is sent, once it is full or the input ends; <see cref="Fault.Batch"/> holds its relationships. Batches have the call's <see cref="ImportOptions.BatchSize"/> (default 1,000).</summary>
    ImportBatch,
    /// <summary>After every batch, before the import commits.</summary>
    ImportCommit,
    /// <summary>Before a read, export or lookup yields item <see cref="Fault.Item"/> (0-based within the call).</summary>
    Item,
}

/// <summary>The point at which a fault hook is consulted.</summary>
/// <param name="Call">The call in progress.</param>
/// <param name="Stage">Where in the call.</param>
/// <param name="Item">The batch index for <see cref="FaultStage.ImportBatch"/> and the item index for <see cref="FaultStage.Item"/>; 0 otherwise.</param>
public sealed record Fault(RecordedCall Call, FaultStage Stage, int Item)
{
    /// <summary>The relationships in the batch at <see cref="FaultStage.ImportBatch"/>, in input order; empty at other stages.</summary>
    public IReadOnlyList<Relationship> Batch { get; init; } = [];
}

/// <summary>Whether <see cref="InMemorySpiceDBClient.ReadSchemaAsync"/> reports a revision; see <see cref="InMemorySpiceDBClient.SetReadSchemaRevision"/>.</summary>
[JsonConverter(typeof(JsonStringEnumConverter<SchemaRevision>))]
public enum SchemaRevision
{
    /// <summary>Reports the double's current revision. The default.</summary>
    Included,
    /// <summary>Reports an empty revision, as some hosted SpiceDB deployments do.</summary>
    Omitted,
}
