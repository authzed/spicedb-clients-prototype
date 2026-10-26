using System.Diagnostics;
using System.Runtime.CompilerServices;
using Authzed.Api.SpiceDB.Proto;
using Authzed.Api.V1;
using Google.Protobuf.WellKnownTypes;
using Grpc.Core;
using Google.Protobuf;
using Grpc.Net.Client;
using MaterializeV0 = Authzed.Api.Materialize.V0;
[assembly: InternalsVisibleTo("SpiceDB.Client.Tests")]
namespace SpiceDB.Client;

/// <summary>Native asynchronous SpiceDB API. RawProto is the authenticated secondary protocol escape hatch.</summary>
public sealed partial class SpiceDBClient : IAsyncDisposable
{
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(30);
    private readonly SpiceDBProtoClient? _protoClient;
    private readonly PermissionsService.PermissionsServiceClient _permissions;
    private readonly SchemaService.SchemaServiceClient _schema;
    private readonly WatchService.WatchServiceClient _watch;
    private readonly ExperimentalService.ExperimentalServiceClient _experimental;
    private readonly MaterializeV0.RoaringLookupResourcesService.RoaringLookupResourcesServiceClient _materialize;
    private readonly TimeSpan _defaultTimeout;
    private readonly CancellationTokenSource _lifetime = new();
    private int _disposed;
    private SpiceDBClient(SpiceDBProtoClient proto, TimeSpan timeout)
    { _protoClient = proto; _permissions = proto.Permissions; _schema = proto.Schema; _watch = proto.Watch; _experimental = proto.Experimental; _materialize = proto.Materialize; _defaultTimeout = timeout; }
    internal SpiceDBClient(PermissionsService.PermissionsServiceClient permissions, SchemaService.SchemaServiceClient schema,
        WatchService.WatchServiceClient watch, ExperimentalService.ExperimentalServiceClient experimental,
        MaterializeV0.RoaringLookupResourcesService.RoaringLookupResourcesServiceClient? materialize = null, TimeSpan? defaultTimeout = null)
    { _permissions = permissions; _schema = schema; _watch = watch; _experimental = experimental; _materialize = materialize!; _defaultTimeout = defaultTimeout ?? DefaultTimeout; }

