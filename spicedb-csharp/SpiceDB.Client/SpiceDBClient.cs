// SpiceDBClient is the idiomatic C# client for SpiceDB.

using System.Runtime.CompilerServices;
using Authzed.Api.V1;
using Google.Protobuf.WellKnownTypes;
using Grpc.Core;
using Grpc.Net.Client;

namespace SpiceDB.Client;

/// <summary>
/// The idiomatic SpiceDB client. Use <see cref="CreatePlaintext"/> or
/// <see cref="CreateSystemTls"/> to create one. Implements
/// <see cref="IAsyncDisposable"/> — the channel is disposed when the client is.
/// </summary>
public sealed class SpiceDBClient : IAsyncDisposable
{
    private const int DefaultReadPageSize = 512;
    private const int DefaultDeletePageSize = 10_000;
    private const int DefaultLookupPageSize = 512;
    private const int DefaultImportBatchSize = 1_000;
    private const int DefaultExportPageSize = 512;
    private const int DefaultCheckBatchSize = 1_000;
    private const int MaxRetryAttempts = 5;
    private static readonly TimeSpan InitialBackoff = TimeSpan.FromMilliseconds(100);

    private readonly GrpcChannel _channel;
    private readonly Metadata _metadata;
    private readonly PermissionsService.PermissionsServiceClient _permissions;
    private readonly SchemaService.SchemaServiceClient _schema;
    private readonly WatchService.WatchServiceClient _watch;
    private readonly ExperimentalService.ExperimentalServiceClient _experimental;

    private SpiceDBClient(GrpcChannel channel, string presharedKey)
    {
        _channel = channel;
        _metadata = new Metadata
        {
            { "authorization", $"Bearer {presharedKey}" },
        };
        _permissions = new PermissionsService.PermissionsServiceClient(channel);
        _schema = new SchemaService.SchemaServiceClient(channel);
        _watch = new WatchService.WatchServiceClient(channel);
        _experimental = new ExperimentalService.ExperimentalServiceClient(channel);
    }

    /// <summary>
    /// Creates a client with a plaintext (insecure) connection. Use this for
    /// testing only — the lack of TLS is made obvious by the name.
    /// </summary>
    /// <exception cref="ArgumentException">Thrown when endpoint or presharedKey is empty.</exception>
    public static SpiceDBClient CreatePlaintext(string endpoint, string presharedKey)
    {
        ValidateArgs(endpoint, presharedKey);
        var channel = GrpcChannel.ForAddress($"http://{endpoint}", new GrpcChannelOptions
        {
            Credentials = ChannelCredentials.Insecure,
        });
        return new SpiceDBClient(channel, presharedKey);
    }

    /// <summary>
    /// Creates a client using the system's TLS certificate pool. Use this
    /// for production connections.
    /// </summary>
    /// <exception cref="ArgumentException">Thrown when endpoint or presharedKey is empty.</exception>
    public static SpiceDBClient CreateSystemTls(string endpoint, string presharedKey)
    {
        ValidateArgs(endpoint, presharedKey);
        var channel = GrpcChannel.ForAddress($"https://{endpoint}");
        return new SpiceDBClient(channel, presharedKey);
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
        return new SpiceDBClient(channel, presharedKey);
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
        _channel.Dispose();
        return ValueTask.CompletedTask;
    }

    // ──────────────────────────────────────────────────────────────────────
    // Checks — all via BulkCheckPermissions
    // ──────────────────────────────────────────────────────────────────────

