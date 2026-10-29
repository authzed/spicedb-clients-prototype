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

/// <summary>
/// Native asynchronous SpiceDB API. RawProto is the authenticated secondary protocol escape hatch.
/// Integrating with an AI coding agent? Start with spicedb-csharp/AI_INTEGRATION_GUIDE.md: tier
/// choice, fail-closed checks, atomic writes, paging, tooling options, errors and test doubles.
/// </summary>
public sealed partial class SpiceDBClient : ISpiceDBClient
{
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(30);
    private readonly SpiceDBProtoClient? _protoClient;
    private readonly PermissionsService.PermissionsServiceClient _permissions;
    private readonly SchemaService.SchemaServiceClient _schema;
    private readonly WatchService.WatchServiceClient _watch;
    private readonly ExperimentalService.ExperimentalServiceClient _experimental;
    private readonly MaterializeV0.RoaringLookupResourcesService.RoaringLookupResourcesServiceClient _materialize;
    private readonly TimeSpan? _defaultTimeout;
    private readonly RetryPolicy? _retryPolicy;
    private readonly bool _streamRetries;
    private readonly Dictionary<string, RetryPolicy> _operationRetry;
    private readonly CancellationTokenSource _lifetime = new();
    private int _disposed;
    /// <summary>The endpoint this client dials, shown in connection-failure messages.</summary>
    private string? _endpoint;
    private SpiceDBClient(SpiceDBProtoClient proto, ClientOptions? options)
        : this(proto.Permissions, proto.Schema, proto.Watch, proto.Experimental, proto.Materialize, options)
    { _protoClient = proto; }
    internal SpiceDBClient(PermissionsService.PermissionsServiceClient permissions, SchemaService.SchemaServiceClient schema,
        WatchService.WatchServiceClient watch, ExperimentalService.ExperimentalServiceClient experimental,
        MaterializeV0.RoaringLookupResourcesService.RoaringLookupResourcesServiceClient? materialize = null, TimeSpan? defaultTimeout = null)
        : this(permissions, schema, watch, experimental, materialize, new ClientOptions { DefaultTimeout = defaultTimeout ?? DefaultTimeout }) { }
    internal SpiceDBClient(PermissionsService.PermissionsServiceClient permissions, SchemaService.SchemaServiceClient schema,
        WatchService.WatchServiceClient watch, ExperimentalService.ExperimentalServiceClient experimental,
        MaterializeV0.RoaringLookupResourcesService.RoaringLookupResourcesServiceClient? materialize, ClientOptions? options)
    {
        _permissions = permissions; _schema = schema; _watch = watch; _experimental = experimental; _materialize = materialize!;
        var timeout = ValidTimeout(options?.DefaultTimeout ?? DefaultTimeout);
        _defaultTimeout = options?.DefaultDeadline == DefaultDeadline.Disabled ? null : timeout;
        _retryPolicy = options?.Retries == Retries.Disabled ? null : (options?.RetryPolicy ?? RetryPolicy.Default).Validated();
        _streamRetries = options?.StreamRetries != StreamRetries.Disabled;
        options?.ValidateOperationRetryPolicies();
        // Keyed by method name, which Begin receives as its caller.
        _operationRetry = (options?.OperationRetryPolicies ?? new Dictionary<SpiceDBOperation, RetryPolicy>())
            .ToDictionary(e => e.Key + "Async", e => e.Value, StringComparer.Ordinal);
    }

