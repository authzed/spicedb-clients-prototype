using System.Globalization;
using System.Runtime.CompilerServices;
using Google.Protobuf.WellKnownTypes;
using Google.Rpc;
using Grpc.Core;

namespace SpiceDB.Client.Testing;

/// <summary>
/// An in-memory test double for <see cref="SpiceDBClient"/>, implementing
/// <see cref="ISpiceDBRelationshipClient"/> and <see cref="ISpiceDBSchemaClient"/> with the same
/// signatures. It stores relationships and applies SpiceDB's write semantics: preconditions
/// (<c>MustMatch</c>/<c>MustNotMatch</c>), CREATE conflicts (<see cref="AlreadyExistsException"/>),
/// TOUCH upserts and idempotent DELETE, all atomically per write; an empty write checks its
/// preconditions and returns the current revision, as SpiceDB does. Imports are one
/// transaction (a duplicate fails the whole import with <see cref="AlreadyExistsException"/>).
/// Filtered reads, exports and bounded or complete deletes use <see cref="Filter.Matches"/>, and
/// reads and exports honor <c>ResumeCursor</c> and <c>Limit</c>. Schema writes store the text
/// without compiling it; <see cref="ReflectSchemaAsync"/> returns the result registered with
/// <see cref="SetReflectSchemaResult"/> (an empty result without one). Checks return results registered with
/// <see cref="SetCheckResult"/> or <see cref="SetCheckError"/>, and
/// <see cref="Permissionship.NoPermission"/> otherwise; it does not evaluate a schema.
/// <para>
/// Errors have the same types, codes and message shape as the real client's for the same
/// failure: server failures carry the server's message in both <see cref="Exception.Message"/>
/// and <see cref="SpiceDBException.ServerMessage"/> (and SpiceDB's <c>ErrorInfo</c> reason where
/// SpiceDB attaches one), and local validation is an <see cref="InvalidArgumentException"/> with
/// no server message. Per-call options are plain values the double reads directly:
/// <see cref="TransportOptions.ResponseMetadata"/> sinks receive the metadata set with
/// <see cref="SetResponseMetadata"/>, and a mutation given a <c>RetryPolicy</c> with retries is
/// rejected as the real client rejects it. Thread-safe.
/// </para>
/// <para>
/// Every call is recorded (<see cref="Calls"/>, <see cref="CallsOf"/>) with its arguments and
/// options, so tests can assert on what was sent without a strict mock. Faults can be injected
/// at defined points (<see cref="SetFaultHook"/>, <see cref="FailNext"/>): before any
/// operation, before each import batch, before an import commits, and before each read or
/// export item, so failure paths need no fake gRPC server. Build injected errors with
/// <see cref="ServerError"/> to match the real client's.
/// </para>
/// </summary>
public sealed class InMemorySpiceDBClient : ISpiceDBRelationshipClient, ISpiceDBSchemaClient
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
            var (result, error) = Lookup(request);
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
                var (result, error) = Lookup(items[i]);
                if (error is not null)
                {
                    var itemError = SpiceDBException.Create(error.StatusCode, error.ServerMessage ?? error.Message);
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
            if (batch.Count % batchSize == 0) Inject(call, FaultStage.ImportBatch, batch.Count / batchSize);
            batch.Add(relationship);
        }
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
            return Task.FromResult(new SchemaResult(_schema, Token()));
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
    private void Inject(RecordedCall call, FaultStage stage, int item = 0)
    {
        Func<Fault, Exception?>? hook;
        lock (_lock) hook = _hook;
        if (hook?.Invoke(new Fault(call, stage, item)) is { } error) throw error;
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

    private (CheckResult? Result, SpiceDBException? Error) Lookup(PermissionCheck request) =>
        _checks.TryGetValue(Key(request.Resource, request.Permission, request.Subject), out var registered)
            ? registered
            : (new CheckResult(Permissionship.NoPermission, [], Token()), null);

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
public enum FaultStage
{
    /// <summary>Before the operation does anything, including before a stream yields its first item.</summary>
    Call,
    /// <summary>Before import batch <see cref="Fault.Item"/> (0-based) is sent. Batches have the call's <see cref="ImportOptions.BatchSize"/> (default 1,000).</summary>
    ImportBatch,
    /// <summary>After every batch, before the import commits.</summary>
    ImportCommit,
    /// <summary>Before a read or export yields item <see cref="Fault.Item"/> (0-based within the call).</summary>
    Item,
}

/// <summary>The point at which a fault hook is consulted.</summary>
/// <param name="Call">The call in progress.</param>
/// <param name="Stage">Where in the call.</param>
/// <param name="Item">The batch index for <see cref="FaultStage.ImportBatch"/> and the item index for <see cref="FaultStage.Item"/>; 0 otherwise.</param>
public sealed record Fault(RecordedCall Call, FaultStage Stage, int Item);
