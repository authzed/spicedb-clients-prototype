using Grpc.Core;
using V1 = Authzed.Api.V1;

namespace SpiceDB.Client;

/// <summary>
/// Maps server responses on the client's call paths, failing a response the client cannot
/// interpret with an <see cref="StatusCode.Internal"/> <see cref="SpiceDBException"/>. Such a
/// response is never skipped and its missing fields are never filled with empty values: a
/// skipped item makes a full page look short and silently ends a paged read early. No retry
/// policy treats <see cref="StatusCode.Internal"/> as retryable by default.
/// </summary>
internal static class Response
{
    /// <summary>
    /// The error for a response the client cannot interpret. Its message says the client
    /// detected it, so it is not mistaken for an <see cref="StatusCode.Internal"/> the server sent.
    /// </summary>
    internal static SpiceDBException Malformed(string operation, string detail) =>
        SpiceDBException.InternalError($"{operation}: {ClientDetected}: {detail}.");

    /// <summary>The words every client-detected malformed-response message carries.</summary>
    internal const string ClientDetected = "client detected malformed response from server";

    /// <summary>
    /// A check permissionship the server always sets: an unset (zero, <c>PERMISSIONSHIP_UNSPECIFIED</c>) one is
    /// malformed, never <see cref="Client.Permissionship.Unspecified"/>. An unrecognised newer
    /// value still maps to the safe <see cref="Client.Permissionship.Unspecified"/>.
    /// </summary>
    internal static Permissionship CheckPermissionship(V1.CheckPermissionResponse.Types.Permissionship value, string operation) =>
        value == V1.CheckPermissionResponse.Types.Permissionship.Unspecified
            ? throw Malformed(operation, "permissionship is unset")
            : SpiceDBClient.ToCheckPermissionship(value);

    /// <summary>A lookup permissionship the server always sets; see <see cref="CheckPermissionship"/>.</summary>
    internal static Permissionship LookupPermissionship(V1.LookupPermissionship value, string operation) =>
        value == V1.LookupPermissionship.Unspecified
            ? throw Malformed(operation, "permissionship is unset")
            : SpiceDBClient.ToPermissionship(value);

    internal static ObjectRef Object(V1.ObjectReference? value, string operation, string field)
    {
        if (value is null) throw Malformed(operation, $"missing {field}");
        if (string.IsNullOrEmpty(value.ObjectType)) throw Malformed(operation, $"{field} has no object type");
        if (string.IsNullOrEmpty(value.ObjectId)) throw Malformed(operation, $"{field} has no object id");
        return new(value.ObjectType, value.ObjectId);
    }

    internal static SubjectRef Subject(V1.SubjectReference? value, string operation, string field = "subject")
    {
        if (value is null) throw Malformed(operation, $"missing {field}");
        var obj = Object(value.Object, operation, $"{field} object");
        return new(obj, value.OptionalRelation is "" or SubjectRef.DirectRelation ? null : value.OptionalRelation);
    }

    internal static Relationship Relationship(V1.Relationship? value, string operation)
    {
        if (value is null) throw Malformed(operation, "missing relationship");
        var resource = Object(value.Resource, operation, "relationship resource");
        if (string.IsNullOrEmpty(value.Relation)) throw Malformed(operation, "relationship has no relation");
        var subject = Subject(value.Subject, operation, "relationship subject");
        try
        {
            var result = new Relationship(resource, value.Relation, subject);
            if (value.OptionalCaveat is { } caveat) result = result.WithCaveat(caveat.CaveatName, Native.FromStruct(caveat.Context));
            if (value.OptionalExpiresAt is { } time) result = result.WithExpiration(time.ToDateTimeOffset());
            return result;
        }
        catch (InvalidArgumentException ex) { throw Malformed(operation, ex.Message.TrimEnd('.')); }
    }

    internal static RelationshipUpdate Update(V1.RelationshipUpdate value, string operation)
    {
        var mapped = SpiceDBClient.UpdateOperationFromProto(value.Operation);
        return new(mapped, Relationship(value.Relationship, operation));
    }

    /// <summary>Checks an expanded tree: every node names its object and every leaf subject is complete.</summary>
    internal static V1.PermissionRelationshipTree Tree(V1.PermissionRelationshipTree? value, string operation)
    {
        if (value is null) throw Malformed(operation, "missing tree root");
        Object(value.ExpandedObject, operation, "tree node object");
        if (value.TreeTypeCase == V1.PermissionRelationshipTree.TreeTypeOneofCase.Intermediate)
            foreach (var child in value.Intermediate.Children) Tree(child, operation);
        else if (value.TreeTypeCase == V1.PermissionRelationshipTree.TreeTypeOneofCase.Leaf)
            foreach (var subject in value.Leaf.Subjects) Subject(subject, operation, "tree leaf subject");
        return value;
    }

    internal static string Id(string? value, string operation, string field) =>
        string.IsNullOrEmpty(value) ? throw Malformed(operation, $"missing {field}") : value;
}