    public static SpiceDBClient CreatePlaintext(string endpoint, string presharedKey, ClientOptions? options = null) => Create(endpoint, presharedKey, options, true);
    public static SpiceDBClient CreateSystemTls(string endpoint, string presharedKey, ClientOptions? options = null) => Create(endpoint, presharedKey, options, false);
    private static SpiceDBClient Create(string endpoint, string key, ClientOptions? options, bool insecure)
    {
        Native.Required(endpoint, nameof(endpoint)); Native.Required(key, "presharedKey");
        var timeout = ValidTimeout(options?.DefaultTimeout ?? DefaultTimeout);
        try { return new(new SpiceDBProtoClient(endpoint, key, insecure, options?.RemotePlaintextCredentials == RemotePlaintextCredentials.Allow), timeout); }
        catch (Exception ex) when (ex is ArgumentException or InvalidOperationException or UriFormatException) { throw new InvalidArgumentException(ex.Message.Replace("allowInsecureRemoteCredentials", "RemotePlaintextCredentials.Allow", StringComparison.Ordinal), ex); }
    }
    /// <summary>Borrows the channel. Arbitrary caller transport configuration can supersede endpoint security assumptions.</summary>
    public static SpiceDBClient CreateFromChannel(GrpcChannel channel, string presharedKey, ClientOptions? options = null)
    {
        if (channel is null) throw new InvalidArgumentException("Channel is required.");
        Native.Required(presharedKey, nameof(presharedKey));
        var timeout = ValidTimeout(options?.DefaultTimeout ?? DefaultTimeout);
        // Target omits the scheme. Inspect the transport's parsed address and fail
        // closed if a future Grpc.Net.Client version stops exposing that metadata.
        var address = typeof(GrpcChannel).GetProperty("Address", System.Reflection.BindingFlags.Instance | System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.NonPublic)?.GetValue(channel) as Uri;
        if (!(options?.RemotePlaintextCredentials == RemotePlaintextCredentials.Allow) &&
            (address is null || (address.Scheme != "https" && !address.IsLoopback)))
            throw new InvalidArgumentException("Cannot establish secure or loopback channel transport. Set RemotePlaintextCredentials.Allow only to explicitly permit remote plaintext credentials.");
        return new(new SpiceDBProtoClient(channel, presharedKey), timeout);
    }
    public SpiceDBProtoClient RawProto() => _protoClient ?? throw new InvalidOperationException("The test client has no raw transport.");
    public ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref _disposed, 1) == 0) { _lifetime.Cancel(); _protoClient?.Dispose(); }
        return ValueTask.CompletedTask;
    }
    private static TimeSpan JitteredDelay(TimeSpan cap) => TimeSpan.FromMilliseconds(Random.Shared.NextDouble() * cap.TotalMilliseconds);
    private static TimeSpan ValidTimeout(TimeSpan value) => value <= TimeSpan.Zero || value.TotalMilliseconds > uint.MaxValue - 1 ? throw new InvalidArgumentException("Timeout must be positive and supported by the runtime timer.") : value;
    private Budget Begin(TimeSpan? timeout, CancellationToken token, bool unary = true)
    {
        token.ThrowIfCancellationRequested();
        ObjectDisposedException.ThrowIf(_disposed != 0, this);
        return new(timeout is { } t ? ValidTimeout(t) : unary ? _defaultTimeout : null, token, _lifetime.Token);
    }
    private sealed class Budget : IDisposable
    {
        private readonly long _start = Stopwatch.GetTimestamp();
        private readonly TimeSpan? _timeout;
        private readonly CancellationToken _caller;
        private readonly CancellationTokenSource _linked;
        public CancellationToken Token => _linked.Token;
        public Budget(TimeSpan? timeout, CancellationToken caller, CancellationToken lifetime)
        {
            _timeout = timeout; _caller = caller; _linked = CancellationTokenSource.CreateLinkedTokenSource(caller, lifetime);
            if (timeout is { } t) _linked.CancelAfter(t);
        }
        public DateTime? Deadline
        {
            get { Check(); return _timeout is { } t ? DateTime.UtcNow + (t - Stopwatch.GetElapsedTime(_start)) : null; }
        }
        public void Check()
        {
            _caller.ThrowIfCancellationRequested();
            if (_timeout is { } t && Stopwatch.GetElapsedTime(_start) >= t) throw new DeadlineExceededException("Operation total deadline exceeded.");
            Token.ThrowIfCancellationRequested();
        }
        public Exception Map(Exception ex)
        {
            if (_caller.IsCancellationRequested) return new OperationCanceledException("Operation cancelled by caller.", ex, _caller);
            if (_timeout is { } t && Stopwatch.GetElapsedTime(_start) >= t) return new DeadlineExceededException("Operation total deadline exceeded.", ex);
            return ex is RpcException rpc ? ErrorMapper.ToSpiceDBException(rpc) : ex;
        }
        public async Task Backoff(int attempt)
        {
            Check();
            try { await Task.Delay(JitteredDelay(TimeSpan.FromMilliseconds(100 * (1 << attempt))), Token); }
            catch (OperationCanceledException ex) { throw Map(ex); }
            Check();
        }
        public void Dispose() => _linked.Dispose();
    }
    private static async Task<T> Unary<T>(Budget budget, Func<DateTime?, CancellationToken, AsyncUnaryCall<T>> call, bool retry = true)
    {
        for (var attempt = 0; ; attempt++)
        {
            budget.Check();
            try { using var rpc = call(budget.Deadline, budget.Token); return await rpc.ResponseAsync.WaitAsync(budget.Token); }
            catch (RpcException ex) when (retry && attempt < 3 && ErrorMapper.IsTransient(ex)) { budget.Check(); await budget.Backoff(attempt); }
            catch (Exception ex) when (ex is RpcException or OperationCanceledException) { throw budget.Map(ex); }
        }
    }
    private static async IAsyncEnumerable<T> Stream<T>(Budget budget, Func<DateTime?, CancellationToken, AsyncServerStreamingCall<T>> open)
    {
        bool exposed = false;
        for (int attempt = 0; ; attempt++)
        {
            budget.Check();
            AsyncServerStreamingCall<T>? call = null;
            bool retry = false;
            try { call = open(budget.Deadline, budget.Token); }
            catch (RpcException ex) when (attempt < 3 && ErrorMapper.IsTransient(ex)) { retry = true; }
            catch (Exception ex) when (ex is RpcException or OperationCanceledException) { throw budget.Map(ex); }
            if (call is not null)
            {
                using (call)
                using (budget.Token.Register(call.Dispose))
                {
                    while (true)
                    {
                        budget.Check();
                        bool next;
                        try { next = await call.ResponseStream.MoveNext(budget.Token).WaitAsync(budget.Token); }
                        catch (RpcException ex) when (!exposed && attempt < 3 && ErrorMapper.IsTransient(ex)) { retry = true; break; }
                        catch (Exception ex) when (ex is RpcException or OperationCanceledException) { throw budget.Map(ex); }
                        if (!next) yield break;
                        exposed = true;
                        yield return call.ResponseStream.Current;
                    }
                }
            }
            if (!retry) yield break;
            await budget.Backoff(attempt);
        }
    }

    public async Task<CheckResult> CheckAsync(PermissionCheck request, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        if (request is null) throw new InvalidArgumentException("Request is required.");
        var wire = new CheckPermissionRequest
        {
            Resource = request.Resource.ToProto(),
            Permission = request.Permission,
            Subject = request.Subject.ToProto(),
            Consistency = request.Consistency.ToProto(),
            Context = MergeCheckContext(options?.Context, request.Context),
            WithTracing = options?.Tracing == CheckTracing.Enabled
        };
        var response = await Unary(budget, (d, c) => _permissions.CheckPermissionAsync(wire, deadline: d, cancellationToken: c));
        return new(ToCheckPermissionship(response.Permissionship), response.PartialCaveatInfo?.MissingRequiredContext ?? Enumerable.Empty<string>(), new(response.CheckedAt?.Token ?? ""), DebugInformation.FromProto(response.DebugTrace), response.OptionalExpiresAt?.ToDateTimeOffset());
    }
    public async Task<IReadOnlyList<CheckResult>> CheckBulkAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        if (requests is null) throw new InvalidArgumentException("Requests are required.");
        var items = requests.ToArray();
        if (items.Any(x => x is null)) throw new InvalidArgumentException("Requests must not contain null.");
        if (items.Length == 0) return Array.Empty<CheckResult>();
        if (items.Any(x => x.Consistency != items[0].Consistency)) throw new InvalidArgumentException("Bulk requests must have equal consistency strategies and tokens.");
        var results = new List<CheckResult>(items.Length);
        for (int offset = 0; offset < items.Length; offset += 1000)
        {
            budget.Check();
            var wire = new CheckBulkPermissionsRequest { Consistency = items[0].Consistency.ToProto(), WithTracing = options?.Tracing == CheckTracing.Enabled };
            foreach (var item in items.Skip(offset).Take(1000)) wire.Items.Add(new CheckBulkPermissionsRequestItem
            { Resource = item.Resource.ToProto(), Permission = item.Permission, Subject = item.Subject.ToProto(), Context = MergeCheckContext(options?.Context, item.Context) });
            var response = await Unary(budget, (d, c) => _permissions.CheckBulkPermissionsAsync(wire, deadline: d, cancellationToken: c));
            if (response.Pairs.Count != wire.Items.Count) throw new SpiceDBException($"Bulk response returned {response.Pairs.Count} pair(s) for {wire.Items.Count} request item(s).");
            foreach (var pair in response.Pairs)
            {
                if (!wire.Items[results.Count - offset].Equals(pair.Request))
                    throw new SpiceDBException($"check item {results.Count}: bulk response echoed request does not match its input.") { InputIndex = results.Count };
                if (pair.Error is { } error)
                {
                    var exception = ErrorMapper.ToSpiceDBException(new RpcException(new Grpc.Core.Status((StatusCode)error.Code, error.Message), new Metadata { { "grpc-status-details-bin", error.ToByteArray() } }));
                    exception.InputIndex = results.Count;
                    throw exception;
                }
                if (pair.Item is null) throw new SpiceDBException($"check item {results.Count}: bulk response is missing its result.") { InputIndex = results.Count };
                results.Add(ToCheckResult(pair.Item, response.CheckedAt?.Token ?? ""));
            }
        }
        return results.AsReadOnly();
    }
    public async Task<bool> CheckAnyAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default) => (await CheckBulkAsync(requests, options, cancellationToken)).Any(r => r.HasPermission);
    public async Task<bool> CheckAllAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default)
    { var results = await CheckBulkAsync(requests, options, cancellationToken); return results.Count > 0 && results.All(r => r.HasPermission); }

    public async Task<WriteResult> WriteAsync(Transaction transaction, WriteOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        if (transaction is null) throw new InvalidArgumentException("Transaction is required.");
        var request = new WriteRelationshipsRequest { OptionalTransactionMetadata = Native.Struct(options?.Metadata) };
        request.Updates.AddRange(transaction.V1Updates); request.OptionalPreconditions.AddRange(transaction.Preconditions);
        var response = await Unary(budget, (d, c) => _permissions.WriteRelationshipsAsync(request, deadline: d, cancellationToken: c), false);
        return new(new ZedToken(response.WrittenAt?.Token ?? ""));
    }
    private static int Positive(int value, string field) => value > 0 ? value : throw new InvalidArgumentException($"{field} must be positive.");
    public async Task<DeleteResult> DeleteRelationshipsAsync(Filter filter, int limit, DeleteOptions? options = null, CancellationToken cancellationToken = default)
    {
        bool inFlight = false;
        try
        {
            using var budget = Begin(options?.Timeout, cancellationToken);
            Positive(limit, nameof(limit));
            Native.Required(filter, nameof(filter)).ToProto();
            foreach (var precondition in (options?.MustMatch ?? []).Concat(options?.MustNotMatch ?? [])) precondition.ToProto();
            budget.Check();
            inFlight = true;
            return await DeletePage(budget, filter, limit, options?.ResumeCursor, options?.MustMatch, options?.MustNotMatch, options?.Metadata);
        }
        catch (Exception ex)
        {
            var progress = new DeleteProgress(0, null, null, 0, inFlight);
            if (ex is SpiceDBException typed) typed.DeleteProgress ??= progress;
            if (ex is OperationCanceledException) ex.Data["SpiceDB.DeleteProgress"] = progress;
            throw;
        }
    }
    private async Task<DeleteResult> DeletePage(Budget budget, Filter filter, int limit, string? cursor, IReadOnlyList<Filter>? matches, IReadOnlyList<Filter>? notMatches, IReadOnlyDictionary<string, object?>? metadata)
    {
        if (filter is null) throw new InvalidArgumentException("Filter is required.");
        var request = new DeleteRelationshipsRequest
        {
            RelationshipFilter = Native.Required(filter, nameof(filter)).ToProto(),
            OptionalLimit = (uint)limit,
            OptionalAllowPartialDeletions = true,
            OptionalTransactionMetadata = Native.Struct(metadata),
            OptionalCursor = string.IsNullOrEmpty(cursor) ? null : new() { Token = cursor }
        };
        foreach (var item in matches ?? []) request.OptionalPreconditions.Add(Transaction.BuildPrecondition(Precondition.Types.Operation.MustMatch, item));
        foreach (var item in notMatches ?? []) request.OptionalPreconditions.Add(Transaction.BuildPrecondition(Precondition.Types.Operation.MustNotMatch, item));
        var response = await Unary(budget, (d, c) => _permissions.DeleteRelationshipsAsync(request, deadline: d, cancellationToken: c), false);
        if (response.DeletionProgress is not (DeleteRelationshipsResponse.Types.DeletionProgress.Complete or DeleteRelationshipsResponse.Types.DeletionProgress.Partial))
            throw new SpiceDBException("Invalid server deletion progress.") { DeleteProgress = new(response.RelationshipsDeletedCount, new ZedToken(response.DeletedAt?.Token ?? ""), response.AfterResultCursor?.Token, 1, false) };
        return new(new(response.DeletedAt?.Token ?? ""), response.RelationshipsDeletedCount, response.DeletionProgress == DeleteRelationshipsResponse.Types.DeletionProgress.Complete, response.AfterResultCursor?.Token);
    }
    /// <summary>Deletes all matches in separate transactional batches. The overall operation is non-atomic: completed batches remain committed if a later batch fails or is canceled.</summary>
    public async Task<DeleteResult> DeleteAllRelationshipsNonAtomicAsync(Filter filter, DeleteAllOptions? options = null, CancellationToken cancellationToken = default)
    {
        int pages = 0;
        ulong count = 0; ZedToken? token = null; string? cursor = options?.ResumeCursor; bool inFlight = false;
        try
        {
            using var budget = Begin(options?.Timeout, cancellationToken);
            int size = Positive(options?.PageSize ?? 1000, "PageSize");
            Native.Required(filter, nameof(filter)).ToProto();
            foreach (var precondition in (options?.MustMatch ?? []).Concat(options?.MustNotMatch ?? [])) precondition.ToProto();
            while (true)
            {
                budget.Check(); inFlight = true;
                var page = await DeletePage(budget, filter, size, cursor, options?.MustMatch, options?.MustNotMatch, options?.Metadata);
                inFlight = false; pages++; count += page.DeletedCount; token = page.Revision; cursor = page.ResumeCursor;
                if (page.Completed) return page with { DeletedCount = count };
            }
        }
        catch (Exception ex)
        {
            var progress = new DeleteProgress(count, token, pages == 0 ? null : cursor, pages, inFlight);
            if (ex is SpiceDBException typed)
            {
                if (typed.DeleteProgress is { } acknowledged)
                    progress = new(count + acknowledged.AcknowledgedCount, acknowledged.LastToken ?? token, acknowledged.LastCursor, pages + acknowledged.CompletedPages, acknowledged.OutcomeUnknown);
                typed.DeleteProgress = progress;
            }
            if (ex is OperationCanceledException) ex.Data["SpiceDB.DeleteProgress"] = progress;
            throw;
        }
    }

    public async IAsyncEnumerable<Relationship> ReadRelationshipsAsync(Consistency consistency, Filter filter, ReadOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false);
        int size = Positive(options?.PageSize ?? 512, "PageSize"), remaining = Positive(options?.Limit ?? int.MaxValue, "Limit");
        var request = new ReadRelationshipsRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), RelationshipFilter = Native.Required(filter, nameof(filter)).ToProto() };
        while (remaining > 0)
        {
            request.OptionalLimit = (uint)Math.Min(size, remaining); int count = 0;
            await foreach (var response in Stream(budget, (d, c) => _permissions.ReadRelationships(request, deadline: d, cancellationToken: c)))
            {
                request.OptionalCursor = response.AfterResultCursor; count++; remaining--;
                yield return Relationship.FromProto(response.Relationship);
                if (remaining == 0) yield break;
            }
            if (count < request.OptionalLimit || request.OptionalCursor is null) yield break;
        }
    }
    public async IAsyncEnumerable<LookupResource> LookupResourcesAsync(Consistency consistency, string resourceType, string permission, SubjectRef subject, LookupOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false);
        if (options?.SubjectRelation is not null) throw new InvalidArgumentException("LookupResources uses the supplied SubjectRef relation.");
        int size = Positive(options?.PageSize ?? 512, "PageSize"), remaining = Positive(options?.Limit ?? int.MaxValue, "Limit");
        var request = new LookupResourcesRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), ResourceObjectType = Native.Required(resourceType, nameof(resourceType)), Permission = Native.Required(permission, nameof(permission)), Subject = Native.Required(subject, nameof(subject)).ToProto(), Context = Native.Struct(options?.Context), WithDebug = options?.Debug == LookupDebug.Enabled };
        while (remaining > 0)
        {
            request.OptionalLimit = (uint)Math.Min(size, remaining); int count = 0;
            await foreach (var response in Stream(budget, (d, c) => _permissions.LookupResources(request, deadline: d, cancellationToken: c)))
            {
                request.OptionalCursor = response.AfterResultCursor; count++;
                if (options?.PermissionshipFilter == LookupPermissionshipFilter.HasPermissionOnly && response.Permissionship != LookupPermissionship.HasPermission) continue;
                remaining--;
                yield return new() { ResourceID = response.ResourceObjectId, Permissionship = ToPermissionship(response.Permissionship), PartialCaveat = ToPartialCaveatInfo(response.PartialCaveatInfo), LookedUpAt = new(response.LookedUpAt?.Token ?? "") };
                if (remaining == 0) yield break;
            }
            if (count < request.OptionalLimit || request.OptionalCursor is null) yield break;
        }
    }
    public async IAsyncEnumerable<LookupSubject> LookupSubjectsAsync(Consistency consistency, ObjectRef resource, string permission, string subjectType, LookupOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false);
        if (options is not null && options.PermissionshipFilter != LookupPermissionshipFilter.All) throw new InvalidArgumentException("LookupSubjects does not support PermissionshipFilter.");
        if (options?.Debug == LookupDebug.Enabled || options?.PageSize is not null) throw new InvalidArgumentException("LookupSubjects does not support Debug or PageSize; upstream concrete pagination is unimplemented.");
        int remaining = Positive(options?.Limit ?? int.MaxValue, "Limit");
        var request = new LookupSubjectsRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), Resource = Native.Required(resource, nameof(resource)).ToProto(), Permission = Native.Required(permission, nameof(permission)), SubjectObjectType = Native.Required(subjectType, nameof(subjectType)), OptionalSubjectRelation = options?.SubjectRelation ?? "", Context = Native.Struct(options?.Context) };
        await foreach (var response in Stream(budget, (d, c) => _permissions.LookupSubjects(request, deadline: d, cancellationToken: c)))
        {
            yield return ToLookupSubject(response);
            if (--remaining == 0) yield break;
        }
    }
    public async Task<ulong> ImportRelationshipsAsync(IAsyncEnumerable<Relationship> relationships, ImportOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false);
        int size = Positive(options?.BatchSize ?? 1000, "BatchSize");
        try
        {
            using var call = _permissions.ImportBulkRelationships(deadline: budget.Deadline, cancellationToken: budget.Token);
            using var registration = budget.Token.Register(call.Dispose);
            using var producer = CancellationTokenSource.CreateLinkedTokenSource(budget.Token);
            async Task ObserveResponse()
            {
                try { await call.ResponseAsync; }
                catch (Exception) { /* The upload path below preserves the response error. */ }
                finally { producer.Cancel(); }
            }
            var responseFinished = ObserveResponse();
            try
            {
                var batch = new ImportBulkRelationshipsRequest();
                await foreach (var relationship in Native.Required(relationships, nameof(relationships)).WithCancellation(producer.Token))
                {
                    budget.Check(); batch.Relationships.Add(relationship.ToProto());
                    if (batch.Relationships.Count == size) { await call.RequestStream.WriteAsync(batch).WaitAsync(producer.Token); batch = new(); }
                }
                if (batch.Relationships.Count > 0) await call.RequestStream.WriteAsync(batch).WaitAsync(producer.Token);
                await call.RequestStream.CompleteAsync().WaitAsync(producer.Token);
                return (await call.ResponseAsync.WaitAsync(budget.Token)).NumLoaded;
            }
            catch (OperationCanceledException) when (!budget.Token.IsCancellationRequested && call.ResponseAsync.IsCompleted)
            {
                // Server completion interrupts a cooperative producer, but is not caller cancellation.
                return (await call.ResponseAsync).NumLoaded;
            }
            finally
            {
                call.Dispose();
                await responseFinished;
            }
        }
        catch (Exception ex) when (ex is RpcException or OperationCanceledException) { throw budget.Map(ex); }
    }
    public async IAsyncEnumerable<Relationship> ExportRelationshipsAsync(Consistency consistency, Filter filter, ExportOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false);
        int size = Positive(options?.PageSize ?? 512, "PageSize"), remaining = Positive(options?.Limit ?? int.MaxValue, "Limit");
        var request = new ExportBulkRelationshipsRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), OptionalRelationshipFilter = Native.Required(filter, nameof(filter)).ToProto() };
        while (remaining > 0)
        {
            request.OptionalLimit = (uint)Math.Min(size, remaining); int count = 0;
            await foreach (var response in Stream(budget, (d, c) => _permissions.ExportBulkRelationships(request, deadline: d, cancellationToken: c)))
            {
                request.OptionalCursor = response.AfterResultCursor;
                foreach (var relationship in response.Relationships)
                {
                    count++; remaining--; yield return Relationship.FromProto(relationship);
                    if (remaining == 0) yield break;
                }
            }
            if (count < request.OptionalLimit || request.OptionalCursor is null) yield break;
        }
    }
    public async IAsyncEnumerable<WatchEvent> UpdatesAsync(IReadOnlyList<string> objectTypes, ZedToken? startRevision, WatchOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false);
        var request = new WatchRequest { OptionalStartCursor = startRevision is { } token ? new() { Token = token.Value } : null };
        request.OptionalObjectTypes.AddRange(Native.Required(objectTypes, nameof(objectTypes)).ToArray());
        if (request.OptionalObjectTypes.Count > 0 && (options?.RelationshipFilters.Count ?? 0) > 0)
            throw new InvalidArgumentException("Object types and relationship filters are mutually exclusive.");
        request.OptionalRelationshipFilters.AddRange((options?.RelationshipFilters ?? []).Select(x => x.ToProto()));
        var content = options?.Content ?? WatchContent.Relationships;
        if (content.HasFlag(WatchContent.Relationships)) request.OptionalUpdateKinds.Add(WatchKind.IncludeRelationshipUpdates);
        if (content.HasFlag(WatchContent.Checkpoints)) request.OptionalUpdateKinds.Add(WatchKind.IncludeCheckpoints);
        if (content.HasFlag(WatchContent.SchemaUpdates)) request.OptionalUpdateKinds.Add(WatchKind.IncludeSchemaUpdates);
        await foreach (var response in Stream(budget, (d, c) => _watch.Watch(request, deadline: d, cancellationToken: c)))
            yield return new(response.Updates.Select(UpdateFromProto), new(response.ChangesThrough?.Token ?? ""), response.IsCheckpoint, response.SchemaUpdated, Native.FromStruct(response.OptionalTransactionMetadata), response.FullRevisionMetadata.Select(x => Native.FromStruct(x)!));
    }
    public async Task<SchemaResult> ReadSchemaAsync(
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate();
        var resp = await Unary(budget, (deadline, token) => _schema.ReadSchemaAsync(
                new ReadSchemaRequest(),
                deadline: deadline,
                cancellationToken: token));

        return new(resp.SchemaText, new(resp.ReadAt?.Token ?? ""));
    }

    public async Task<ZedToken> WriteSchemaAsync(
        string schema,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate();
        if (string.IsNullOrEmpty(schema))
            throw new InvalidArgumentException("Schema must not be empty.");

        var resp = await Unary(budget, (deadline, token) => _schema.WriteSchemaAsync(
                new WriteSchemaRequest { Schema = schema },
                deadline: deadline,
                cancellationToken: token), false);

        return new(resp.WrittenAt?.Token ?? "");
    }

    public async Task<ReflectSchemaResult> ReflectSchemaAsync(
        Consistency consistency,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate(reflect: true);
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, (deadline, token) => _schema.ReflectSchemaAsync(
                new ReflectSchemaRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), OptionalFilters = { (options?.SchemaFilters ?? []).Select(x => x.ToProto()) } },
                deadline: deadline,
                cancellationToken: token));

        var definitions = new List<SchemaDefinition>();
        var caveats = new List<SchemaCaveat>();

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
                    SubjectTypes = r.SubjectTypes.Select(SchemaTypeReference.FromProto).ToArray(),
                }).ToList(),
                Permissions = def.Permissions.Select(p => new SchemaPermission
                {
                    Name = p.Name,
                    Comment = p.Comment,
                    ParentDefinitionName = p.ParentDefinitionName,
                }).ToList(),
            };
            definitions.Add(sd);
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
            caveats.Add(sc);
        }

        return new ReflectSchemaResult { Definitions = definitions, Caveats = caveats, Revision = new(resp.ReadAt?.Token ?? "") };
    }

    public async Task<ComputablePermissionsResult> ComputablePermissionsAsync(
        Consistency consistency,
        string definitionName,
        string relationName,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate(computable: true);
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, (deadline, token) => _schema.ComputablePermissionsAsync(
                new ComputablePermissionsRequest
                {
                    Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
                    DefinitionName = definitionName,
                    RelationName = relationName,
                    OptionalDefinitionNameFilter = options?.DefinitionNameFilter ?? "",
                },
                deadline: deadline,
                cancellationToken: token));

        var refs = resp.Permissions.Select(p => new RelationReference
        {
            DefinitionName = p.DefinitionName,
            RelationName = p.RelationName,
            IsPermission = p.IsPermission,
        }).ToList();

        return new(refs, new(resp.ReadAt?.Token ?? ""));
    }

    public async Task<DependentRelationsResult> DependentRelationsAsync(
        Consistency consistency,
        string definitionName,
        string permissionName,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate();
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, (deadline, token) => _schema.DependentRelationsAsync(
                new DependentRelationsRequest
                {
                    Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
                    DefinitionName = definitionName,
                    PermissionName = permissionName,
                },
                deadline: deadline,
                cancellationToken: token));

        var refs = resp.Relations.Select(r => new RelationReference
        {
            DefinitionName = r.DefinitionName,
            RelationName = r.RelationName,
            IsPermission = r.IsPermission,
        }).ToList();

        return new(refs, new(resp.ReadAt?.Token ?? ""));
    }

    public async Task<DiffSchemaResult> DiffSchemaAsync(
        Consistency consistency,
        string comparisonSchema,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate();
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, (deadline, token) => _schema.DiffSchemaAsync(
                new DiffSchemaRequest
                {
                    Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
                    ComparisonSchema = comparisonSchema,
                },
                deadline: deadline,
                cancellationToken: token));

        var diffs = resp.Diffs.Select(SchemaDiff.FromProto).ToList();
        return new(diffs, new(resp.ReadAt?.Token ?? ""));
    }

    // ──────────────────────────────────────────────────────────────────────
    // Expand
    // ──────────────────────────────────────────────────────────────────────

    public async Task<ExpandResult> ExpandPermissionTreeAsync(
        Consistency consistency,
        ObjectRef resource,
        string permission,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate();
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, (deadline, token) => _permissions.ExpandPermissionTreeAsync(
                new ExpandPermissionTreeRequest
                {
                    Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
                    Resource = Native.Required(resource, nameof(resource)).ToProto(),
                    Permission = permission,
                },
                deadline: deadline,
                cancellationToken: token));

        return new ExpandResult
        {
            Tree = ToPermissionTree(resp.TreeRoot),
            Revision = new(resp.ExpandedAt?.Token ?? ""),
        };
    }

    public async Task ExperimentalRegisterRelationshipCounterAsync(
        string name,
        Filter filter,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate();
        if (string.IsNullOrEmpty(name))
            throw new InvalidArgumentException("Name must not be empty.");
        Native.Required(filter, nameof(filter));

        await Unary(budget, (deadline, token) => _experimental.ExperimentalRegisterRelationshipCounterAsync(
                new ExperimentalRegisterRelationshipCounterRequest
                {
                    Name = name,
                    RelationshipFilter = Native.Required(filter, nameof(filter)).ToProto(),
                },
                deadline: deadline,
                cancellationToken: token), false);
    }

    public async Task<CountResult> ExperimentalCountRelationshipsAsync(
        string name,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate();
        if (string.IsNullOrEmpty(name))
            throw new InvalidArgumentException("Name must not be empty.");

        var resp = await Unary(budget, (deadline, token) => _experimental.ExperimentalCountRelationshipsAsync(
                new ExperimentalCountRelationshipsRequest { Name = name },
                deadline: deadline,
                cancellationToken: token));

        if (resp.CounterStillCalculating)
            return new CountResult { StillCalculating = true };

        var cv = resp.ReadCounterValue ?? throw new SpiceDBException("Counter response does not contain a count or calculating state.");
        return new CountResult
        {
            RelationshipCount = cv.RelationshipCount,
            Revision = new(cv.ReadAt?.Token ?? ""),
        };
    }

    public async Task ExperimentalUnregisterRelationshipCounterAsync(
        string name,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        options?.Validate();
        if (string.IsNullOrEmpty(name))
            throw new InvalidArgumentException("Name must not be empty.");

        await Unary(budget, (deadline, token) => _experimental.ExperimentalUnregisterRelationshipCounterAsync(
                new ExperimentalUnregisterRelationshipCounterRequest { Name = name },
                deadline: deadline,
                cancellationToken: token), false);
    }

    // ──────────────────────────────────────────────────────────────────────
    // Experimental — Roaring Lookup Resources
    // ──────────────────────────────────────────────────────────────────────

    public async Task<RoaringLookupResourcesResult> ExperimentalRoaringLookupResourcesAsync(
        Consistency consistency,
        string resourceType,
        string permission,
        SubjectRef subject,
        LookupOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken);
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        if (options is not null && (options.Debug == LookupDebug.Enabled || options.Context is not null || options.PageSize is not null || options.Limit is not null || options.SubjectRelation is not null))
            throw new InvalidArgumentException("Roaring lookup supports Timeout only; context, debug, limits and subject relation options are unsupported. Put a subject relation on SubjectRef.");
        var resp = await Unary(budget, (deadline, token) => _materialize.ExperimentalRoaringLookupResourcesAsync(
                new MaterializeV0.ExperimentalRoaringLookupResourcesRequest
                {
                    Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
                    ResourceObjectType = resourceType,
                    Permission = permission,
                    Subject = Native.Required(subject, nameof(subject)).ToProto(),
                },
                deadline: deadline,
                cancellationToken: token));

        return new RoaringLookupResourcesResult
        {
            Bitmap = Array.AsReadOnly(resp.Bitmap.ToByteArray()),
            Cardinality = resp.Cardinality,
            AtRevision = new(resp.AtRevision?.Token ?? ""),
        };
    }

    internal static Permissionship ToPermissionship(Authzed.Api.V1.LookupPermissionship v) => v switch
    {
        Authzed.Api.V1.LookupPermissionship.HasPermission => Permissionship.HasPermission,
        Authzed.Api.V1.LookupPermissionship.ConditionalPermission => Permissionship.ConditionalPermission,
        _ => Permissionship.Unspecified,
    };

    internal static PartialCaveatInfo? ToPartialCaveatInfo(Authzed.Api.V1.PartialCaveatInfo? v)
    {
        if (v == null)
            return null;
        return new PartialCaveatInfo { MissingRequiredContext = v.MissingRequiredContext.ToList() };
    }

    internal static Permissionship ToCheckPermissionship(CheckPermissionResponse.Types.Permissionship v) => v switch
    {
        CheckPermissionResponse.Types.Permissionship.HasPermission => Permissionship.HasPermission,
        CheckPermissionResponse.Types.Permissionship.NoPermission => Permissionship.NoPermission,
        CheckPermissionResponse.Types.Permissionship.ConditionalPermission => Permissionship.ConditionalPermission,
        _ => Permissionship.Unspecified,
    };

    internal static CheckResult ToCheckResult(CheckBulkPermissionsResponseItem item, string checkedAt) => new(
        ToCheckPermissionship(item.Permissionship), item.PartialCaveatInfo?.MissingRequiredContext ?? Enumerable.Empty<string>(), new(checkedAt), DebugInformation.FromProto(item.DebugTrace));

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
            LookedUpAt = new(resp.LookedUpAt?.Token ?? ""),
        };
    }

    internal static PermissionTree ToPermissionTree(PermissionRelationshipTree? t)
    {
        if (t is null) return new();
        var resource = t.ExpandedObject is null ? null : ObjectRef.FromProto(t.ExpandedObject);
        return t.TreeTypeCase switch
        {
            PermissionRelationshipTree.TreeTypeOneofCase.Intermediate => new(resource, t.ExpandedRelation, intermediate: new IntermediateNode
            { Operation = ToTreeOperation(t.Intermediate.Operation), Children = t.Intermediate.Children.Select(ToPermissionTree).ToArray() }),
            PermissionRelationshipTree.TreeTypeOneofCase.Leaf => new(resource, t.ExpandedRelation, leaf: new LeafNode
            { Subjects = t.Leaf.Subjects.Select(SubjectRef.FromProto).ToArray() }),
            _ => new(resource, t.ExpandedRelation),
        };
    }

    private static TreeOperation ToTreeOperation(AlgebraicSubjectSet.Types.Operation op) => op switch
    {
        AlgebraicSubjectSet.Types.Operation.Union => TreeOperation.Union,
        AlgebraicSubjectSet.Types.Operation.Intersection => TreeOperation.Intersection,
        AlgebraicSubjectSet.Types.Operation.Exclusion => TreeOperation.Exclusion,
        _ => TreeOperation.Unspecified,
    };

    private static RelationshipUpdate UpdateFromProto(Authzed.Api.V1.RelationshipUpdate pu)
    {
        // Server-supplied data: an unrecognized operation (OPERATION_UNSPECIFIED, or a future
        // wire value added after this client shipped) MUST NOT map to a write. Mirrors
        // ToTreeOperation directly above and both permissionship mappers in this file, which
        // already map an unrecognized server enum to their safe Unspecified value rather than
        // raising or guessing. Root DESIGN.md, "RULE: A conversion that cannot preserve meaning
        // must fail", clause 2: server-supplied values the client does not recognise MUST NOT
        // raise, and MUST map to the safe, non-permissive default -- never a grant, and never a
        // write. Mapping to Touch here would let a cache or index mirror consuming the watch
        // stream upsert a relationship that may in fact have been deleted.
        var op = pu.Operation switch
        {
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Create => UpdateOperation.Create,
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch => UpdateOperation.Touch,
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Delete => UpdateOperation.Delete,
            _ => UpdateOperation.Unspecified,
        };
        return new RelationshipUpdate(op, Relationship.FromProto(pu.Relationship));
    }

    internal static Struct? MergeCheckContext(IReadOnlyDictionary<string, object?>? defaults, IReadOnlyDictionary<string, object?>? item)
    {
        if (defaults is null && item is null) return null;
        var merged = defaults?.ToDictionary(p => p.Key, p => p.Value) ?? new Dictionary<string, object?>();
        if (item is not null) foreach (var pair in item) merged[pair.Key] = pair.Value;
        return Native.Struct(Native.Context(merged));
    }
    internal static Value ToProtoValue(object? value) => Native.Value(value);
    internal static Value ToProtoValueForKey(string key, object? value) => Native.Struct(Native.Context(new Dictionary<string, object?> { [key] = value }))!.Fields[key];
    internal static object? FromProtoValue(Value value) => Native.FromValue(value);
    internal static IReadOnlyDictionary<string, object?> FromProtoStruct(Struct value) => Native.FromStruct(value)!;
}
/// <summary>Represents a definition in a SpiceDB schema.</summary>
public sealed record SchemaDefinition
{
    public string Name { get; init; } = "";
    public string Comment { get; init; } = "";
    private IReadOnlyList<SchemaRelation> _Relations = Array.Empty<SchemaRelation>();
    public IReadOnlyList<SchemaRelation> Relations { get => _Relations; init => _Relations = Native.List(value); }
    private IReadOnlyList<SchemaPermission> _Permissions = Array.Empty<SchemaPermission>();
    public IReadOnlyList<SchemaPermission> Permissions { get => _Permissions; init => _Permissions = Native.List(value); }
}

