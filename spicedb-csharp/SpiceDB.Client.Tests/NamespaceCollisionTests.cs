using System.Reflection;
using Xunit;
using Xunit.Abstractions;

namespace SpiceDB.Client.Tests;

/// <summary>
/// Finds, by reflection, every public type name SpiceDB.Client shares with Grpc.Core or
/// Authzed.Api.V1. A file importing both namespaces fails with CS0104 wherever it writes one of
/// these names, so the AI guide and MIGRATION.md list them all with an alias for each. A new
/// collision fails here until the docs and <c>CallOptionsAliasTests</c> cover it.
/// </summary>
public class NamespaceCollisionTests(ITestOutputHelper output)
{
    /// <summary>The names the documented alias block covers, per colliding namespace.</summary>
    public static readonly string[] DocumentedGrpcCore = ["CallOptions", "WriteOptions"];

    public static readonly string[] DocumentedAuthzedApiV1 =
    [
        "CheckDebugTrace", "Consistency", "DebugInformation", "PartialCaveatInfo", "Precondition",
        "Relationship", "RelationshipUpdate", "ResolvedSubject", "ZedToken",
    ];

    private static SortedSet<string> PublicTopLevelNames(Assembly assembly, string ns) =>
        new(assembly.GetExportedTypes()
            .Where(t => t.Namespace == ns && !t.IsNested)
            .Select(t => t.Name.Split('`')[0]), StringComparer.Ordinal);

    private SortedSet<string> SharedWith(Assembly assembly, string ns)
    {
        var client = PublicTopLevelNames(typeof(SpiceDBClient).Assembly, "SpiceDB.Client");
        var shared = new SortedSet<string>(client.Intersect(PublicTopLevelNames(assembly, ns)), StringComparer.Ordinal);
        output.WriteLine($"{ns}: {string.Join(", ", shared)}");
        return shared;
    }

    [Fact]
    public void GrpcCoreCollisionsAreTheDocumentedOnes() =>
        Assert.Equal(DocumentedGrpcCore.Order(StringComparer.Ordinal), SharedWith(typeof(Grpc.Core.CallOptions).Assembly, "Grpc.Core"));

    [Fact]
    public void AuthzedApiV1CollisionsAreTheDocumentedOnes() =>
        Assert.Equal(DocumentedAuthzedApiV1.Order(StringComparer.Ordinal), SharedWith(typeof(Authzed.Api.V1.Relationship).Assembly, "Authzed.Api.V1"));

    /// <summary>The guides' alias block names every collision, so a reader copying it is covered.</summary>
    [Theory]
    [InlineData("AI_INTEGRATION_GUIDE.md")]
    [InlineData("MIGRATION.md")]
    public void GuidesAliasEveryCollision(string guide)
    {
        var text = File.ReadAllText(Path.Combine(RepoDir(), guide));
        foreach (var name in DocumentedGrpcCore.Concat(DocumentedAuthzedApiV1))
            Assert.Contains($"using {name} = SpiceDB.Client.{name};", text);
        foreach (var name in DocumentedGrpcCore)
            Assert.Contains($"= Grpc.Core.{name};", text);
        foreach (var name in DocumentedAuthzedApiV1)
            Assert.Contains($"= Authzed.Api.V1.{name};", text);
    }

    private static string RepoDir()
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
            if (File.Exists(Path.Combine(dir.FullName, "MIGRATION.md")) && Directory.Exists(Path.Combine(dir.FullName, "SpiceDB.Client")))
                return dir.FullName;
        throw new InvalidOperationException("spicedb-csharp directory not found");
    }
}
