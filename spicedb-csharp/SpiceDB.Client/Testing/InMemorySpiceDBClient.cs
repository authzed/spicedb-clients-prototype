using System.Globalization;
using System.Runtime.CompilerServices;
using Grpc.Core;

namespace SpiceDB.Client.Testing;

/// <summary>
/// An in-memory test double for the relationship and check surface of
/// <see cref="SpiceDBClient"/>, implementing <see cref="ISpiceDBRelationshipClient"/> with the
/// same signatures. It stores relationships and applies SpiceDB's write semantics: preconditions
/// (<c>MustMatch</c>/<c>MustNotMatch</c>), CREATE conflicts (<see cref="AlreadyExistsException"/>),
/// TOUCH upserts and idempotent DELETE, all atomically per write; filtered reads and bounded or
/// complete deletes use <see cref="Filter.Matches"/>. Checks return results registered with
/// <see cref="SetCheckResult"/> or <see cref="SetCheckError"/>, and
/// <see cref="Permissionship.NoPermission"/> otherwise; it does not evaluate a schema.
/// Errors are built with <see cref="SpiceDBException.Create"/>, so every predicate treats them
/// like the server's. Thread-safe.
/// </summary>
public sealed class InMemorySpiceDBClient : ISpiceDBRelationshipClient
{
    private readonly object _lock = new();
    private readonly List<Relationship> _relationships = [];
    private readonly Dictionary<string, (CheckResult? Result, SpiceDBException? Error)> _checks = new(StringComparer.Ordinal);
    private long _revision;

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

    public Task<CheckResult> CheckAsync(PermissionCheck request, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        Native.Required(request, nameof(request));
        lock (_lock)
        {
            var (result, error) = Lookup(request);
            if (error is not null) throw error;
            return Task.FromResult(result!);
        }
    }

    public Task<IReadOnlyList<CheckResult>> CheckBulkAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var items = Native.Required(requests, nameof(requests)).ToArray();
        if (items.Any(x => x is null)) throw new InvalidArgumentException("Requests must not contain null.");
        if (items.Any(x => x.Consistency != items[0].Consistency)) throw new InvalidArgumentException("Bulk requests must have equal consistency strategies and tokens.");
        var results = new List<CheckResult>(items.Length);
        lock (_lock)
        {
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
        cancellationToken.ThrowIfCancellationRequested();
        Native.Required(transaction, nameof(transaction));
        lock (_lock)
        {
            CheckPreconditions(transaction.Preconditions);
            var next = new List<Relationship>(_relationships);
            foreach (var update in transaction.Updates)
            {
                var index = next.FindIndex(r => SameTuple(r, update.Relationship));
                switch (update.Operation)
                {
                    case UpdateOperation.Create when index >= 0:
                        throw SpiceDBException.Create(StatusCode.AlreadyExists, $"could not CREATE relationship `{update.Relationship}`, as it already existed");
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
                        throw SpiceDBException.Create(StatusCode.InvalidArgument, "relationship update operation is unspecified");
                }
            }
            _relationships.Clear(); _relationships.AddRange(next);
            _revision++;
            return Task.FromResult(new WriteResult(Token()));
        }
    }

    public Task<DeleteResult> DeleteRelationshipsAsync(Filter filter, int? limit, DeleteOptions? options = null, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        Native.Required(filter, nameof(filter)).ToProto();
        var partial = options?.Partial != PartialDeletion.Disallowed;
        if (limit is { } l && l <= 0) throw new InvalidArgumentException("limit must be positive.");
        if (limit is null && partial) throw new InvalidArgumentException("limit is required unless Partial is PartialDeletion.Disallowed.");
        lock (_lock)
        {
            CheckPreconditions((options?.MustMatch ?? []).Select(f => new Precondition(PreconditionOperation.MustMatch, f))
                .Concat((options?.MustNotMatch ?? []).Select(f => new Precondition(PreconditionOperation.MustNotMatch, f))).ToArray());
            var matches = _relationships.Where(filter.Matches).ToList();
            if (!partial && limit is { } max && matches.Count > max)
                throw SpiceDBException.Create(StatusCode.FailedPrecondition, $"found more than {max} relationships to be deleted and partial deletion was not requested");
            var deleted = limit is { } bound ? matches.Take(bound).ToList() : matches;
            foreach (var r in deleted) _relationships.Remove(r);
            _revision++;
            var completed = deleted.Count == matches.Count;
            return Task.FromResult(new DeleteResult(Token(), (ulong)deleted.Count, completed, completed ? null : "mem-delete"));
        }
    }

    public async Task<DeleteResult> DeleteAllRelationshipsNonAtomicAsync(Filter filter, DeleteAllOptions? options = null, CancellationToken cancellationToken = default)
    {
        var size = options?.PageSize ?? 1000;
        if (size <= 0) throw new InvalidArgumentException("PageSize must be positive.");
        ulong total = 0;
        while (true)
        {
            var page = await DeleteRelationshipsAsync(filter, size, new DeleteOptions { MustMatch = options?.MustMatch ?? [], MustNotMatch = options?.MustNotMatch ?? [] }, cancellationToken);
            total += page.DeletedCount;
            options?.Progress?.Report(page with { DeletedCount = total });
            if (page.Completed) return page with { DeletedCount = total };
        }
    }

    public async IAsyncEnumerable<ReadRelationship> ReadRelationshipsAsync(Consistency consistency, Filter filter, ReadOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        Native.Required(consistency, nameof(consistency));
        Native.Required(filter, nameof(filter)).ToProto();
        Relationship[] snapshot; ZedToken readAt;
        lock (_lock) { snapshot = _relationships.Where(filter.Matches).ToArray(); readAt = Token(); }
        var start = options?.ResumeCursor is { } cursor ? int.Parse(cursor, CultureInfo.InvariantCulture) : 0;
        var limit = options?.Limit ?? int.MaxValue;
        for (var i = start; i < snapshot.Length && i - start < limit; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            await Task.Yield();
            yield return new(snapshot[i], (i + 1).ToString(CultureInfo.InvariantCulture), readAt);
        }
    }

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
                throw SpiceDBException.Create(StatusCode.FailedPrecondition, $"unable to satisfy write precondition `{p}`");
        }
    }

    private static bool SameTuple(Relationship a, Relationship b) =>
        a.Resource == b.Resource && a.Relation == b.Relation && a.Subject == b.Subject;

    private static string Key(ObjectRef resource, string permission, SubjectRef subject) =>
        $"{Native.Required(resource, nameof(resource))}#{Native.Required(permission, nameof(permission))}@{Native.Required(subject, nameof(subject))}";

    private ZedToken Token() => new("mem-" + _revision.ToString(CultureInfo.InvariantCulture));
}
