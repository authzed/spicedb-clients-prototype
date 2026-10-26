namespace SpiceDB.Client;

public sealed record SchemaResult(string Schema, ZedToken Revision);
public sealed record ComputablePermissionsResult
{
    public IReadOnlyList<RelationReference> Permissions { get; }
    public ZedToken Revision { get; }
    public ComputablePermissionsResult(IEnumerable<RelationReference> permissions, ZedToken revision) { Permissions = Native.List(permissions); Revision = revision; }
}
public sealed record DependentRelationsResult
{
    public IReadOnlyList<RelationReference> Relations { get; }
    public ZedToken Revision { get; }
    public DependentRelationsResult(IEnumerable<RelationReference> relations, ZedToken revision) { Relations = Native.List(relations); Revision = revision; }
}
public sealed record DiffSchemaResult
{
    public IReadOnlyList<SchemaDiff> Diffs { get; }
    public ZedToken Revision { get; }
    public DiffSchemaResult(IEnumerable<SchemaDiff> diffs, ZedToken revision) { Diffs = Native.List(diffs); Revision = revision; }
}
