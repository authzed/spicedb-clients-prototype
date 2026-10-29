using System.Diagnostics;
using System.Reflection;
using Google.Protobuf;
using Google.Protobuf.Reflection;
using Xunit;

namespace Authzed.Api.SpiceDB.Proto.Tests;

/// <summary>
/// Every generated file's descriptor must initialise: <c>ToString()</c>,
/// <c>JsonFormatter</c> and <c>.Descriptor</c> all go through it, and a descriptor
/// type initialiser that throws once throws for the life of the process. The generated
/// descriptors depend on transitive protos (protovalidate, protoc-gen-validate, openapiv2)
/// that this assembly stubs; each stub must carry its real file name, or the stubs collide
/// in the dependent file's descriptor pool.
/// </summary>
public class DescriptorInitializationTest
{
    private static readonly Assembly Proto = typeof(Authzed.Api.V1.Relationship).Assembly;

    private static string Metadata(string key) =>
        typeof(DescriptorInitializationTest).Assembly.GetCustomAttributes<AssemblyMetadataAttribute>().Single(a => a.Key == key).Value!;

    /// <summary>The generated <c>*Reflection</c> holder of every generated file.</summary>
    private static string[] ReflectionClassNames() =>
        Proto.GetExportedTypes()
            .Where(t => t.Namespace is "Authzed.Api.V1" or "Authzed.Api.Materialize.V0"
                        && t.Name.EndsWith("Reflection", StringComparison.Ordinal)
                        && t.GetProperty("Descriptor", BindingFlags.Public | BindingFlags.Static)?.PropertyType == typeof(FileDescriptor))
            .Select(t => t.FullName!)
            .OrderBy(n => n, StringComparer.Ordinal)
            .ToArray();

    public static TheoryData<string> ReflectionClasses() => new(ReflectionClassNames());

    [Theory]
    [MemberData(nameof(ReflectionClasses))]
    public void EveryGeneratedFileDescriptorInitialises(string reflectionClass)
    {
        var holder = Proto.GetType(reflectionClass, throwOnError: true)!;
        var file = (FileDescriptor)holder.GetProperty("Descriptor", BindingFlags.Public | BindingFlags.Static)!.GetValue(null)!;
        Assert.False(string.IsNullOrEmpty(file.Name));
        Assert.All(file.Dependencies, d => Assert.False(string.IsNullOrEmpty(d.Name), $"{file.Name} has a dependency with no file name"));

        foreach (var message in file.MessageTypes.Where(m => m.ClrType is not null))
        {
            var instance = (IMessage)Activator.CreateInstance(message.ClrType)!;
            Assert.Same(message, instance.Descriptor);
            Assert.NotNull(instance.ToString());
            Assert.StartsWith("{", JsonFormatter.Default.Format(instance));
        }
    }

    [Fact]
    public void EveryGeneratedFileIsCovered()
    {
        // A file with no message types (enums only, options only) still has a holder;
        // make sure the theory above visits the files that carry the RPC messages.
        var names = ReflectionClassNames();
        Assert.Contains("Authzed.Api.V1.CoreReflection", names);
        Assert.Contains("Authzed.Api.V1.PermissionServiceReflection", names);
        Assert.Contains("Authzed.Api.V1.OpenapiReflection", names);
        Assert.Contains("Authzed.Api.Materialize.V0.RoaringlookupresourcesReflection", names);
        Assert.True(names.Length >= 12, $"expected every generated file, found {names.Length}");
    }

    [Fact]
    public void CommonMessagesFormatAsJson()
    {
        var request = new Authzed.Api.V1.CheckPermissionRequest
        {
            Resource = new Authzed.Api.V1.ObjectReference { ObjectType = "document", ObjectId = "readme" },
            Permission = "view",
        };
        Assert.Contains("\"permission\": \"view\"", JsonFormatter.Default.Format(request));
        Assert.Contains("readme", request.ToString());
        Assert.Equal("authzed.api.v1.CheckPermissionResponse", Authzed.Api.V1.CheckPermissionResponse.Descriptor.FullName);
    }

    [Fact]
    public void LegacyBridgeMessagesFormatAsJson()
    {
        // Legacy code that logs proto messages reaches them through the Authzed.Net names.
        var type = Type.GetType("Authzed.Api.V1.CheckPermissionResponse, Authzed.Net", throwOnError: true)!;
        var message = (IMessage)Activator.CreateInstance(type)!;
        Assert.Equal("authzed.api.v1.CheckPermissionResponse", message.Descriptor.FullName);
        Assert.NotNull(message.ToString());
        Assert.StartsWith("{", JsonFormatter.Default.Format(message));
    }

    [Fact]
    public async Task DescriptorsInitialiseInAFreshProcess()
    {
        // The in-process tests above can be masked by whichever test touched a descriptor
        // first; this runs a separate program whose first protobuf use is the descriptor.
        var probe = Metadata("DescriptorProbe");
        Assert.True(File.Exists(probe), $"descriptor probe not built at {probe}");
        var psi = new ProcessStartInfo("dotnet") { RedirectStandardOutput = true, RedirectStandardError = true };
        psi.ArgumentList.Add(probe);
        using var process = Process.Start(psi)!;
        var stdout = process.StandardOutput.ReadToEndAsync();
        var stderr = process.StandardError.ReadToEndAsync();
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(60));
        await process.WaitForExitAsync(timeout.Token);
        var output = await stdout;
        var errors = await stderr;
        Assert.True(process.ExitCode == 0, $"descriptor probe failed ({process.ExitCode}):\n{output}\n{errors}");
        Assert.Contains("direct: ok", output);
        Assert.Contains("bridge: ok", output);
    }
}
