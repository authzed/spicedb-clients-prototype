using System.Collections;
using System.Collections.ObjectModel;
using Google.Protobuf.WellKnownTypes;
namespace SpiceDB.Client;

public sealed record ObjectRef
{
    public string ObjectType { get; }
    public string ObjectId { get; }
    public ObjectRef(string objectType, string objectId)
    { ObjectType = Native.Required(objectType, nameof(objectType)); ObjectId = Native.Required(objectId, nameof(objectId)); }
    /// <summary>Formats the object as <c>type:id</c>.</summary>
    public override string ToString() => $"{ObjectType}:{ObjectId}";
    internal Authzed.Api.V1.ObjectReference ToProto() => new() { ObjectType = ObjectType, ObjectId = ObjectId };
    internal static ObjectRef FromProto(Authzed.Api.V1.ObjectReference value) => new(value.ObjectType, value.ObjectId);
}
public sealed record SubjectRef
{
    public ObjectRef Object { get; }
    public string? Relation { get; }
    /// <summary>
    /// A subject: <paramref name="object"/> itself when <paramref name="relation"/> is null or
    /// <c>...</c> (SpiceDB's direct-subject relation), otherwise the subject set
    /// <c>object#relation</c>.
    /// </summary>
    public SubjectRef(ObjectRef @object, string? relation = null)
    {
        Object = @object ?? throw new InvalidArgumentException("Subject object is required.");
        if (relation is "") throw new InvalidArgumentException("Use null or \"...\" for a direct subject relation.");
        Relation = relation is DirectRelation ? null : relation;
    }

    /// <summary>The subject relation SpiceDB uses for a direct subject.</summary>
    internal const string DirectRelation = "...";

    public static SubjectRef Direct(ObjectRef value) => new(value);

    /// <summary>The subject set <c>value#relation</c>; <c>...</c> gives the direct subject, as tuple and proto parsing do.</summary>
    public static SubjectRef Named(ObjectRef value, string relation) => new(value, Native.Required(relation, nameof(relation)));

    /// <summary>The direct subject for <paramref name="value"/>, so an object can be passed wherever a subject is expected.</summary>
    public static implicit operator SubjectRef(ObjectRef value) => value is null ? null! : Direct(value);
    /// <summary>Formats the subject as <c>type:id</c> or <c>type:id#relation</c>.</summary>
    public override string ToString() => Relation is null ? Object.ToString() : $"{Object}#{Relation}";
    internal Authzed.Api.V1.SubjectReference ToProto() => new() { Object = Object.ToProto(), OptionalRelation = Relation ?? "" };
    internal static SubjectRef FromProto(Authzed.Api.V1.SubjectReference value) => new(ObjectRef.FromProto(value.Object), value.OptionalRelation is "" or "..." ? null : value.OptionalRelation);
}
public sealed class PermissionCheck
{
    public ObjectRef Resource { get; }
    public string Permission { get; }
    public SubjectRef Subject { get; }
    public Consistency Consistency { get; }
    public IReadOnlyDictionary<string, object?>? Context { get; }
    public PermissionCheck(ObjectRef resource, string permission, SubjectRef subject, Consistency consistency, IReadOnlyDictionary<string, object?>? context = null)
    {
        Resource = resource ?? throw new InvalidArgumentException("Resource is required.");
        Permission = Native.Required(permission, nameof(permission));
        Subject = subject ?? throw new InvalidArgumentException("Subject is required.");
        Consistency = consistency ?? throw new InvalidArgumentException("Consistency is required.");
        Context = Native.Context(context);
    }
    /// <summary>Creates a mutable builder for an immutable permission check.</summary>
    public static Builder CreateBuilder() => new();

    public sealed class Builder
    {
        private ObjectRef? _resource;
        private string? _permission;
        private SubjectRef? _subject;
        private Consistency? _consistency;
        private IReadOnlyDictionary<string, object?>? _context;

        internal Builder() { }

        public Builder WithResource(ObjectRef value) { _resource = value; return this; }
        public Builder WithPermission(string value) { _permission = value; return this; }
        public Builder WithSubject(SubjectRef value) { _subject = value; return this; }
        public Builder WithConsistency(Consistency value) { _consistency = value; return this; }
        public Builder WithContext(IReadOnlyDictionary<string, object?>? value)
        { _context = Native.Context(value); return this; }

