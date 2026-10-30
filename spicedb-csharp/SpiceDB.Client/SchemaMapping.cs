using P = Authzed.Api.V1;
using System.Text.Json.Serialization;
namespace SpiceDB.Client;

public sealed record SchemaFilter(string? DefinitionName = null, string? CaveatName = null, string? RelationName = null, string? PermissionName = null)
{
    internal P.ReflectionSchemaFilter ToProto() => new() { OptionalDefinitionNameFilter = DefinitionName ?? "", OptionalCaveatNameFilter = CaveatName ?? "", OptionalRelationNameFilter = RelationName ?? "", OptionalPermissionNameFilter = PermissionName ?? "" };
}
[JsonConverter(typeof(JsonStringEnumConverter<SchemaSubjectKind>))]
public enum SchemaSubjectKind { Unspecified, Terminal, Relation, PublicWildcard }
public sealed record SchemaTypeReference(string SubjectDefinitionName, string? CaveatName, SchemaSubjectKind Kind, bool? Terminal, string? Relation, bool? PublicWildcard)
{
    internal static SchemaTypeReference FromProto(P.ReflectionTypeReference value) => new(value.SubjectDefinitionName, value.OptionalCaveatName == "" ? null : value.OptionalCaveatName,
        value.TyperefCase switch { P.ReflectionTypeReference.TyperefOneofCase.IsTerminalSubject => SchemaSubjectKind.Terminal, P.ReflectionTypeReference.TyperefOneofCase.OptionalRelationName => SchemaSubjectKind.Relation, P.ReflectionTypeReference.TyperefOneofCase.IsPublicWildcard => SchemaSubjectKind.PublicWildcard, _ => SchemaSubjectKind.Unspecified },
        value.HasIsTerminalSubject ? value.IsTerminalSubject : null, value.HasOptionalRelationName ? value.OptionalRelationName : null, value.HasIsPublicWildcard ? value.IsPublicWildcard : null);
}
internal static class SchemaMapping
{
    internal static SchemaDefinition? Definition(P.ReflectionDefinition? v) => v is null ? null : new() { Name = v.Name, Comment = v.Comment, Relations = v.Relations.Select(x => Relation(x)!).ToArray(), Permissions = v.Permissions.Select(x => Permission(x)!).ToArray() };
    internal static SchemaRelation? Relation(P.ReflectionRelation? v) => v is null ? null : new() { Name = v.Name, Comment = v.Comment, ParentDefinitionName = v.ParentDefinitionName, SubjectTypes = v.SubjectTypes.Select(SchemaTypeReference.FromProto).ToArray() };
    internal static SchemaPermission? Permission(P.ReflectionPermission? v) => v is null ? null : new() { Name = v.Name, Comment = v.Comment, ParentDefinitionName = v.ParentDefinitionName };
    internal static SchemaCaveat? Caveat(P.ReflectionCaveat? v) => v is null ? null : new() { Name = v.Name, Comment = v.Comment, Expression = v.Expression, Parameters = v.Parameters.Select(x => Parameter(x)!).ToArray() };
    internal static SchemaCaveatParameter? Parameter(P.ReflectionCaveatParameter? v) => v is null ? null : new() { Name = v.Name, Type = v.Type, ParentCaveatName = v.ParentCaveatName };
}