    /// <summary>
    /// Performs a bulk permission check on the given relationships and returns
    /// a bool for each relationship indicating whether permission is granted.
    /// All checks use BulkCheckPermissions under the hood.
    /// </summary>
    public async Task<bool[]> CheckPermissionsAsync(
        ConsistencyStrategy consistency,
        string permission,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships)
    {
        ArgumentNullException.ThrowIfNull(consistency);
        if (string.IsNullOrEmpty(permission))
            throw new ArgumentException("Permission must not be empty.", nameof(permission));
        if (relationships.Length == 0)
            return [];

        var items = relationships.Select(r => CheckItemFromRel(r, permission)).ToList();

        var resp = await RetryAsync(async () =>
            await _permissions.CheckBulkPermissionsAsync(
                new CheckBulkPermissionsRequest
                {
                    Consistency = consistency.V1Consistency,
                    Items = { items },
                },
                headers: _metadata,
                cancellationToken: cancellationToken),
            cancellationToken);

        var results = new bool[resp.Pairs.Count];
        for (var i = 0; i < resp.Pairs.Count; i++)
        {
            var pair = resp.Pairs[i];
            if (pair.Error != null)
                throw new SpiceDBException($"Check item {i}: {pair.Error.Message}");
            results[i] = pair.Item.Permissionship == CheckPermissionResponse.Types.Permissionship.HasPermission;
        }
        return results;
    }

    /// <summary>
    /// Checks a single permission and returns true if granted.
    /// </summary>
    public async Task<bool> CheckPermissionAsync(
        ConsistencyStrategy consistency,
        string permission,
        Relationship relationship,
        CancellationToken cancellationToken = default)
    {
        var results = await CheckPermissionsAsync(consistency, permission, cancellationToken, relationship);
        return results[0];
    }

    /// <summary>
    /// Returns true if any of the given relationships have the permission.
    /// </summary>
    public async Task<bool> CheckAnyAsync(
        ConsistencyStrategy consistency,
        string permission,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships)
    {
        var results = await CheckPermissionsAsync(consistency, permission, cancellationToken, relationships);
        return results.Any(r => r);
    }