        /// <summary>Validates required fields and builds an independent immutable request.</summary>
        public PermissionCheck Build() => new(
            Native.Required(_resource, nameof(Resource)),
            Native.Required(_permission, nameof(Permission)),
            Native.Required(_subject, nameof(Subject)),
            Native.Required(_consistency, nameof(Consistency)), _context);
    }
    public PermissionCheck WithContext(IReadOnlyDictionary<string, object?>? context) => new(Resource, Permission, Subject, Consistency, context);
}
internal static class Native
{
    internal static T Required<T>(T? value, string path) where T : class => value ?? throw new InvalidArgumentException($"{path} is required.");
    internal static string Required(string? value, string path) => string.IsNullOrEmpty(value) ? throw new InvalidArgumentException($"{path} must not be empty.") : value;
    /// <summary>
    /// A credential that must hold something: an empty or whitespace-only key (usually an
    /// unset environment variable) would otherwise be sent as a bare "Bearer" header the
    /// server rejects on the first call.
    /// </summary>
    internal static string Credential(string? value, string path) => string.IsNullOrWhiteSpace(value)
        ? throw new InvalidArgumentException($"{path} must not be empty or whitespace (check that the variable holding it is set).")
        : value;
    internal static IReadOnlyList<T> List<T>(IEnumerable<T> values) => Array.AsReadOnly((values ?? throw new InvalidArgumentException("Collection cannot be null.")).ToArray());
    internal static IReadOnlyDictionary<string, object?>? Context(IReadOnlyDictionary<string, object?>? value) => value is null ? null : (IReadOnlyDictionary<string, object?>)Snapshot(value, "context", new HashSet<object>(ReferenceEqualityComparer.Instance))!;
    private static object? Snapshot(object? value, string path, HashSet<object> visiting)
    {
        if (value is null or string or bool) return value;
        if (value is byte or sbyte or short or ushort or int or uint or long or ulong or float or double or decimal)
        {
            double number = Convert.ToDouble(value, System.Globalization.CultureInfo.InvariantCulture);
            if (!double.IsFinite(number) || (value is decimal && (number >= (double)decimal.MaxValue || number <= (double)decimal.MinValue)) || (value is long l && (decimal)number != l) || (value is ulong u && (decimal)number != u) || (value is decimal d && (decimal)number != d))
                throw new InvalidArgumentException($"{path}: number cannot be represented losslessly as JSON.");
            return number;
        }
        if (!visiting.Add(value)) throw new InvalidArgumentException($"{path}: cyclic context.");
        try
        {
            var result = new Dictionary<string, object?>();
            if (value is IReadOnlyDictionary<string, object?> readOnly)
            {
                foreach (var pair in readOnly) result[pair.Key] = Snapshot(pair.Value, path + "." + pair.Key, visiting);
                return new ReadOnlyDictionary<string, object?>(result);
            }
            if (value is IDictionary dictionary)
            {
                foreach (DictionaryEntry pair in dictionary)
                {
                    if (pair.Key is not string key) throw new InvalidArgumentException($"{path}: keys must be strings.");
                    result[key] = Snapshot(pair.Value, path + "." + key, visiting);
                }
                return new ReadOnlyDictionary<string, object?>(result);
            }
            if (value is IEnumerable items)
            {
                var list = new List<object?>();
                foreach (var item in items) list.Add(Snapshot(item, $"{path}[{list.Count}]", visiting));
                return list.AsReadOnly();
            }
            throw new InvalidArgumentException($"{path}: unsupported value {value.GetType().Name}.");
        }
        finally { visiting.Remove(value); }
    }
    internal static Struct? Struct(IReadOnlyDictionary<string, object?>? context)
    {
        if (context is null) return null;
        var result = new Struct();
        foreach (var pair in context) result.Fields[pair.Key] = Value(pair.Value);
        return result;
    }
    internal static Value Value(object? value) => value switch
    {
        null => Google.Protobuf.WellKnownTypes.Value.ForNull(),
        string s => Google.Protobuf.WellKnownTypes.Value.ForString(s),
        bool b => Google.Protobuf.WellKnownTypes.Value.ForBool(b),
        double n => Google.Protobuf.WellKnownTypes.Value.ForNumber(n),
        IReadOnlyDictionary<string, object?> map => Google.Protobuf.WellKnownTypes.Value.ForStruct(Struct(map)),
        IEnumerable<object?> list => Google.Protobuf.WellKnownTypes.Value.ForList(list.Select(Value).ToArray()),
        _ => Value(Snapshot(value, "context", new HashSet<object>(ReferenceEqualityComparer.Instance))),
    };
    internal static object? FromValue(Value value) => value.KindCase switch
    {
        Google.Protobuf.WellKnownTypes.Value.KindOneofCase.StringValue => value.StringValue,
        Google.Protobuf.WellKnownTypes.Value.KindOneofCase.BoolValue => value.BoolValue,
        Google.Protobuf.WellKnownTypes.Value.KindOneofCase.NumberValue => value.NumberValue,
        Google.Protobuf.WellKnownTypes.Value.KindOneofCase.StructValue => FromStruct(value.StructValue),
        Google.Protobuf.WellKnownTypes.Value.KindOneofCase.ListValue => List(value.ListValue.Values.Select(FromValue)),
        _ => null,
    };
    internal static IReadOnlyDictionary<string, object?>? FromStruct(Struct? value) => value is null ? null : new ReadOnlyDictionary<string, object?>(value.Fields.ToDictionary(p => p.Key, p => FromValue(p.Value)));
}