/// <summary>Represents a relation within a schema definition.</summary>
public sealed record SchemaRelation
{
    private IReadOnlyList<SchemaTypeReference> _subjectTypes = Array.Empty<SchemaTypeReference>();
    public IReadOnlyList<SchemaTypeReference> SubjectTypes { get => _subjectTypes; init => _subjectTypes = Native.List(value); }
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
    private IReadOnlyList<SchemaCaveatParameter> _Parameters = Array.Empty<SchemaCaveatParameter>();
    public IReadOnlyList<SchemaCaveatParameter> Parameters { get => _Parameters; init => _Parameters = Native.List(value); }
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
    private IReadOnlyList<SchemaDefinition> _Definitions = Array.Empty<SchemaDefinition>();
    public IReadOnlyList<SchemaDefinition> Definitions { get => _Definitions; init => _Definitions = Native.List(value); }
    private IReadOnlyList<SchemaCaveat> _Caveats = Array.Empty<SchemaCaveat>();
    public IReadOnlyList<SchemaCaveat> Caveats { get => _Caveats; init => _Caveats = Native.List(value); }
    public ZedToken Revision { get; init; }
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

    public SchemaDefinition? Definition { get; init; }
    public SchemaRelation? Relation { get; init; }
    public SchemaPermission? Permission { get; init; }
    public SchemaCaveat? Caveat { get; init; }
    public SchemaCaveatParameter? Parameter { get; init; }
    public SchemaTypeReference? ChangedSubjectType { get; init; }
    public string? PreviousType { get; init; }
    internal static SchemaDiff FromProto(ReflectionSchemaDiff d)
    {
        var result = MapSummary(d);
        return result with
        {
            Definition = SchemaMapping.Definition(d.DefinitionAdded ?? d.DefinitionRemoved ?? d.DefinitionDocCommentChanged),
            Relation = SchemaMapping.Relation(d.RelationAdded ?? d.RelationRemoved ?? d.RelationDocCommentChanged ?? d.RelationSubjectTypeAdded?.Relation ?? d.RelationSubjectTypeRemoved?.Relation),
            Permission = SchemaMapping.Permission(d.PermissionAdded ?? d.PermissionRemoved ?? d.PermissionDocCommentChanged ?? d.PermissionExprChanged),
            Caveat = SchemaMapping.Caveat(d.CaveatAdded ?? d.CaveatRemoved ?? d.CaveatDocCommentChanged ?? d.CaveatExprChanged),
            Parameter = SchemaMapping.Parameter(d.CaveatParameterAdded ?? d.CaveatParameterRemoved ?? d.CaveatParameterTypeChanged?.Parameter),
            ChangedSubjectType = (d.RelationSubjectTypeAdded?.ChangedSubjectType ?? d.RelationSubjectTypeRemoved?.ChangedSubjectType) is { } type ? SchemaTypeReference.FromProto(type) : null,
            PreviousType = d.CaveatParameterTypeChanged?.PreviousType,
        };
    }
    private static SchemaDiff MapSummary(ReflectionSchemaDiff d)
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
    public ZedToken Revision { get; init; }
}

/// <summary>Holds the result of a relationship count operation.</summary>
public sealed record CountResult
{
    public bool StillCalculating { get; init; }
    public ulong RelationshipCount { get; init; }
    public ZedToken Revision { get; init; }
}

/// <summary>
/// Holds the result of <see cref="SpiceDBClient.ExperimentalRoaringLookupResourcesAsync"/>.
/// </summary>
public sealed record RoaringLookupResourcesResult
{
    /// <summary>
    /// The roaring64 bitmap, in RoaringFormatSpec 64-bit portable format, of the
    /// resource object IDs accessible to the subject. IDs are the object IDs from
    /// the relationships themselves, as 44-bit integers -- this client does not
    /// decode the bitmap.
    /// </summary>
    private IReadOnlyList<byte> _Bitmap = Array.Empty<byte>();
    public IReadOnlyList<byte> Bitmap { get => _Bitmap; init => _Bitmap = Native.List(value); }

    /// <summary>The number of resource IDs in <see cref="Bitmap"/>.</summary>
    public ulong Cardinality { get; init; }

    /// <summary>The ZedToken revision the lookup was performed at.</summary>
    public ZedToken AtRevision { get; init; }
}