    /// <summary>
    /// Returns true if all of the given relationships have the permission.
    /// </summary>
    public async Task<bool> CheckAllAsync(
        ConsistencyStrategy consistency,
        string permission,
        CancellationToken cancellationToken = default,
        params Relationship[] relationships)
    {
        var results = await CheckPermissionsAsync(consistency, permission, cancellationToken, relationships);
        return results.All(r => r);
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
                headers: _metadata,
                cancellationToken: cancellationToken),
            cancellationToken);

        return resp.WrittenAt?.Token ?? "";
    }

    /// <summary>
    /// Returns an async enumerable of relationships matching the given filter.
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 relationships using the AfterResultCursor.
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

            using var stream = _permissions.ReadRelationships(req, headers: _metadata, cancellationToken: cancellationToken);

            uint count = 0;
            while (await stream.ResponseStream.MoveNext(cancellationToken))
            {
                count++;
                var resp = stream.ResponseStream.Current;
                cursor = resp.AfterResultCursor;
                yield return Relationship.FromProto(resp.Relationship);
            }

            if (count < DefaultReadPageSize)
                yield break;
        }
    }

    /// <summary>
    /// Deletes all relationships matching the given filter. Large result sets
    /// are automatically paged in batches of 10,000. Returns the revision of
    /// the final deletion.
    /// </summary>
    public async Task<string> DeleteRelationshipsAsync(
        Filter filter,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(filter);

        string revision = "";
        while (true)
        {
            var resp = await RetryAsync(async () =>
                await _permissions.DeleteRelationshipsAsync(
                    new DeleteRelationshipsRequest
                    {
                        RelationshipFilter = filter.ToProto(),
                        OptionalLimit = DefaultDeletePageSize,
                        OptionalAllowPartialDeletions = true,
                    },
                    headers: _metadata,
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
    /// Returns an async enumerable of resource IDs of the given type that the
    /// subject has the specified permission on. Cursors are handled
    /// transparently with 512-item pages.
    /// </summary>
    public async IAsyncEnumerable<string> LookupResourcesAsync(
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

            using var stream = _permissions.LookupResources(req, headers: _metadata, cancellationToken: cancellationToken);

            int count = 0;
            while (await stream.ResponseStream.MoveNext(cancellationToken))
            {
                count++;
                var resp = stream.ResponseStream.Current;
                cursor = resp.AfterResultCursor;
                yield return resp.ResourceObjectId;
            }

            if (count < DefaultLookupPageSize)
                yield break;
        }
    }

    /// <summary>
    /// Returns an async enumerable of subject IDs of the given type that have
    /// the specified permission on the resource. Unlike LookupResources,
    /// LookupSubjects does not currently support cursor-based pagination in
    /// SpiceDB and streams all results in a single server-streaming call.
    /// </summary>
    public async IAsyncEnumerable<string> LookupSubjectsAsync(
        ConsistencyStrategy consistency,
        string resourceType,
        string resourceID,
        string permission,
        string subjectType,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(consistency);

        using var stream = _permissions.LookupSubjects(
            new LookupSubjectsRequest
            {
                Consistency = consistency.V1Consistency,
                Resource = new ObjectReference
                {
                    ObjectType = resourceType,
                    ObjectId = resourceID,
                },
                Permission = permission,
                SubjectObjectType = subjectType,
            },
            headers: _metadata,
            cancellationToken: cancellationToken);

        while (await stream.ResponseStream.MoveNext(cancellationToken))
        {
            var resp = stream.ResponseStream.Current;
            var subjectId = resp.Subject?.SubjectObjectId;
            if (string.IsNullOrEmpty(subjectId))
                subjectId = resp.SubjectObjectId; // deprecated field fallback
            yield return subjectId;
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
                headers: _metadata,
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
                headers: _metadata,
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
                headers: _metadata,
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
                headers: _metadata,
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
                headers: _metadata,
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
                headers: _metadata,
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
                headers: _metadata,
                cancellationToken: cancellationToken),
            cancellationToken);

        return new ExpandResult
        {
            TreeRoot = resp.TreeRoot,
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

        using var stream = _permissions.ImportBulkRelationships(headers: _metadata, cancellationToken: cancellationToken);

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

    /// <summary>
    /// Returns an async enumerable over all relationships matching the optional
    /// filter, streamed from SpiceDB in bulk. Cursors are handled transparently
    /// with 512-item pages.
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

            using var stream = _permissions.ExportBulkRelationships(req, headers: _metadata, cancellationToken: cancellationToken);

            int pageCount = 0;
            while (await stream.ResponseStream.MoveNext(cancellationToken))
            {
                var resp = stream.ResponseStream.Current;
                cursor = resp.AfterResultCursor;
                foreach (var r in resp.Relationships)
                {
                    pageCount++;
                    yield return Relationship.FromProto(r);
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

        using var stream = _watch.Watch(req, headers: _metadata, cancellationToken: cancellationToken);

        while (await stream.ResponseStream.MoveNext(cancellationToken))
        {
            var resp = stream.ResponseStream.Current;
            foreach (var update in resp.Updates)
            {
                yield return UpdateFromProto(update);
            }
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
                headers: _metadata,
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
                headers: _metadata,
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
                headers: _metadata,
                cancellationToken: cancellationToken),
            cancellationToken);
    }

    // ──────────────────────────────────────────────────────────────────────
    // Internals
    // ──────────────────────────────────────────────────────────────────────

    private static CheckBulkPermissionsRequestItem CheckItemFromRel(Relationship r, string permission) =>
        new()
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
    /// <summary>
    /// The root of the expanded permission tree. This is the underlying proto
    /// type since the tree structure is complex and deeply nested.
    /// </summary>
    public PermissionRelationshipTree? TreeRoot { get; init; }
    public string Revision { get; init; } = "";
}

/// <summary>Holds the result of a relationship count operation.</summary>
public sealed record CountResult
{
    public ulong RelationshipCount { get; init; }
    public string Revision { get; init; } = "";
}
