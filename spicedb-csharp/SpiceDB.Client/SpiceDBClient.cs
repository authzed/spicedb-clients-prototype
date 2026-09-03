// SpiceDBClient is the idiomatic C# client for SpiceDB.

using System.Runtime.CompilerServices;
using Authzed.Api.SpiceDB.Proto;
using Authzed.Api.V1;
using Google.Protobuf.WellKnownTypes;
using Google.Rpc;
using Grpc.Core;
using Grpc.Net.Client;

// Exposes internal helpers (e.g. the proto -> native PermissionTree mapper)
// to the test assembly without making them part of the public API surface.
[assembly: InternalsVisibleTo("SpiceDB.Client.Tests")]

namespace SpiceDB.Client;

/// <summary>
/// The idiomatic SpiceDB client. Use <see cref="CreatePlaintext"/> or
/// <see cref="CreateSystemTls"/> to create one. Implements
/// <see cref="IAsyncDisposable"/> — a channel this client created is disposed
/// when the client is, while a channel handed to <see cref="CreateFromChannel"/>
/// is left open for its owner (see <see cref="DisposeAsync"/>).
/// </summary>
public sealed class SpiceDBClient : IAsyncDisposable
{
    private const int DefaultReadPageSize = 512;
    private const int DefaultDeletePageSize = 1_000;
    private const int DefaultLookupPageSize = 512;
    private const int DefaultImportBatchSize = 1_000;
    private const int DefaultExportPageSize = 512;
    /// <summary>
    /// How many items go into one CheckBulkPermissions request.
    /// <para>
    /// SpiceDB rejects a request carrying more items than
    /// <c>maxBulkCheckCount</c> — 10,000, a hard-coded const in
    /// <c>internal/services/v1/bulkcheck.go</c> with no flag to raise or
    /// lower it — with <c>ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST</c>.
    /// Nothing in the proto enforces this:
    /// <c>CheckBulkPermissionsRequest.items</c> carries only a per-item
    /// <c>required</c> rule, not a collection-size rule, so the limit lives
    /// solely in server code and a client that forwards the caller's array
    /// unchanged fails on large inputs. 1,000 leaves ten times' headroom and
    /// matches <see cref="DefaultImportBatchSize"/> and the other clients'
    /// check batch size.
    /// </para>
    /// </summary>
    private const int DefaultCheckBatchSize = 1_000;
    private const int MaxRetryAttempts = 3;
    private static readonly TimeSpan InitialBackoff = TimeSpan.FromMilliseconds(100);

    /// <summary>
    /// Applied to every unary call that does not pass its own <c>timeout</c>.
    /// <para>
    /// Mirrors <c>authzed-node</c>'s <c>DEFAULT_DEADLINE_MS = 30_000</c> (its
    /// comment cites <c>grpc/grpc-node#541</c>, a known gRPC failure mode
    /// where a channel that accepts a connection but never answers produces
    /// no error at all). Without a finite default, a wedged SpiceDB hangs
    /// every caller that didn't opt in to a timeout — in practice, most
    /// callers — forever: the connection looks fine at the transport level,
    /// so nothing ever times out and nothing is ever produced to retry. See
    /// root DESIGN.md, "RULE: A unary call must have a deadline".
    /// </para>
    /// <para>
    /// Deliberately NOT applied to streaming calls (<see cref="ReadRelationshipsAsync"/>,
    /// <see cref="LookupResourcesAsync"/>, <see cref="LookupSubjectsAsync"/>,
    /// <see cref="UpdatesAsync"/>, <see cref="ExportRelationshipsAsync"/>) —
    /// those are long-lived by design, and applying this default to them
    /// would make the stream itself the outage (see DESIGN.md, "Streaming
    /// calls MUST NOT inherit the unary default").
    /// </para>
    /// </summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(30);

    /// <summary>
    /// Full-jitter backoff delay: <c>uniform(0, cap)</c> rather than the
    /// fixed <paramref name="cap"/>. Without jitter, every client in a
    /// fleet retries on the same schedule after a server restart, turning
    /// the recovery into a thundering herd; sampling uniformly under the
    /// cap spreads retries out instead.
    /// </summary>
    private static TimeSpan JitteredDelay(TimeSpan cap) =>
        TimeSpan.FromMilliseconds(Random.Shared.NextDouble() * cap.TotalMilliseconds);

    /// <summary>
    /// Resolves a per-call <paramref name="timeout"/> override against
    /// <see cref="_defaultTimeout"/>. <c>null</c> means "use the client
    /// default" — there is deliberately no way to make an unbounded unary
    /// call. See root DESIGN.md, "RULE: A unary call must have a deadline".
    /// </summary>
    private TimeSpan EffectiveTimeout(TimeSpan? timeout) => timeout ?? _defaultTimeout;

    /// <summary>
    /// Computes an absolute UTC deadline from a per-call <paramref name="timeout"/>
    /// override (or the client default). Call this fresh at each individual
    /// RPC attempt — including inside a retry loop's lambda — so a retried
    /// call gets a full new window per attempt rather than a shrinking one.
    /// </summary>
    private DateTime EffectiveDeadline(TimeSpan? timeout) => DateTime.UtcNow + EffectiveTimeout(timeout);