    public static SpiceDBClient CreatePlaintext(string endpoint, string presharedKey, ClientOptions? options = null) => Create(endpoint, presharedKey, options, true);
    public static SpiceDBClient CreateSystemTls(string endpoint, string presharedKey, ClientOptions? options = null) => Create(endpoint, presharedKey, options, false);
    private static SpiceDBClient Create(string endpoint, string key, ClientOptions? options, bool insecure)
    {
        Native.Required(endpoint, nameof(endpoint)); Native.Required(key, "presharedKey");
        ValidTimeout(options?.DefaultTimeout ?? DefaultTimeout);
        (options?.RetryPolicy ?? RetryPolicy.Default).Validated(); options?.ValidateOperationRetryPolicies();
        try { return new(new SpiceDBProtoClient(endpoint, key, insecure, options?.RemotePlaintextCredentials == RemotePlaintextCredentials.Allow), options) { _endpoint = endpoint }; }
        catch (InsecureRemoteHostException ex) { throw new InvalidArgumentException(RemotePlaintextRefusal(endpoint), ex); }
        catch (Exception ex) when (ex is ArgumentException or InvalidOperationException or UriFormatException) { throw new InvalidArgumentException(ex.Message, ex); }
    }
    /// <summary>
    /// The remote-plaintext refusal, naming both settings a remote plaintext endpoint (a
    /// Docker Compose service such as <c>spicedb:50051</c>) needs, as the caller writes them.
    /// </summary>
    internal static string RemotePlaintextRefusal(string endpoint) =>
        $"spicedb: refusing to send the preshared key over plaintext to non-loopback endpoint \"{endpoint}\". " +
        "Use SpiceDBClient.CreateSystemTls for TLS, or, to send it in cleartext to a remote host on purpose, call " +
        "SpiceDBClient.CreatePlaintext(endpoint, presharedKey, new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow })";
    /// <summary>Borrows the channel. Arbitrary caller transport configuration can supersede endpoint security assumptions.</summary>
    public static SpiceDBClient CreateFromChannel(GrpcChannel channel, string presharedKey, ClientOptions? options = null)
    {
        if (channel is null) throw new InvalidArgumentException("Channel is required.");
        Native.Required(presharedKey, nameof(presharedKey));
        ValidTimeout(options?.DefaultTimeout ?? DefaultTimeout);
        (options?.RetryPolicy ?? RetryPolicy.Default).Validated(); options?.ValidateOperationRetryPolicies();
        // Target omits the scheme. Inspect the transport's parsed address and fail
        // closed if a future Grpc.Net.Client version stops exposing that metadata.
        var address = typeof(GrpcChannel).GetProperty("Address", System.Reflection.BindingFlags.Instance | System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.NonPublic)?.GetValue(channel) as Uri;
        if (!(options?.RemotePlaintextCredentials == RemotePlaintextCredentials.Allow) &&
            (address is null || (address.Scheme != "https" && !address.IsLoopback)))
            throw new InvalidArgumentException("spicedb: refusing to send the preshared key over a channel that is neither TLS nor loopback. To send it in cleartext to a remote host on purpose, call SpiceDBClient.CreateFromChannel(channel, presharedKey, new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow })");
        return new(new SpiceDBProtoClient(channel, presharedKey), options) { _endpoint = channel.Target };
    }
    /// <summary>
    /// Wraps an existing, caller-owned <see cref="CallInvoker"/>: an in-memory or test-server
    /// channel's invoker, an intercepted invoker, or <c>channel.CreateCallInvoker()</c>. The
    /// invoker already carries its own credentials, so this adds none, and
    /// <see cref="ClientOptions.RemotePlaintextCredentials"/> does not apply and is rejected.
    /// Operation options (deadlines, retry policy) still apply. <see cref="DisposeAsync"/>
    /// never disposes the underlying channel; <see cref="RawProto"/> shares the invoker.
    /// </summary>
    public static SpiceDBClient CreateFromCallInvoker(CallInvoker callInvoker, ClientOptions? options = null)
    {
        if (callInvoker is null) throw new InvalidArgumentException("CallInvoker is required.");
        if (options?.RemotePlaintextCredentials == RemotePlaintextCredentials.Allow)
            throw new InvalidArgumentException("RemotePlaintextCredentials does not apply to a caller-supplied CallInvoker, which already carries its transport and credentials.");
        ValidTimeout(options?.DefaultTimeout ?? DefaultTimeout);
        (options?.RetryPolicy ?? RetryPolicy.Default).Validated(); options?.ValidateOperationRetryPolicies();
        return new(new SpiceDBProtoClient(callInvoker), options);
    }
    public SpiceDBProtoClient RawProto() => _protoClient ?? throw new InvalidOperationException("The test client has no raw transport.");
    /// <summary>
    /// Startup readiness check. Constructors connect lazily, so a wrong or unreachable endpoint
    /// otherwise surfaces only on the first operation. This waits until the endpoint answers an
    /// authenticated request (a schema read; "no schema yet" counts as ready), within
    /// <see cref="ConnectOptions.Timeout"/> or the client default deadline. An endpoint that is
    /// not ready in time fails with <see cref="DeadlineExceededException"/> naming it; a refused
    /// connection that does not recover, with <see cref="UnavailableException"/>; a rejected
    /// preshared key, with <see cref="UnauthenticatedException"/>.
    /// </summary>
    public async Task ConnectAsync(ConnectOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, retry: RetryPolicy.None);
        try
        {
            // Wait-for-ready keeps the call pending while the channel connects, instead of failing
            // fast on the first refused connection, so a server that is still starting can pass.
            await Unary(budget, o => _schema.ReadSchemaAsync(new ReadSchemaRequest(), o.WithWaitForReady()), retry: false);
        }
        catch (NotFoundException) { /* Reachable and authenticated; no schema has been written yet. */ }
        catch (DeadlineExceededException ex)
        {
            var within = options?.Timeout ?? _defaultTimeout;
            throw new DeadlineExceededException($"spicedb: endpoint \"{_endpoint ?? "(caller-supplied transport)"}\" was not ready within {within}: {ex.Message}", ex);
        }
    }
    public ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref _disposed, 1) == 0) { _lifetime.Cancel(); _protoClient?.Dispose(); }
        return ValueTask.CompletedTask;
    }
    private static TimeSpan JitteredDelay(TimeSpan cap) => TimeSpan.FromMilliseconds(Random.Shared.NextDouble() * cap.TotalMilliseconds);
    private static TimeSpan ValidTimeout(TimeSpan value) => value <= TimeSpan.Zero || value.TotalMilliseconds > uint.MaxValue - 1 ? throw new InvalidArgumentException("Timeout must be positive and supported by the runtime timer.") : value;
    /// <summary>
    /// Starts an operation's budget. A per-call <paramref name="retry"/> policy replaces the
    /// client's for this call, including stream establishment. A <paramref name="watch"/> never
    /// inherits the client-wide policy: reopening a watch without a start revision resumes at a
    /// later revision and silently skips the changes in between, so only a per-call or
    /// per-operation policy may reopen one.
    /// <para>A <paramref name="paged"/> read (relationship reads, lookups, exports) has no default
    /// total budget, but applies the client default deadline to each page request, from opening it
    /// to receiving its last item, so a server that stalls mid-read fails the read instead of
    /// hanging it. A caller's <paramref name="timeout"/> still bounds the whole read and wins when
    /// earlier.</para>
    /// </summary>
    private Budget Begin(TimeSpan? timeout, CancellationToken token, bool unary = true, TransportOptions? transport = null, RetryPolicy? retry = null, bool watch = false, bool paged = false, [CallerMemberName] string operation = "")
    {
        token.ThrowIfCancellationRequested();
        ObjectDisposedException.ThrowIf(_disposed != 0, this);
        // The most specific policy wins: per call, then per operation, then client-wide. A
        // per-call or per-operation policy also governs stream establishment.
        var specific = retry?.Validated() ?? OperationPolicy(operation);
        if (watch && specific is null) return new(timeout is { } w ? ValidTimeout(w) : null, token, _lifetime.Token, transport, null, false, operation, _endpoint);
        return new(timeout is { } t ? ValidTimeout(t) : unary ? _defaultTimeout : null, token, _lifetime.Token, transport, specific ?? _retryPolicy, specific is not null || _streamRetries, operation, _endpoint)
        { PageTimeout = paged ? _defaultTimeout : null };
    }
    /// <summary>The client's <see cref="ClientOptions.OperationRetryPolicies"/> entry for the calling operation, if any.</summary>
    private RetryPolicy? OperationPolicy([CallerMemberName] string operation = "") => _operationRetry.GetValueOrDefault(operation);
    /// <summary>
    /// Whether a mutation may retry. A per-call <paramref name="policy"/> with retries is an
    /// opt-in that only idempotent mutations may make; any other mutation is rejected before any
    /// request. Without a per-call policy, a client-wide <paramref name="operationPolicy"/>
    /// applies only to the calls that are idempotent, and leaves the others single-attempt.
    /// </summary>
    internal static bool MutationRetry(RetryPolicy? policy, bool idempotent, string operation, RetryPolicy? operationPolicy = null)
    {
        if (policy is null) return operationPolicy is { MaxRetries: > 0 } && idempotent;
        if (policy.MaxRetries == 0) return false;
        return idempotent ? true : throw new InvalidArgumentException($"{operation} cannot be retried: only touch-only writes without preconditions and schema writes are idempotent on replay.");
    }
    private sealed class Budget : IDisposable
    {
        private readonly long _start = Stopwatch.GetTimestamp();
        private readonly TimeSpan? _timeout;
        private readonly CancellationToken _caller;
        private readonly CancellationTokenSource _linked;
        private readonly TransportOptions? _transport;
        private readonly RetryPolicy? _retry;
        private readonly string _operation;
        private readonly string? _endpoint;
        // Timer rounding can cancel before the monotonic elapsed-time check expires.
        // Keep the deadline source separate from caller and client-lifetime cancellation.
        private readonly CancellationTokenSource _deadline = new();
        // The per-page deadline of a paged read, when PageTimeout is set. It is a watchdog on
        // waiting for the server: each wait for the next message runs under its own deadline
        // source (kept apart from the linked token for the timer-rounding reason above) for
        // what is left of the page's allowance, and the clock stops while the caller holds an
        // item, so the caller's processing never counts.
        private CancellationTokenSource? _waitDeadline;
        private CancellationTokenSource? _wait;
        private long _waitStart;
        private TimeSpan _pageWaited;
        public CancellationToken Token => _wait?.Token ?? _linked.Token;
        /// <summary>The operation's token, without the page watchdog.</summary>
        public CancellationToken OperationToken => _linked.Token;
        public bool StreamRetries { get; }
        /// <summary>The per-page deadline of a paged read: the client default, or null.</summary>
        public TimeSpan? PageTimeout { get; init; }
        /// <summary>Starts a new page's allowance of server wait time.</summary>
        public void StartPage() { EndWait(); _pageWaited = TimeSpan.Zero; }
        /// <summary>Starts waiting on the server for the next message. A no-op without <see cref="PageTimeout"/>.</summary>
        public void StartWait()
        {
            EndWait();
            if (PageTimeout is not { } p) return;
            var left = p - _pageWaited;
            _waitStart = Stopwatch.GetTimestamp();
            _waitDeadline = new();
            _wait = CancellationTokenSource.CreateLinkedTokenSource(_linked.Token, _waitDeadline.Token);
            if (left > TimeSpan.Zero) _waitDeadline.CancelAfter(left); else _waitDeadline.Cancel();
        }
        /// <summary>Stops the clock: the wait's time counts against the page, the caller's next does not.</summary>
        public void EndWait()
        {
            if (_waitDeadline is null) return;
            _pageWaited += Stopwatch.GetElapsedTime(_waitStart);
            _wait?.Dispose(); _wait = null;
            _waitDeadline.Dispose(); _waitDeadline = null;
        }
        private bool PageExpired => _waitDeadline is not null && PageTimeout is { } p && (_waitDeadline.IsCancellationRequested || _pageWaited + Stopwatch.GetElapsedTime(_waitStart) >= p);
        private DeadlineExceededException PageDeadlineExceeded(Exception? inner = null)
        {
            var message = $"Page request deadline exceeded: the server did not deliver the page within the client default deadline ({PageTimeout}).";
            return inner is null ? new(message) : new(message, inner);
        }
        public Budget(TimeSpan? timeout, CancellationToken caller, CancellationToken lifetime, TransportOptions? transport, RetryPolicy? retry, bool streamRetries, string operation, string? endpoint)
        {
            _timeout = timeout; _caller = caller; _linked = CancellationTokenSource.CreateLinkedTokenSource(caller, lifetime, _deadline.Token);
            _transport = transport; _retry = retry; StreamRetries = streamRetries; _operation = operation; _endpoint = endpoint;
            if (timeout is { } t) _deadline.CancelAfter(t);
        }
        public DateTime? Deadline
        {
            get
            {
                Check();
                // Only the total budget goes on the wire: the page watchdog stops while the
                // caller holds an item, which an absolute call deadline cannot.
                return _timeout is { } t ? DateTime.UtcNow + (t - Stopwatch.GetElapsedTime(_start)) : null;
            }
        }
        /// <summary>gRPC call options for one attempt: remaining deadline, budget token and caller transport settings.</summary>
        public Grpc.Core.CallOptions CallOptions()
        {
            Metadata? headers = null;
            if (_transport?.Headers is { } supplied)
            {
                // Copy per attempt: auth interceptors append to the headers object they receive.
                headers = new Metadata();
                foreach (var entry in supplied) headers.Add(entry);
            }
            return new(headers: headers, deadline: Deadline, cancellationToken: Token);
        }
        /// <summary>Records response headers and trailers into the caller's sink, if any. The last RPC wins.</summary>
        public void Capture(Func<Task<Metadata>> headers, Func<Metadata> trailers)
        {
            if (_transport?.ResponseMetadata is not { } sink) return;
            try { var task = headers(); if (task.IsCompletedSuccessfully) sink.Headers = task.Result; } catch (Exception) { /* not available for this call */ }
            try { sink.Trailers = trailers(); } catch (Exception) { /* call still in progress or never started */ }
        }
        public void Check()
        {
            _caller.ThrowIfCancellationRequested();
            if (_deadline.IsCancellationRequested || (_timeout is { } t && Stopwatch.GetElapsedTime(_start) >= t)) throw new DeadlineExceededException("Operation total deadline exceeded.");
            if (PageExpired) throw PageDeadlineExceeded();
            Token.ThrowIfCancellationRequested();
        }
        public Exception Map(Exception ex)
        {
            if (_caller.IsCancellationRequested) return new OperationCanceledException("Operation cancelled by caller.", ex, _caller);
            if (_deadline.IsCancellationRequested || (_timeout is { } t && Stopwatch.GetElapsedTime(_start) >= t)) return ex as DeadlineExceededException ?? new DeadlineExceededException("Operation total deadline exceeded.", ex);
            if (PageExpired) return ex as DeadlineExceededException ?? PageDeadlineExceeded(ex);
            return ex is RpcException rpc ? ErrorMapper.ToSpiceDBException(rpc, _endpoint) : ex;
        }
        /// <summary>Whether a read attempt that failed with <paramref name="ex"/> may be retried.</summary>
        public bool ShouldRetry(RpcException ex, int attempt) => _retry is { } p && attempt < p.MaxRetries && p.RetryableCodes.Contains(ex.StatusCode);
        public async Task Backoff(int attempt, RpcException cause)
        {
            Check();
            var policy = _retry!;
            var bound = policy.InitialBackoff * Math.Pow(2, Math.Min(attempt, 30));
            if (policy.MaxBackoff is { } max && bound > max) bound = max;
            var delay = JitteredDelay(bound);
            policy.OnRetry?.Invoke(new RetryEvent(_operation, attempt + 1, ErrorMapper.ToSpiceDBException(cause, _endpoint), delay));
            try { await Task.Delay(delay, Token); }
            catch (OperationCanceledException ex) { throw Map(ex); }
            Check();
        }
        public void Dispose() { EndWait(); _linked.Dispose(); _deadline.Dispose(); }
    }
    private static async Task<T> Unary<T>(Budget budget, Func<Grpc.Core.CallOptions, AsyncUnaryCall<T>> call, bool retry = true)
    {
        for (var attempt = 0; ; attempt++)
        {
            budget.Check();
            AsyncUnaryCall<T>? rpc = null;
            try { rpc = call(budget.CallOptions()); return await rpc.ResponseAsync.WaitAsync(budget.Token); }
            catch (RpcException ex) when (retry && budget.ShouldRetry(ex, attempt)) { budget.Check(); await budget.Backoff(attempt, ex); }
            catch (Exception ex) when (ex is RpcException or OperationCanceledException) { throw budget.Map(ex); }
            finally
            {
                if (rpc is not null) { budget.Capture(() => rpc.ResponseHeadersAsync, rpc.GetTrailers); rpc.Dispose(); }
            }
        }
    }
    /// <summary>
    /// Opens a server stream with establishment retries. In a paged read (a budget with a
    /// <see cref="Budget.PageTimeout"/>) the per-page deadline bounds only waiting on the server:
    /// the clock runs while the client waits for the next message and stops while the caller
    /// holds an item, so the caller's processing never counts. A page's waits share one
    /// allowance; with <paramref name="perMessage"/> (a stream not bounded by a page size) each
    /// wait for a message gets the whole allowance.
    /// </summary>
    private static async IAsyncEnumerable<T> Stream<T>(Budget budget, Func<Grpc.Core.CallOptions, AsyncServerStreamingCall<T>> open, bool perMessage = false)
    {
        bool exposed = false;
        for (int attempt = 0; ; attempt++)
        {
            budget.Check();
            AsyncServerStreamingCall<T>? call = null;
            RpcException? retryCause = null;
            budget.StartPage();
            try { call = open(budget.CallOptions()); }
            catch (RpcException ex) when (budget.StreamRetries && budget.ShouldRetry(ex, attempt)) { retryCause = ex; }
            catch (Exception ex) when (ex is RpcException or OperationCanceledException) { throw budget.Map(ex); }
            if (call is not null)
            {
                try
                {
                    using (budget.OperationToken.Register(call.Dispose))
                    {
                        while (true)
                        {
                            budget.Check();
                            if (perMessage) budget.StartPage();
                            budget.StartWait();
                            bool next;
                            try { next = await call.ResponseStream.MoveNext(budget.Token).WaitAsync(budget.Token); }
                            catch (RpcException ex) when (!exposed && budget.StreamRetries && budget.ShouldRetry(ex, attempt)) { retryCause = ex; break; }
                            catch (Exception ex) when (ex is RpcException or OperationCanceledException) { throw budget.Map(ex); }
                            budget.EndWait();
                            if (!next) yield break;
                            exposed = true;
                            yield return call.ResponseStream.Current;
                        }
                    }
                }
                finally
                {
                    budget.EndWait();
                    budget.Capture(() => call.ResponseHeadersAsync, call.GetTrailers);
                    call.Dispose();
                }
            }
            budget.EndWait();
            if (retryCause is null) yield break;
            await budget.Backoff(attempt, retryCause);
        }
    }

    public async Task<CheckResult> CheckAsync(PermissionCheck request, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
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
        var response = await Unary(budget, o => _permissions.CheckPermissionAsync(wire, o.Headers, o.Deadline, o.CancellationToken));
        return new(Response.CheckPermissionship(response.Permissionship, "CheckPermission"), response.PartialCaveatInfo?.MissingRequiredContext ?? Enumerable.Empty<string>(), new(response.CheckedAt?.Token ?? ""), DebugInformation.FromProto(response.DebugTrace), response.OptionalExpiresAt?.ToDateTimeOffset());
    }
    public async Task<IReadOnlyList<CheckResult>> CheckBulkAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
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
            var response = await Unary(budget, o => _permissions.CheckBulkPermissionsAsync(wire, o.Headers, o.Deadline, o.CancellationToken));
            if (response.Pairs.Count != wire.Items.Count) throw Response.Malformed("CheckBulkPermissions", $"returned {response.Pairs.Count} pair(s) for {wire.Items.Count} request item(s)");
            foreach (var pair in response.Pairs)
            {
                if (!wire.Items[results.Count - offset].Equals(pair.Request))
                { var malformed = Response.Malformed($"check item {results.Count}", "bulk response echoed request does not match its input"); malformed.InputIndex = results.Count; throw malformed; }
                if (pair.Error is { } error)
                {
                    var exception = ErrorMapper.ToSpiceDBException(new RpcException(new Grpc.Core.Status((StatusCode)error.Code, error.Message), new Metadata { { "grpc-status-details-bin", error.ToByteArray() } }));
                    exception.InputIndex = results.Count;
                    if (options?.ItemErrors != BulkItemErrors.Returned) throw exception;
                    results.Add(new CheckResult(exception, new(response.CheckedAt?.Token ?? "")));
                    continue;
                }
                if (pair.Item is null) { var malformed = Response.Malformed($"check item {results.Count}", "bulk response is missing its result"); malformed.InputIndex = results.Count; throw malformed; }
                if (pair.Item.Permissionship == CheckPermissionResponse.Types.Permissionship.Unspecified) { var malformed = Response.Malformed($"check item {results.Count}", "bulk response permissionship is unspecified"); malformed.InputIndex = results.Count; throw malformed; }
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
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        if (transaction is null) throw new InvalidArgumentException("Transaction is required.");
        var retry = MutationRetry(options?.RetryPolicy, transaction.IsIdempotent, "Write", OperationPolicy());
        // An empty transaction is sent as is: SpiceDB accepts it, evaluates any preconditions
        // and returns the current revision.
        var request = new WriteRelationshipsRequest { OptionalTransactionMetadata = Native.Struct(options?.Metadata) };
        request.Updates.AddRange(transaction.V1Updates); request.OptionalPreconditions.AddRange(transaction.V1Preconditions);
        var response = await Unary(budget, o => _permissions.WriteRelationshipsAsync(request, o.Headers, o.Deadline, o.CancellationToken), retry);
        return new(new ZedToken(response.WrittenAt?.Token ?? ""));
    }
    private static int Positive(int value, string field) => value > 0 ? value : throw new InvalidArgumentException($"{field} must be positive.");
    /// <summary>
    /// Issues one bounded delete. In the default <see cref="PartialDeletion.Allowed"/> mode
    /// <paramref name="limit"/> is required and positive; under
    /// <see cref="PartialDeletion.Disallowed"/> the delete is all-or-nothing and a null limit
    /// uses the server's default.
    /// </summary>
    public async Task<DeleteResult> DeleteRelationshipsAsync(Filter filter, int? limit, DeleteOptions? options = null, CancellationToken cancellationToken = default)
    {
        bool inFlight = false;
        try
        {
            using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
            MutationRetry(options?.RetryPolicy, false, "DeleteRelationships");
            var partial = options?.Partial != PartialDeletion.Disallowed;
            if (limit is { } l) Positive(l, nameof(limit));
            else if (partial) throw new InvalidArgumentException("limit is required unless Partial is PartialDeletion.Disallowed.");
            Native.Required(filter, nameof(filter)).ToProto();
            foreach (var precondition in (options?.MustMatch ?? []).Concat(options?.MustNotMatch ?? [])) precondition.ToProto();
            budget.Check();
            inFlight = true;
            return await DeletePage(budget, filter, limit, options?.ResumeCursor, options?.MustMatch, options?.MustNotMatch, options?.Metadata, partial);
        }
        catch (Exception ex)
        {
            var progress = new DeleteProgress(0, null, null, 0, inFlight);
            if (ex is SpiceDBException typed) typed.DeleteProgress ??= progress;
            if (ex is OperationCanceledException) ex.Data["SpiceDB.DeleteProgress"] = progress;
            throw;
        }
    }
    private async Task<DeleteResult> DeletePage(Budget budget, Filter filter, int? limit, string? cursor, IReadOnlyList<Filter>? matches, IReadOnlyList<Filter>? notMatches, IReadOnlyDictionary<string, object?>? metadata, bool allowPartial = true)
    {
        if (filter is null) throw new InvalidArgumentException("Filter is required.");
        var request = new DeleteRelationshipsRequest
        {
            RelationshipFilter = Native.Required(filter, nameof(filter)).ToProto(),
            OptionalLimit = (uint)(limit ?? 0),
            OptionalAllowPartialDeletions = allowPartial,
            OptionalTransactionMetadata = Native.Struct(metadata),
            OptionalCursor = string.IsNullOrEmpty(cursor) ? null : new() { Token = cursor }
        };
        foreach (var item in matches ?? []) request.OptionalPreconditions.Add(Transaction.BuildPrecondition(Authzed.Api.V1.Precondition.Types.Operation.MustMatch, item));
        foreach (var item in notMatches ?? []) request.OptionalPreconditions.Add(Transaction.BuildPrecondition(Authzed.Api.V1.Precondition.Types.Operation.MustNotMatch, item));
        var response = await Unary(budget, o => _permissions.DeleteRelationshipsAsync(request, o.Headers, o.Deadline, o.CancellationToken), false);
        if (response.DeletionProgress is not (DeleteRelationshipsResponse.Types.DeletionProgress.Complete or DeleteRelationshipsResponse.Types.DeletionProgress.Partial))
            throw WithProgress(SpiceDBException.InternalError("Invalid server deletion progress."), new(response.RelationshipsDeletedCount, new ZedToken(response.DeletedAt?.Token ?? ""), response.AfterResultCursor?.Token, 1, false));
        return new(new(response.DeletedAt?.Token ?? ""), response.RelationshipsDeletedCount, response.DeletionProgress == DeleteRelationshipsResponse.Types.DeletionProgress.Complete, response.AfterResultCursor?.Token);
    }
    /// <summary>Deletes all matches in separate transactional batches. The overall operation is non-atomic: completed batches remain committed if a later batch fails or is canceled.</summary>
    public async Task<DeleteResult> DeleteAllRelationshipsNonAtomicAsync(Filter filter, DeleteAllOptions? options = null, CancellationToken cancellationToken = default)
    {
        int pages = 0;
        ulong count = 0; ZedToken? token = null; string? cursor = options?.ResumeCursor; bool inFlight = false;
        try
        {
            using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
            MutationRetry(options?.RetryPolicy, false, "DeleteAllRelationshipsNonAtomic");
            int size = Positive(options?.PageSize ?? 1000, "PageSize");
            Native.Required(filter, nameof(filter)).ToProto();
            foreach (var precondition in (options?.MustMatch ?? []).Concat(options?.MustNotMatch ?? [])) precondition.ToProto();
            while (true)
            {
                budget.Check(); inFlight = true;
                var page = await DeletePage(budget, filter, size, cursor, options?.MustMatch, options?.MustNotMatch, options?.Metadata);
                inFlight = false; pages++; count += page.DeletedCount; token = page.Revision; cursor = page.ResumeCursor;
                options?.Progress?.Report(page with { DeletedCount = count });
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

    // Paged reads: every page repeats the caller's consistency and the one fixed page size.
    // SpiceDB hashes the request arguments (consistency and limit included) into each cursor
    // and rejects a cursor sent with different ones; the cursor itself pins later pages to the
    // first page's revision. A total Limit is therefore enforced here, never by shrinking the
    // last page's limit.
    private static Cursor? StartCursor(string? cursor) => string.IsNullOrEmpty(cursor) ? null : new() { Token = cursor };
    // The OptionalLimit every request of a paged read sends: the fixed page size, or in
    // single-request mode the total limit alone (0, unbounded, without one).
    private static uint PageRequestLimit(bool singleRequest, int pageSize, int? limit) => singleRequest ? (uint)(limit ?? 0) : (uint)pageSize;
    // After a full page, the next request must carry a new cursor. A missing, empty or
    // unchanged cursor would re-request the same page forever, so it fails as an internal error.
    private static string Advanced(string? sent, Cursor? next, string operation)
    {
        var token = next?.Token;
        if (string.IsNullOrEmpty(token) || token == sent)
        {
            var message = $"{operation} cursor did not advance.";
            throw SpiceDBException.InternalError(message);
        }
        return token;
    }

    public async IAsyncEnumerable<ReadRelationship> ReadRelationshipsAsync(Consistency consistency, Filter filter, ReadOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false, transport: options?.Transport, retry: options?.RetryPolicy, paged: true);
        int size = Positive(options?.PageSize ?? 512, "PageSize"), remaining = Positive(options?.Limit ?? int.MaxValue, "Limit");
        var single = options?.Paging == Paging.SingleRequest;
        var request = new ReadRelationshipsRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), RelationshipFilter = Native.Required(filter, nameof(filter)).ToProto(), OptionalLimit = PageRequestLimit(single, size, options?.Limit), OptionalCursor = StartCursor(options?.ResumeCursor) };
        var sent = options?.ResumeCursor;
        while (true)
        {
            int count = 0;
            await foreach (var response in Stream(budget, o => _permissions.ReadRelationships(request, o.Headers, o.Deadline, o.CancellationToken), perMessage: single))
            {
                request.OptionalCursor = response.AfterResultCursor; count++; remaining--;
                yield return new(Response.Relationship(response.Relationship, "ReadRelationships"), response.AfterResultCursor?.Token, new(response.ReadAt?.Token ?? ""));
                if (remaining == 0) yield break;
            }
            if (single || count < size) yield break;
            sent = Advanced(sent, request.OptionalCursor, "Read");
        }
    }
    public async IAsyncEnumerable<LookupResource> LookupResourcesAsync(Consistency consistency, string resourceType, string permission, SubjectRef subject, LookupOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false, transport: options?.Transport, retry: options?.RetryPolicy, paged: true);
        if (options?.SubjectRelation is not null) throw new InvalidArgumentException("LookupResources uses the supplied SubjectRef relation.");
        int size = Positive(options?.PageSize ?? 512, "PageSize"), remaining = Positive(options?.Limit ?? int.MaxValue, "Limit");
        var single = options?.Paging == Paging.SingleRequest;
        var request = new LookupResourcesRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), ResourceObjectType = Native.Required(resourceType, nameof(resourceType)), Permission = Native.Required(permission, nameof(permission)), Subject = Native.Required(subject, nameof(subject)).ToProto(), Context = Native.Struct(options?.Context), WithDebug = options?.Debug == LookupDebug.Enabled, OptionalLimit = PageRequestLimit(single, size, options?.Limit), OptionalCursor = StartCursor(options?.ResumeCursor) };
        var sent = options?.ResumeCursor;
        while (true)
        {
            int count = 0;
            await foreach (var response in Stream(budget, o => _permissions.LookupResources(request, o.Headers, o.Deadline, o.CancellationToken), perMessage: single))
            {
                request.OptionalCursor = response.AfterResultCursor; count++;
                var permissionship = Response.LookupPermissionship(response.Permissionship, "LookupResources");
                if (options?.PermissionshipFilter == LookupPermissionshipFilter.HasPermissionOnly && response.Permissionship != LookupPermissionship.HasPermission) continue;
                remaining--;
                yield return new() { ResourceId = Response.Id(response.ResourceObjectId, "LookupResources", "resource object id"), Permissionship = permissionship, PartialCaveat = ToPartialCaveatInfo(response.PartialCaveatInfo), LookedUpAt = new(response.LookedUpAt?.Token ?? ""), AfterResultCursor = response.AfterResultCursor?.Token };
                if (remaining == 0) yield break;
            }
            if (single || count < size) yield break;
            sent = Advanced(sent, request.OptionalCursor, "Lookup");
        }
    }
    public async IAsyncEnumerable<LookupSubject> LookupSubjectsAsync(Consistency consistency, ObjectRef resource, string permission, string subjectType, LookupOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false, transport: options?.Transport, retry: options?.RetryPolicy, paged: true);
        if (options is not null && options.PermissionshipFilter != LookupPermissionshipFilter.All) throw new InvalidArgumentException("LookupSubjects does not support PermissionshipFilter.");
        if (options?.Debug == LookupDebug.Enabled || options?.PageSize is not null || options?.Paging is Paging.SingleRequest) throw new InvalidArgumentException("LookupSubjects does not support Debug, PageSize or Paging; upstream concrete pagination is unimplemented.");
        int remaining = Positive(options?.Limit ?? int.MaxValue, "Limit");
        var request = new LookupSubjectsRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), Resource = Native.Required(resource, nameof(resource)).ToProto(), Permission = Native.Required(permission, nameof(permission)), SubjectObjectType = Native.Required(subjectType, nameof(subjectType)), OptionalSubjectRelation = options?.SubjectRelation ?? "", Context = Native.Struct(options?.Context) };
        await foreach (var response in Stream(budget, o => _permissions.LookupSubjects(request, o.Headers, o.Deadline, o.CancellationToken), perMessage: true))
        {
            yield return ValidLookupSubject(response);
            if (--remaining == 0) yield break;
        }
    }
    public async Task<ulong> ImportRelationshipsAsync(IAsyncEnumerable<Relationship> relationships, ImportOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false, transport: options?.Transport, retry: options?.RetryPolicy);
        int size = Positive(options?.BatchSize ?? 1000, "BatchSize");
        MutationRetry(options?.RetryPolicy, false, "ImportRelationships");
        try
        {
            // When the server aborts the stream (for example ALREADY_EXISTS for a duplicate), a
            // pending write fails or the producer is interrupted; either way the server's final
            // status is read from ResponseAsync below rather than reporting the failed send.
            var importOptions = budget.CallOptions();
            using var call = _permissions.ImportBulkRelationships(importOptions.Headers, importOptions.Deadline, importOptions.CancellationToken);
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
            catch (RpcException) when (!budget.Token.IsCancellationRequested)
            {
                // A write that failed because the server already ended the call surfaces a
                // transport error; the server's own status is the call's final status.
                await responseFinished;
                if (call.ResponseAsync.IsFaulted) return (await call.ResponseAsync).NumLoaded;
                throw;
            }
            finally
            {
                budget.Capture(() => call.ResponseHeadersAsync, call.GetTrailers);
                call.Dispose();
                await responseFinished;
            }
        }
        catch (Exception ex) when (ex is RpcException or OperationCanceledException) { throw budget.Map(ex); }
    }
    public async IAsyncEnumerable<ExportedRelationship> ExportRelationshipsAsync(Consistency consistency, Filter filter, ExportOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false, transport: options?.Transport, retry: options?.RetryPolicy, paged: true);
        int size = Positive(options?.PageSize ?? 512, "PageSize"), remaining = Positive(options?.Limit ?? int.MaxValue, "Limit");
        var request = new ExportBulkRelationshipsRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), OptionalRelationshipFilter = Native.Required(filter, nameof(filter)).ToProto(), OptionalLimit = (uint)size, OptionalCursor = StartCursor(options?.ResumeCursor) };
        var sent = options?.ResumeCursor;
        while (true)
        {
            int count = 0;
            await foreach (var response in Stream(budget, o => _permissions.ExportBulkRelationships(request, o.Headers, o.Deadline, o.CancellationToken)))
            {
                request.OptionalCursor = response.AfterResultCursor;
                foreach (var relationship in response.Relationships)
                {
                    count++; remaining--; yield return new(Response.Relationship(relationship, "ExportBulkRelationships"), response.AfterResultCursor?.Token);
                    if (remaining == 0) yield break;
                }
            }
            if (count < size) yield break;
            sent = Advanced(sent, request.OptionalCursor, "Export");
        }
    }
    /// <summary>
    /// Watches relationship (and optionally schema and checkpoint) changes from
    /// <paramref name="startRevision"/>, or from now when it is null. The client-wide retry
    /// policy never reopens the watch, since a watch reopened without a start revision begins at
    /// a later revision and silently skips the changes in between; a failure surfaces to the
    /// caller, who resumes from the last event's revision. Only a per-call
    /// <see cref="WatchOptions.RetryPolicy"/> or a <see cref="ClientOptions.OperationRetryPolicies"/>
    /// entry for <see cref="SpiceDBOperation.Updates"/> reopens it, and only before its first event.
    /// </summary>
    public async IAsyncEnumerable<WatchEvent> UpdatesAsync(IReadOnlyList<string> objectTypes, ZedToken? startRevision, WatchOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false, transport: options?.Transport, retry: options?.RetryPolicy, watch: true);
        var request = new WatchRequest { OptionalStartCursor = startRevision is { } token ? new() { Token = token.Value } : null };
        request.OptionalObjectTypes.AddRange(Native.Required(objectTypes, nameof(objectTypes)).ToArray());
        if (request.OptionalObjectTypes.Count > 0 && (options?.RelationshipFilters.Count ?? 0) > 0)
            throw new InvalidArgumentException("Object types and relationship filters are mutually exclusive.");
        request.OptionalRelationshipFilters.AddRange((options?.RelationshipFilters ?? []).Select(x => x.ToProto()));
        var content = options?.Content ?? WatchContent.Relationships;
        if (content.HasFlag(WatchContent.Relationships)) request.OptionalUpdateKinds.Add(WatchKind.IncludeRelationshipUpdates);
        if (content.HasFlag(WatchContent.Checkpoints)) request.OptionalUpdateKinds.Add(WatchKind.IncludeCheckpoints);
        if (content.HasFlag(WatchContent.SchemaUpdates)) request.OptionalUpdateKinds.Add(WatchKind.IncludeSchemaUpdates);
        await foreach (var response in Stream(budget, o => _watch.Watch(request, o.Headers, o.Deadline, o.CancellationToken)))
            yield return new(response.Updates.Select(u => Response.Update(u, "Watch")).ToArray(), new(response.ChangesThrough?.Token ?? ""), response.IsCheckpoint, response.SchemaUpdated, Native.FromStruct(response.OptionalTransactionMetadata), response.FullRevisionMetadata.Select(x => Native.FromStruct(x)!));
    }
    public async Task<SchemaResult> ReadSchemaAsync(
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        var resp = await Unary(budget, o => _schema.ReadSchemaAsync(new ReadSchemaRequest(), o.Headers, o.Deadline, o.CancellationToken));

        return new(resp.SchemaText, new(resp.ReadAt?.Token ?? ""));
    }

    public async Task<ZedToken> WriteSchemaAsync(
        string schema,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        if (string.IsNullOrEmpty(schema))
            throw new InvalidArgumentException("Schema must not be empty.");
        // Writing the same schema again is idempotent, so a caller may opt this call into retries.
        var retry = MutationRetry(options?.RetryPolicy, true, "WriteSchema", OperationPolicy());

        var resp = await Unary(budget, o => _schema.WriteSchemaAsync(new WriteSchemaRequest { Schema = schema }, o.Headers, o.Deadline, o.CancellationToken), retry);

        return new(resp.WrittenAt?.Token ?? "");
    }

    public async Task<ReflectSchemaResult> ReflectSchemaAsync(
        Consistency consistency,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate(reflect: true);
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, o => _schema.ReflectSchemaAsync(new ReflectSchemaRequest { Consistency = Native.Required(consistency, nameof(consistency)).ToProto(), OptionalFilters = { (options?.SchemaFilters ?? []).Select(x => x.ToProto()) } }, o.Headers, o.Deadline, o.CancellationToken));

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
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate(computable: true);
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, o => _schema.ComputablePermissionsAsync(new ComputablePermissionsRequest
        {
            Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
            DefinitionName = definitionName,
            RelationName = relationName,
            OptionalDefinitionNameFilter = options?.DefinitionNameFilter ?? "",
        }, o.Headers, o.Deadline, o.CancellationToken));

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
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, o => _schema.DependentRelationsAsync(new DependentRelationsRequest
        {
            Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
            DefinitionName = definitionName,
            PermissionName = permissionName,
        }, o.Headers, o.Deadline, o.CancellationToken));

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
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, o => _schema.DiffSchemaAsync(new DiffSchemaRequest
        {
            Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
            ComparisonSchema = comparisonSchema,
        }, o.Headers, o.Deadline, o.CancellationToken));

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
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        var resp = await Unary(budget, o => _permissions.ExpandPermissionTreeAsync(new ExpandPermissionTreeRequest
        {
            Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
            Resource = Native.Required(resource, nameof(resource)).ToProto(),
            Permission = permission,
        }, o.Headers, o.Deadline, o.CancellationToken));

        return new ExpandResult
        {
            Tree = ToPermissionTree(Response.Tree(resp.TreeRoot, "ExpandPermissionTree")),
            Revision = new(resp.ExpandedAt?.Token ?? ""),
        };
    }

    public async Task ExperimentalRegisterRelationshipCounterAsync(
        string name,
        Filter filter,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        if (string.IsNullOrEmpty(name))
            throw new InvalidArgumentException("Name must not be empty.");
        Native.Required(filter, nameof(filter));
        MutationRetry(options?.RetryPolicy, false, "ExperimentalRegisterRelationshipCounter");

        await Unary(budget, o => _experimental.ExperimentalRegisterRelationshipCounterAsync(new ExperimentalRegisterRelationshipCounterRequest
        {
            Name = name,
            RelationshipFilter = Native.Required(filter, nameof(filter)).ToProto(),
        }, o.Headers, o.Deadline, o.CancellationToken), false);
    }

    public async Task<CountResult> ExperimentalCountRelationshipsAsync(
        string name,
        CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        if (string.IsNullOrEmpty(name))
            throw new InvalidArgumentException("Name must not be empty.");

        var resp = await Unary(budget, o => _experimental.ExperimentalCountRelationshipsAsync(new ExperimentalCountRelationshipsRequest { Name = name }, o.Headers, o.Deadline, o.CancellationToken));

        if (resp.CounterStillCalculating)
            return new CountResult { StillCalculating = true };

        var cv = resp.ReadCounterValue ?? throw SpiceDBException.InternalError("Counter response does not contain a count or calculating state.");
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
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        if (string.IsNullOrEmpty(name))
            throw new InvalidArgumentException("Name must not be empty.");
        MutationRetry(options?.RetryPolicy, false, "ExperimentalUnregisterRelationshipCounter");

        await Unary(budget, o => _experimental.ExperimentalUnregisterRelationshipCounterAsync(new ExperimentalUnregisterRelationshipCounterRequest { Name = name }, o.Headers, o.Deadline, o.CancellationToken), false);
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
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        if (consistency is null) throw new InvalidArgumentException("Consistency is required.");

        if (options is not null && (options.Debug == LookupDebug.Enabled || options.Context is not null || options.PageSize is not null || options.Limit is not null || options.SubjectRelation is not null || options.Paging != Paging.Automatic))
            throw new InvalidArgumentException("Roaring lookup supports Timeout only; context, debug, limits and subject relation options are unsupported. Put a subject relation on SubjectRef.");
        var resp = await Unary(budget, o => _materialize.ExperimentalRoaringLookupResourcesAsync(new MaterializeV0.ExperimentalRoaringLookupResourcesRequest
        {
            Consistency = Native.Required(consistency, nameof(consistency)).ToProto(),
            ResourceObjectType = resourceType,
            Permission = permission,
            Subject = Native.Required(subject, nameof(subject)).ToProto(),
        }, o.Headers, o.Deadline, o.CancellationToken));

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
            SubjectId = v.SubjectObjectId,
            Permissionship = ToPermissionship(v.Permissionship),
            PartialCaveat = ToPartialCaveatInfo(v.PartialCaveatInfo),
        };
    }

    internal static LookupSubject ToLookupSubject(LookupSubjectsResponse resp)
    {
        var subject = ToResolvedSubject(resp.Subject);
        if (string.IsNullOrEmpty(subject.SubjectId))
        {
#pragma warning disable CS0612 // deprecated proto fields — explicit fallback for older servers
            subject = new ResolvedSubject
            {
                SubjectId = resp.SubjectObjectId,
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
                excluded = resp.ExcludedSubjectIds.Select(id => new ResolvedSubject { SubjectId = id }).ToList();
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

    internal static RelationshipUpdate UpdateFromProto(Authzed.Api.V1.RelationshipUpdate pu)
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
        return new RelationshipUpdate(UpdateOperationFromProto(pu.Operation), Relationship.FromProto(pu.Relationship));
    }
    internal static UpdateOperation UpdateOperationFromProto(Authzed.Api.V1.RelationshipUpdate.Types.Operation operation) => operation switch
    {
        Authzed.Api.V1.RelationshipUpdate.Types.Operation.Create => UpdateOperation.Create,
        Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch => UpdateOperation.Touch,
        Authzed.Api.V1.RelationshipUpdate.Types.Operation.Delete => UpdateOperation.Delete,
        _ => UpdateOperation.Unspecified,
    };
    /// <summary>
    /// <see cref="ToLookupSubject"/> for the client's own call path: a response with no subject
    /// id (in either the current or the deprecated field), or an excluded subject without one,
    /// is malformed rather than an empty-id subject, and so is a subject whose permissionship is
    /// unspecified.
    /// </summary>
    internal static LookupSubject ValidLookupSubject(LookupSubjectsResponse response)
    {
        var mapped = ToLookupSubject(response);
        Response.Id(mapped.Subject.SubjectId, "LookupSubjects", "subject object id");
#pragma warning disable CS0612 // deprecated proto field — the same fallback ToLookupSubject takes
        var permissionship = string.IsNullOrEmpty(response.Subject?.SubjectObjectId) ? response.Permissionship : response.Subject.Permissionship;
#pragma warning restore CS0612
        Response.LookupPermissionship(permissionship, "LookupSubjects");
        foreach (var excluded in mapped.ExcludedSubjects) Response.Id(excluded.SubjectId, "LookupSubjects", "excluded subject object id");
        return mapped;
    }
    private static SpiceDBException WithProgress(SpiceDBException error, DeleteProgress progress) { error.DeleteProgress = progress; return error; }

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
