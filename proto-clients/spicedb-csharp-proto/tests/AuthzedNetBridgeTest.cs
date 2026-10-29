using System.Reflection;
using System.Runtime.InteropServices;
using Xunit;

namespace Authzed.Api.SpiceDB.Proto.Tests;

/// <summary>
/// The proto tier is the single copy of the generated authzed.api types in a process (root
/// DESIGN.md, "Be the single copy of the protocol bindings in any process"). The legacy
/// Authzed.Net package defines the same type names in its own assembly, so a project that
/// referenced both failed to compile (CS0433) and two copies of every message could load.
/// The bridge is an Authzed.Net assembly that forwards those types here; these tests load
/// both assemblies in one process and prove there is one set of types.
/// </summary>
public class AuthzedNetBridgeTest
{
    private static readonly Assembly Proto = typeof(Authzed.Api.V1.Relationship).Assembly;
    private static readonly string[] Namespaces = ["Authzed.Api.V1", "Authzed.Api.Materialize.V0"];

    private static string Metadata(string key) =>
        typeof(AuthzedNetBridgeTest).Assembly.GetCustomAttributes<AssemblyMetadataAttribute>().Single(a => a.Key == key).Value!;

    private static IEnumerable<Type> ProtoTypes() =>
        Proto.GetExportedTypes().Where(t => !t.IsNested && Namespaces.Contains(t.Namespace));

    [Fact]
    public void BridgeForwardsEveryGeneratedTypeToTheProtoTier()
    {
        var bridge = Assembly.Load("Authzed.Net");
        Assert.Equal(new Version(2, 0, 0, 0), bridge.GetName().Version);
        var forwarded = bridge.GetForwardedTypes().Where(t => !t.IsNested).Select(t => t.FullName).ToHashSet();
        var missing = ProtoTypes().Select(t => t.FullName).Where(n => !forwarded.Contains(n)).ToArray();
        Assert.True(missing.Length == 0, "Stale bridge; run `mage bridge`. Missing forwards: " + string.Join(", ", missing));
        Assert.All(bridge.GetForwardedTypes(), t => Assert.Same(Proto, t.Assembly));
        Assert.DoesNotContain(bridge.GetExportedTypes(), t => Namespaces.Contains(t.Namespace));
    }

    [Fact]
    public void LegacyCompiledReferencesResolveToTheProtoTierTypes()
    {
        // A binary compiled against Authzed.Net records "Authzed.Api.V1.X, Authzed.Net";
        // the runtime follows the forwarder, so legacy code and SpiceDB.Client exchange the
        // very same message objects without conversion.
        foreach (var name in new[] { "Authzed.Api.V1.CheckPermissionRequest", "Authzed.Api.V1.PermissionsService+PermissionsServiceClient", "Authzed.Api.V1.CheckPermissionResponse+Types+Permissionship", "Authzed.Api.Materialize.V0.WatchPermissionSetsService" })
        {
            var legacy = Type.GetType($"{name}, Authzed.Net", throwOnError: true)!;
            Assert.Same(Proto.GetType(name, throwOnError: true), legacy);
        }
        var message = (Google.Protobuf.IMessage)Activator.CreateInstance(Type.GetType("Authzed.Api.V1.ObjectReference, Authzed.Net")!)!;
        Assert.IsType<Authzed.Api.V1.ObjectReference>(message);
    }

    [Fact]
    public void BridgeCoversEveryTypeTheLegacyPackageShipsInTheSharedNamespaces()
    {
        // Compare against the real Authzed.Net 1.6.0 so a legacy binary never finds a type
        // missing. Inspected with MetadataLoadContext, never loaded into this process.
        var legacyPath = Metadata("LegacyAuthzedNet");
        var runtime = Directory.GetFiles(RuntimeEnvironment.GetRuntimeDirectory(), "*.dll");
        var deps = new[] { typeof(Google.Protobuf.IMessage).Assembly.Location, typeof(Grpc.Core.CallInvoker).Assembly.Location, typeof(Google.Rpc.Status).Assembly.Location };
        using var context = new MetadataLoadContext(new PathAssemblyResolver(runtime.Concat(deps).Append(legacyPath)));
        var legacy = context.LoadFromAssemblyPath(legacyPath).GetExportedTypes()
            .Where(t => !t.IsNested && Namespaces.Contains(t.Namespace)).Select(t => t.FullName!).ToHashSet();
        var forwarded = Assembly.Load("Authzed.Net").GetForwardedTypes().Select(t => t.FullName!).ToHashSet();
        Assert.True(legacy.Count > 100, $"expected the legacy package's generated types, found {legacy.Count}");
        var uncovered = legacy.Where(n => !forwarded.Contains(n)).OrderBy(n => n).ToArray();
        Assert.True(uncovered.Length == 0, "Types Authzed.Net 1.6.0 ships that the bridge does not forward: " + string.Join(", ", uncovered));
    }

    [Fact]
    public void CheckedInForwardsMatchTheGenerator()
    {
        var path = Metadata("BridgeForwards");
        var lines = File.ReadAllLines(path).Where(l => l.StartsWith("[assembly: TypeForwardedTo(", StringComparison.Ordinal)).ToArray();
        var expected = ProtoTypes().Select(t => t.FullName).OrderBy(n => n, StringComparer.Ordinal)
            .Select(n => $"[assembly: TypeForwardedTo(typeof(global::{n}))]").ToArray();
        Assert.Equal(expected, lines);
    }
}