    /// <summary>
    /// As <see cref="EffectiveDeadline"/>, but for client-streaming calls
    /// (currently only <see cref="ImportRelationshipsAsync"/>) that must NOT
    /// fall back to <see cref="_defaultTimeout"/> — see root DESIGN.md,
    /// "RULE: A unary call must have a deadline", clause 3 (client-streaming
    /// RPCs are excluded from the unary default because their duration
    /// scales with the caller's dataset, not server latency). <c>null</c>
    /// in, <c>null</c> out: no client default is ever substituted here.
    /// </summary>
    private static DateTime? DeadlineOrNull(TimeSpan? timeout) =>
        timeout.HasValue ? DateTime.UtcNow + timeout.Value : null;

    private readonly SpiceDBProtoClient? _protoClient;
    private readonly PermissionsService.PermissionsServiceClient _permissions;
    private readonly SchemaService.SchemaServiceClient _schema;
    private readonly WatchService.WatchServiceClient _watch;
    private readonly ExperimentalService.ExperimentalServiceClient _experimental;
    private readonly TimeSpan _defaultTimeout;

    private SpiceDBClient(SpiceDBProtoClient protoClient, TimeSpan defaultTimeout)
    {
        _protoClient = protoClient;
        _permissions = protoClient.Permissions;
        _schema = protoClient.Schema;
        _watch = protoClient.Watch;
        _experimental = protoClient.Experimental;
        _defaultTimeout = defaultTimeout;
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
        ExperimentalService.ExperimentalServiceClient experimental,
        TimeSpan? defaultTimeout = null)
    {
        _protoClient = null;
        _permissions = permissions;
        _schema = schema;
        _watch = watch;
        _experimental = experimental;
        _defaultTimeout = defaultTimeout ?? DefaultTimeout;
    }

    /// <summary>
    /// Creates a client with a plaintext (insecure) connection. Use this for
    /// testing only — the lack of TLS is made obvious by the name.
    /// <paramref name="defaultTimeout"/>, if supplied, overrides the default
    /// applied to every unary call that doesn't pass its own <c>timeout</c>
    /// (see <see cref="DefaultTimeout"/>).
    /// <para>
    /// By itself, this only works against a loopback <paramref name="endpoint"/>
    /// (localhost, 127.0.0.0/8, or ::1) — the local-development
    /// case that is the entire reason a plaintext connection exists. For any other
    /// endpoint, pass <paramref name="allowInsecureRemoteCredentials"/>: true,
    /// on purpose, if you genuinely mean to send a bearer token in cleartext to a
    /// remote host. See root DESIGN.md, "RULE: Credentials over insecure transport
    /// require an explicit opt-in".
    /// </para>
    /// </summary>
    /// <exception cref="ArgumentException">Thrown when endpoint or presharedKey is empty.</exception>
    /// <exception cref="InvalidOperationException">
    /// Thrown when <paramref name="endpoint"/> is not loopback and
    /// <paramref name="allowInsecureRemoteCredentials"/> is false — before any
    /// channel, credential, or connection is created.
    /// </exception>
    public static SpiceDBClient CreatePlaintext(
        string endpoint, string presharedKey, TimeSpan? defaultTimeout = null,
        bool allowInsecureRemoteCredentials = false)
    {
        ValidateArgs(endpoint, presharedKey);
        SpiceDBProtoClient protoClient;
        try
        {
            protoClient = new SpiceDBProtoClient(
                endpoint, presharedKey, insecure: true, allowInsecureRemoteCredentials: allowInsecureRemoteCredentials);
        }
        catch (InsecureRemoteHostException ex)
        {
            // The insecure-remote-host refusal is a caller argument this client rejects
            // before any connection exists, so it surfaces as InvalidArgumentException --
            // the same type a filter the wire cannot express uses. The proto tier's own
            // exception type is an implementation detail a caller of this class should
            // never have to know. See root DESIGN.md, "RULE: Credentials over insecure
            // transport require an explicit opt-in", clause 4.
            throw new InvalidArgumentException(ex.Message, ex);
        }

        return new SpiceDBClient(protoClient, defaultTimeout ?? DefaultTimeout);
    }

    /// <summary>
    /// Creates a client using the system's TLS certificate pool. Use this
    /// for production connections. <paramref name="defaultTimeout"/>, if
    /// supplied, overrides the default applied to every unary call that
    /// doesn't pass its own <c>timeout</c> (see <see cref="DefaultTimeout"/>).
    /// </summary>
    /// <exception cref="ArgumentException">Thrown when endpoint or presharedKey is empty.</exception>
    public static SpiceDBClient CreateSystemTls(
        string endpoint, string presharedKey, TimeSpan? defaultTimeout = null)
    {
        ValidateArgs(endpoint, presharedKey);
        var protoClient = new SpiceDBProtoClient(endpoint, presharedKey, insecure: false);
        return new SpiceDBClient(protoClient, defaultTimeout ?? DefaultTimeout);
    }

    /// <summary>
    /// Creates a client from an existing <see cref="GrpcChannel"/>.
    /// This is the escape hatch for advanced configuration.
    /// <paramref name="defaultTimeout"/>, if supplied, overrides the default
    /// applied to every unary call that doesn't pass its own <c>timeout</c>
    /// (see <see cref="DefaultTimeout"/>).
    /// <para>
    /// <b>Security note:</b> unlike <see cref="CreatePlaintext"/>, this overload
    /// performs no loopback check (root DESIGN.md, "RULE: Credentials over
    /// insecure transport require an explicit opt-in") -- <paramref name="channel"/>
    /// already exists, fully configured, by the time this runs, so there is no
    /// endpoint string or insecure flag left to guard. The bearer token is attached
    /// unconditionally regardless of what transport security <paramref
    /// name="channel"/> actually has. Only pass a channel you built yourself and
    /// know the transport security of.
    /// </para>
    /// <para>
    /// <b>Ownership:</b> <paramref name="channel"/> stays yours. Disposing the
    /// returned client does NOT dispose it, so a DI-registered singleton
    /// <see cref="GrpcChannel"/> survives any number of scoped clients built on
    /// it, and you dispose it yourself when the application shuts down. Only a
    /// channel this library created — via <see cref="CreatePlaintext"/> or
    /// <see cref="CreateSystemTls"/> — is torn down by <see cref="DisposeAsync"/>.
    /// </para>
    /// </summary>
    public static SpiceDBClient CreateFromChannel(
        GrpcChannel channel, string presharedKey, TimeSpan? defaultTimeout = null)
    {
        ArgumentNullException.ThrowIfNull(channel);
        if (string.IsNullOrEmpty(presharedKey))
            throw new ArgumentException("Preshared key must not be empty.", nameof(presharedKey));
        // Create a proto client wrapping the provided channel by using
        // a dummy endpoint — the channel is already configured.
        var protoClient = new SpiceDBProtoClient(channel, presharedKey);
        return new SpiceDBClient(protoClient, defaultTimeout ?? DefaultTimeout);
    }

    private static void ValidateArgs(string endpoint, string presharedKey)
    {
        if (string.IsNullOrEmpty(endpoint))
            throw new ArgumentException("Endpoint must not be empty.", nameof(endpoint));
        if (string.IsNullOrEmpty(presharedKey))
            throw new ArgumentException("Preshared key must not be empty.", nameof(presharedKey));
    }

    /// <summary>
    /// Escape hatch: the underlying <see cref="SpiceDBProtoClient"/>, with the four
    /// generated gRPC service clients (<c>Permissions</c>, <c>Schema</c>, <c>Watch</c>,
    /// <c>Experimental</c>) this client makes its own calls through.
    /// <para>
    /// Clearly-marked <b>secondary</b> API. Root DESIGN.md's "What NOT To Do" keeps
    /// channels, stubs and metadata out of the primary surface and permits exactly this —
    /// "escape hatches for advanced use are acceptable as clearly marked secondary API" —
    /// so that a request the idiomatic methods cannot express (an RPC or proto field not
    /// wrapped here, such as <c>WriteRelationshipsRequest.OptionalTransactionMetadata</c>,
    /// or the single-check <c>CheckPermission</c> RPC that
    /// <see cref="CheckPermissionAsync"/> deliberately routes around) has a workaround
    /// short of forking the client:
    /// <code>
    /// var response = await client.RawProto().Permissions.CheckPermissionAsync(request);
    /// </code>
    /// </para>
    /// <para>
    /// Four things to know before reaching for it. The bearer token comes free — each
    /// service client is built on an intercepted <see cref="CallInvoker"/>, so a raw call
    /// is authenticated exactly as an idiomatic one is. A raw call is a raw call: no
    /// <c>SpiceDBException</c> mapping (you catch <see cref="RpcException"/>), no retry,
    /// and no <see cref="DefaultTimeout"/> — pass a <c>deadline</c> yourself. Do not
    /// dispose the returned object: it holds this client's own connection, and
    /// <see cref="DisposeAsync"/> is what releases it (or, for a channel you supplied to
    /// <see cref="CreateFromChannel"/>, you are). And there is no stability promise beyond
    /// what <c>Grpc.Net.Client</c> and the generated clients give.
    /// </para>
    /// <para>
    /// It is an accessor, never a constructor: it takes no endpoint, preshared key, or
    /// transport-security argument and hands back a client that already exists, so it
    /// cannot become a second construction path around the guard in
    /// <see cref="CreatePlaintext"/> — root DESIGN.md, "RULE: Credentials over insecure
    /// transport require an explicit opt-in".
    /// </para>
    /// </summary>
    /// <exception cref="InvalidOperationException">
    /// Thrown only for a client built through the internal test-only constructor that takes
    /// service clients directly — that client has no proto client, and no public factory
    /// can produce one.
    /// </exception>
    public SpiceDBProtoClient RawProto() =>
        _protoClient ?? throw new InvalidOperationException(
            "spicedb: this client was constructed from service clients directly (test-only seam) " +
            "and has no underlying SpiceDBProtoClient.");

    /// <summary>
    /// Releases the connection this client created.
    /// <para>
    /// A channel supplied through <see cref="CreateFromChannel"/> is NOT disposed:
    /// it belongs to the caller, who is typically sharing one DI-registered
    /// singleton <see cref="GrpcChannel"/> across the application. Disposing it
    /// here tore down a connection every other consumer was still using — the
    /// first scoped consumer to finish broke the rest. Ownership tracking lives on
    /// <c>SpiceDBProtoClient</c>, which is where the channel is actually disposed.
    /// </para>
    /// </summary>
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
    /// <para>
    /// Large inputs are split automatically into requests of at most 1,000
    /// items and the responses concatenated in input order — SpiceDB rejects
    /// a single request carrying more than 10,000. An empty
    /// <paramref name="relationships"/> sends no request at all and returns
    /// an empty array.
    /// </para>
    /// <para>
    /// Results from one request share a <see cref="CheckResult.CheckedAt"/>
    /// (the response carries a single token for the whole request, not one
    /// per item), so an input large enough to be split carries more than one
    /// token across the returned array.
    /// </para>
    /// <para>
    /// The deadline that bounds a chunked call is the client-level
    /// <see cref="DefaultTimeout"/>, and it bounds <b>each request</b> rather
    /// than the call as a whole; the retry budget is likewise per request.
    /// Worst-case wall time is
    /// <c>ceil(relationships.Length / 1000.0) * DefaultTimeout</c>. That is
    /// deliberate — one deadline spanning every chunk would make a large
    /// check fail purely for being large, and a retry budget shared across
    /// chunks would let one flaky chunk exhaust the allowance for the rest.
    /// Size <see cref="DefaultTimeout"/> per request, and pass a
    /// <see cref="CancellationToken"/> if you need a whole-operation bound.
    /// </para>
    /// <para>
    /// The per-call <c>timeout</c> parameter is not a knob on this path:
    /// <see cref="CheckPermissionAsync"/> is the only caller that supplies
    /// one, and it always passes exactly one relationship, which is never
    /// split. A per-call override on the plural surface would need a new
    /// overload.
    /// </para>
    /// </summary>
    /// <remarks>
    /// This variadic overload does not accept a per-call <c>timeout</c> —
    /// inserting one ahead of <c>params relationships</c> would silently
    /// break any existing positional call site passing relationships right
    /// after <paramref name="cancellationToken"/> (e.g.
    /// <c>CheckPermissionsAsync(cs, "view", default, rel1, rel2)</c>). It is
    /// still bounded by the client's <see cref="DefaultTimeout"/>; use
    /// <see cref="CheckPermissionAsync"/> for a per-call override on checks.
    /// </remarks>
    public async Task<CheckResult[]> CheckPermissionsAsync(
        ConsistencyStrategy consistency,
        string permission,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships) =>
        await CheckPermissionsCoreAsync(consistency, permission, null, cancellationToken, null, relationships);

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
        await CheckPermissionsCoreAsync(consistency, permission, context, cancellationToken, null, relationships);

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
        TimeSpan? timeout,
        Relationship[] relationships)
    {
        ArgumentNullException.ThrowIfNull(consistency);
        if (string.IsNullOrEmpty(permission))
            throw new ArgumentException("Permission must not be empty.", nameof(permission));
        // Zero relationships sends nothing at all. An empty request is not a
        // cheaper way to ask nothing — it is a round trip whose only possible
        // answer is the empty array, and CheckAllAsync already treats an
        // aggregate over zero checks as false rather than a grant.
        if (relationships.Length == 0)
            return [];

        // One request per chunk of DefaultCheckBatchSize, results concatenated
        // in input order so results[i] still corresponds to relationships[i]
        // across the chunk boundary. A caller passing fewer than
        // DefaultCheckBatchSize relationships — the overwhelmingly common
        // case — still makes exactly one request.
        var results = new List<CheckResult>(relationships.Length);
        for (var start = 0; start < relationships.Length; start += DefaultCheckBatchSize)
        {
            var length = Math.Min(DefaultCheckBatchSize, relationships.Length - start);
            results.AddRange(await CheckChunkAsync(
                consistency,
                permission,
                context,
                cancellationToken,
                timeout,
                start,
                relationships[start..(start + length)]));
        }
        return [.. results];
    }

    /// <summary>
    /// Issues one CheckBulkPermissions request for <paramref name="relationships"/>
    /// and maps the response. <paramref name="relationships"/> is non-empty
    /// and no longer than <see cref="DefaultCheckBatchSize"/>;
    /// <see cref="CheckPermissionsCoreAsync"/> is what enforces both. Every
    /// response guard below — the pair-count check and the malformed-oneof
    /// check — therefore applies per chunk, exactly as it applied to the whole
    /// request before chunking.
    /// <para>
    /// <paramref name="offset"/> is this chunk's start index within the
    /// caller's full array. The "check item N" message reports
    /// <c>offset + i</c>, not <c>i</c>: the index a caller sees must be the
    /// one they can use to look up their own relationship. Reporting the
    /// chunk-relative index would attribute the failing item to a different
    /// resource entirely.
    /// </para>
    /// </summary>
    private async Task<CheckResult[]> CheckChunkAsync(
        ConsistencyStrategy consistency,
        string permission,
        IReadOnlyDictionary<string, object>? context,
        CancellationToken cancellationToken,
        TimeSpan? timeout,
        int offset,
        Relationship[] relationships)
    {
        var items = relationships.Select(r => CheckItemFromRel(r, permission, context)).ToList();

        var resp = await RetryAsync(async () =>
            await _permissions.CheckBulkPermissionsAsync(
                new CheckBulkPermissionsRequest
                {
                    Consistency = consistency.V1Consistency,
                    Items = { items },
                },
                deadline: EffectiveDeadline(timeout),
                cancellationToken: cancellationToken),
            cancellationToken);

        // checked_at lives once on the response and applies to every pair —
        // CheckBulkPermissionsResponseItem has no per-item token of its own.
        var checkedAt = resp.CheckedAt?.Token ?? "";

        // The proto guarantees pairs are returned in request order but says
        // nothing about count. A short response would otherwise silently
        // desync results[i] from relationships[i] for every item after the
        // gap — one resource's answer attributed to another. Fail loudly
        // instead of returning a misaligned-but-"successful" array.
        if (resp.Pairs.Count != items.Count)
        {
            throw new SpiceDBException(
                $"CheckBulkPermissions returned {resp.Pairs.Count} pair(s) for {items.Count} " +
                "request item(s).");
        }

        var results = new CheckResult[resp.Pairs.Count];
        for (var i = 0; i < resp.Pairs.Count; i++)
        {
            var pair = resp.Pairs[i];
            if (pair.Error != null)
            {
                // pair.Error is a google.rpc.Status, not a thrown RpcException. ToRpcException
                // (Google.Api.CommonProtos) turns it into one so the per-item error routes
                // through the same ErrorMapper switch as every other RPC in this client, instead
                // of discarding the code and throwing the base SpiceDBException. It also carries
                // the status's own details across, so a per-item failure reaches the caller with
                // the same structured reason an RPC-level failure does — hand-building a
                // Grpc.Core.Status from just the code and message dropped them. See root
                // DESIGN.md, "RULE: Error mapping must not lose the server's detail".
                throw ErrorMapper.ToSpiceDBException(pair.Error.ToRpcException());
            }
            else if (pair.Item != null)
            {
                results[i] = ToCheckResult(pair.Item, checkedAt);
            }
            else
            {
                // pair.Response is a oneof — a well-behaved server always sets
                // it to Item or Error, so this should be unreachable in
                // practice. Mirrors spicedb-rust's malformed-oneof guard:
                // fail loudly instead of dereferencing a null Item.
                throw new SpiceDBException(
                    $"check item {offset + i}: malformed CheckBulkPermissionsPair (neither Item " +
                    "nor Error set).");
            }
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
        IReadOnlyDictionary<string, object>? context = null,
        TimeSpan? timeout = null)
    {
        var results = await CheckPermissionsCoreAsync(consistency, permission, context, cancellationToken, timeout, [relationship]);
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
        var results = await CheckPermissionsCoreAsync(consistency, permission, null, cancellationToken, null, relationships);
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
        var results = await CheckPermissionsCoreAsync(consistency, permission, context, cancellationToken, null, relationships);
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
        // Enumerable.All is vacuously true over an empty sequence, so without this guard an empty
        // relationships array — reached via CheckPermissionsCoreAsync's own early return of []  —
        // would silently produce "all checks passed" for zero checks. Guard explicitly instead of
        // relying on that shared early return (RULE: "An aggregate over zero checks is not a grant").
        if (relationships.Length == 0)
            return false;

        var results = await CheckPermissionsCoreAsync(consistency, permission, null, cancellationToken, null, relationships);
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
        // See CheckAllAsync: guard the empty case explicitly rather than relying on
        // CheckPermissionsCoreAsync's early return of [], which would otherwise feed
        // Enumerable.All an empty sequence and vacuously return true.
        if (relationships.Length == 0)
            return false;

        var results = await CheckPermissionsCoreAsync(consistency, permission, context, cancellationToken, null, relationships);
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(transaction);

        var req = new WriteRelationshipsRequest();
        req.Updates.AddRange(transaction.V1Updates);
        if (transaction.Preconditions.Count > 0)
            req.OptionalPreconditions.AddRange(transaction.Preconditions);

        var resp = await CallOnceAsync(async () =>
            await _permissions.WriteRelationshipsAsync(
                req,
                deadline: EffectiveDeadline(timeout),
                cancellationToken: cancellationToken));

        return resp.WrittenAt?.Token ?? "";
    }

    /// <summary>
    /// Returns an async enumerable of relationships matching the given filter.
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 relationships using the AfterResultCursor.
    /// <para>
    /// Stream/page ESTABLISHMENT is retried on transient errors (the same
    /// {UNAVAILABLE, ABORTED} predicate and backoff/attempt budget as
    /// unary calls), with the attempt budget reset for each new
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
                    await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
                    await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
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

            var resp = await CallOnceAsync(async () =>
                await _permissions.DeleteRelationshipsAsync(
                    req,
                    deadline: EffectiveDeadline(timeout),
                    cancellationToken: cancellationToken));

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
    /// <param name="withDebug">
    /// When true, asks the server to attach debug information to an error, if one occurs. As of
    /// this writing SpiceDB only populates it for a maximum-recursion-depth error, and the
    /// information arrives as additional detail on the underlying <c>RpcException</c> preserved as
    /// <see cref="SpiceDBException"/>'s <see cref="Exception.InnerException"/> — this parameter
    /// does not change anything about a successful result.
    /// </param>
    public async IAsyncEnumerable<LookupResource> LookupResourcesAsync(
        ConsistencyStrategy consistency,
        string resourceType,
        string permission,
        string subjectType,
        string subjectID,
        [EnumeratorCancellation] CancellationToken cancellationToken = default,
        bool withDebug = false)
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
                WithDebug = withDebug,
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
                    await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
                    await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
                await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
            await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        var resp = await RetryAsync(async () =>
            await _schema.ReadSchemaAsync(
                new ReadSchemaRequest(),
                deadline: EffectiveDeadline(timeout),
                cancellationToken: cancellationToken),
            cancellationToken);

        return (resp.SchemaText, resp.ReadAt?.Token ?? "");
    }

    /// <summary>
    /// Writes a new schema to SpiceDB, returning the revision.
    /// </summary>
    public async Task<string> WriteSchemaAsync(
        string schema,
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        if (string.IsNullOrEmpty(schema))
            throw new ArgumentException("Schema must not be empty.", nameof(schema));

        var resp = await CallOnceAsync(async () =>
            await _schema.WriteSchemaAsync(
                new WriteSchemaRequest { Schema = schema },
                deadline: EffectiveDeadline(timeout),
                cancellationToken: cancellationToken));

        return resp.WrittenAt?.Token ?? "";
    }

    /// <summary>
    /// Returns the definitions and caveats in the current schema.
    /// </summary>
    public async Task<ReflectSchemaResult> ReflectSchemaAsync(
        ConsistencyStrategy consistency,
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        var resp = await RetryAsync(async () =>
            await _schema.ReflectSchemaAsync(
                new ReflectSchemaRequest { Consistency = consistency.V1Consistency },
                deadline: EffectiveDeadline(timeout),
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
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
                deadline: EffectiveDeadline(timeout),
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
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
                deadline: EffectiveDeadline(timeout),
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        var resp = await RetryAsync(async () =>
            await _schema.DiffSchemaAsync(
                new DiffSchemaRequest
                {
                    Consistency = consistency.V1Consistency,
                    ComparisonSchema = comparisonSchema,
                },
                deadline: EffectiveDeadline(timeout),
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
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
                deadline: EffectiveDeadline(timeout),
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
    ///
    /// <para>
    /// <c>ImportBulkRelationships</c> is client-streaming: its duration scales
    /// with the size of <paramref name="relationships"/>, not with server
    /// latency, so unlike every other method on this client, this call does
    /// NOT fall back to the client's default timeout (root DESIGN.md, "RULE:
    /// A unary call must have a deadline", clause 3). Omitting
    /// <paramref name="timeout"/> means this call is unbounded; pass it
    /// explicitly to bound a bulk import. (<paramref name="cancellationToken"/>
    /// still cancels the call regardless.)
    /// </para>
    /// </summary>
    public async Task<ulong> ImportRelationshipsAsync(
        IAsyncEnumerable<Relationship> relationships,
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(relationships);

        AsyncClientStreamingCall<ImportBulkRelationshipsRequest, ImportBulkRelationshipsResponse> stream;
        try
        {
            stream = _permissions.ImportBulkRelationships(
                deadline: DeadlineOrNull(timeout),
                cancellationToken: cancellationToken);
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
                    await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
                    await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
    /// Returns an async enumerable of watch events from SpiceDB's watch API,
    /// starting from the given revision. Each yielded <see cref="WatchEvent"/>
    /// corresponds to one server response: zero or more relationship updates,
    /// all current through <see cref="WatchEvent.ChangesThrough"/>.
    /// <para>
    /// <see cref="WatchEvent.ChangesThrough"/> is always populated. Pass it as
    /// <paramref name="startRevision"/> on a later call to resume after a
    /// dropped stream, instead of restarting from the original
    /// <paramref name="startRevision"/> (reprocessing everything since,
    /// possibly past the GC window) or from head (silently losing every
    /// change in the gap).
    /// </para>
    /// <para>
    /// <paramref name="includeCheckpoints"/> requests periodic checkpoint
    /// events (<see cref="WatchEvent.IsCheckpoint"/>, no updates) in addition
    /// to relationship updates. Recommended if this SpiceDB instance is
    /// running behind a proxy that aborts idle connections, since a
    /// checkpoint keeps the stream alive even when there are no changes.
    /// </para>
    /// <para>
    /// Watch ESTABLISHMENT is retried on transient errors — but only up until
    /// the first event is yielded. Once anything has been yielded from the
    /// current watch stream, a transient error is mapped and rethrown instead
    /// of retried; retrying mid-watch would risk re-delivering already-seen
    /// updates (or silently skipping ones the caller never saw), and there is
    /// no cursor to safely resume from mid-stream. See
    /// <see cref="ReadRelationshipsAsync"/> for the full rationale.
    /// </para>
    /// </summary>
    public async IAsyncEnumerable<WatchEvent> UpdatesAsync(
        IEnumerable<string>? objectTypes = null,
        string? startRevision = null,
        bool includeCheckpoints = false,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        var req = new WatchRequest();
        if (objectTypes != null)
            req.OptionalObjectTypes.AddRange(objectTypes);
        if (!string.IsNullOrEmpty(startRevision))
            req.OptionalStartCursor = new ZedToken { Token = startRevision };
        if (includeCheckpoints)
        {
            // OptionalUpdateKinds is empty-means-default (relationship updates only, for
            // backwards compatibility) -- a non-empty list is the exact set requested, so asking
            // for checkpoints must also spell out relationship updates or the server would stop
            // sending them.
            req.OptionalUpdateKinds.Add(WatchKind.IncludeRelationshipUpdates);
            req.OptionalUpdateKinds.Add(WatchKind.IncludeCheckpoints);
        }

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
                await Task.Delay(JitteredDelay(backoff), cancellationToken);
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

                yielded++;
                yield return new WatchEvent
                {
                    Updates = resp!.Updates.Select(UpdateFromProto).ToList(),
                    ChangesThrough = resp.ChangesThrough?.Token ?? "",
                    IsCheckpoint = resp.IsCheckpoint,
                };
            }

            // Reached only via the transient-retry break above.
            await Task.Delay(JitteredDelay(backoff), cancellationToken);
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        if (string.IsNullOrEmpty(name))
            throw new ArgumentException("Name must not be empty.", nameof(name));
        ArgumentNullException.ThrowIfNull(filter);

        await CallOnceAsync(async () =>
            await _experimental.ExperimentalRegisterRelationshipCounterAsync(
                new ExperimentalRegisterRelationshipCounterRequest
                {
                    Name = name,
                    RelationshipFilter = filter.ToProto(),
                },
                deadline: EffectiveDeadline(timeout),
                cancellationToken: cancellationToken));
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        if (string.IsNullOrEmpty(name))
            throw new ArgumentException("Name must not be empty.", nameof(name));

        var resp = await RetryAsync(async () =>
            await _experimental.ExperimentalCountRelationshipsAsync(
                new ExperimentalCountRelationshipsRequest { Name = name },
                deadline: EffectiveDeadline(timeout),
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
        CancellationToken cancellationToken = default,
        TimeSpan? timeout = null)
    {
        if (string.IsNullOrEmpty(name))
            throw new ArgumentException("Name must not be empty.", nameof(name));

        await CallOnceAsync(async () =>
            await _experimental.ExperimentalUnregisterRelationshipCounterAsync(
                new ExperimentalUnregisterRelationshipCounterRequest { Name = name },
                deadline: EffectiveDeadline(timeout),
                cancellationToken: cancellationToken));
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
                merged.Fields[key] = ToProtoValueForKey(key, value);
        }
        if (item != null)
        {
            // Applied after callLevel so item keys overwrite matching
            // call-level keys — this IS the "item wins" half of the merge
            // rule. Call-level keys the item doesn't mention are untouched
            // by this loop, which is the "call-level retained" half.
            foreach (var (key, value) in item)
                merged.Fields[key] = ToProtoValueForKey(key, value);
        }

        return merged;
    }

    /// <summary>
    /// Converts a native .NET value into a protobuf Struct <see cref="Value"/>
    /// by dispatching on type — numbers, booleans, null, and nested
    /// maps/lists all land on their matching <c>kind</c> oneof case rather
    /// than being stringified. This is the single converter for caveat
    /// context on both the check path (call-level and per-item context
    /// merged in <see cref="MergeCheckContext"/>) and the write path
    /// (<see cref="Relationship.ToProto"/>'s <c>CaveatContext</c>) — both
    /// send values through this method so a numeric caveat parameter (e.g. a
    /// schema's <c>now &lt; 100</c> compared against an <c>int</c>) evaluates
    /// correctly instead of comparing against a string SpiceDB would reject
    /// or fail to coerce, whichever surface it was supplied on.
    /// </summary>
    /// <exception cref="InvalidArgumentException">
    /// Thrown when <paramref name="value"/>'s type cannot be represented on the wire (e.g. a
    /// custom class instance) — never silently stringified.
    /// </exception>
    internal static Value ToProtoValue(object? value) => value switch
    {
        null => Value.ForNull(),
        bool b => Value.ForBool(b),
        string s => Value.ForString(s),
        sbyte or byte or short or ushort or int or uint or long or ulong or float or double or decimal =>
            Value.ForNumber(Convert.ToDouble(value)),
        IReadOnlyDictionary<string, object> nested => Value.ForStruct(ToProtoStructFrom(nested)),
        System.Collections.IEnumerable list => Value.ForList(ToProtoValueList(list)),
        // A value this conversion cannot represent (e.g. a custom class instance) came from the
        // caller, who can see this error and fix their input -- stringifying it instead would
        // silently produce a caveat context value SpiceDB never intended, per root DESIGN.md
        // "RULE: A conversion that cannot preserve meaning must fail", clause 1. Shared by both
        // the check path (MergeCheckContext) and the write path (Relationship.ToProto).
        _ => throw new InvalidArgumentException(
            $"unsupported caveat context value type: {value.GetType()}"),
    };

    /// <summary>
    /// Calls <see cref="ToProtoValue"/> for one caveat-context entry, and — if it throws
    /// <see cref="InvalidArgumentException"/> because <paramref name="value"/>'s type cannot be
    /// represented — re-raises with <paramref name="key"/> named, so the caller can tell which
    /// entry in their context dictionary needs fixing rather than just "some value, somewhere."
    /// For a nested dictionary, the innermost failure names its own (nested) key first, and each
    /// enclosing call adds its key in turn, so the message traces the full path to the offending
    /// entry.
    /// </summary>
    internal static Value ToProtoValueForKey(string key, object? value)
    {
        try
        {
            return ToProtoValue(value);
        }
        catch (InvalidArgumentException e)
        {
            throw new InvalidArgumentException($"caveat context key \"{key}\": {e.Message}", e);
        }
    }

    private static Struct ToProtoStructFrom(IReadOnlyDictionary<string, object> dict)
    {
        var s = new Struct();
        foreach (var (key, value) in dict)
            s.Fields[key] = ToProtoValueForKey(key, value);
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
    /// Converts a protobuf Struct <see cref="Value"/> into a native .NET
    /// value by dispatching on its <c>kind</c> oneof — the read-side inverse
    /// of <see cref="ToProtoValue"/>. Used by <see cref="Relationship.FromProto"/>
    /// to read a stored relationship's caveat context back without
    /// stringifying it: <c>number_value</c> becomes a <c>double</c> (a
    /// integer caveat parameter legitimately round-trips as e.g. <c>42.0</c>
    /// — <c>google.protobuf.Value.number_value</c> is a double on the wire,
    /// not a defect in this conversion), <c>bool_value</c> a <c>bool</c>,
    /// <c>null_value</c> becomes .NET <c>null</c>, and
    /// <c>struct_value</c>/<c>list_value</c> recurse. Internal — exposed to
    /// the test assembly via InternalsVisibleTo; not part of the public API.
    /// </summary>
    internal static object? FromProtoValue(Value value) => value.KindCase switch
    {
        Value.KindOneofCase.NullValue => null,
        Value.KindOneofCase.BoolValue => value.BoolValue,
        Value.KindOneofCase.NumberValue => value.NumberValue,
        Value.KindOneofCase.StringValue => value.StringValue,
        Value.KindOneofCase.StructValue => FromProtoStruct(value.StructValue),
        Value.KindOneofCase.ListValue => value.ListValue.Values.Select(FromProtoValue).ToList(),
        _ => null,
    };

    /// <summary>
    /// Converts every field of a protobuf <see cref="Struct"/> via
    /// <see cref="FromProtoValue"/>. Internal — exposed to the test assembly
    /// via InternalsVisibleTo; not part of the public API.
    /// </summary>
    internal static IReadOnlyDictionary<string, object> FromProtoStruct(Struct s) =>
        s.Fields.ToDictionary(f => f.Key, f => FromProtoValue(f.Value)!);

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
        return new RelationshipUpdate
        {
            Operation = op,
            Relationship = Relationship.FromProto(pu.Relationship),
        };
    }

    /// <summary>
    /// Retries an async operation with full-jitter exponential backoff for
    /// transient gRPC errors. Only for idempotent (read) operations — see
    /// <see cref="CallOnceAsync{T}"/> for mutations.
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
                await Task.Delay(JitteredDelay(backoff), cancellationToken);
                backoff *= 2;
            }
            catch (RpcException ex)
            {
                throw ErrorMapper.ToSpiceDBException(ex);
            }
        }
    }

    /// <summary>
    /// Runs an async operation once, converting a gRPC error, but never
    /// retrying.
    /// <para>
    /// For mutations. A <c>WriteRelationships</c> containing
    /// <c>OPERATION_CREATE</c>, or any request carrying preconditions, is
    /// not idempotent: if it commits and the response is lost (a rolling
    /// restart, a proxy dropping the connection), a retry would surface
    /// <c>ALREADY_EXISTS</c>/<c>FAILED_PRECONDITION</c> for a write that in
    /// fact succeeded, and the caller would wrongly conclude it had
    /// failed. See DESIGN.md, "Automatic retry is for idempotent
    /// operations only".
    /// </para>
    /// </summary>
    private static async Task<T> CallOnceAsync<T>(Func<Task<T>> operation)
    {
        try
        {
            return await operation();
        }
        catch (RpcException ex)
        {
            throw ErrorMapper.ToSpiceDBException(ex);
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
