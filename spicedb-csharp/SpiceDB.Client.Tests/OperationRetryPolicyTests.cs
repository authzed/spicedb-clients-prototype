using System.Reflection;
using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;
using GrpcStatus = Grpc.Core.Status;
using Relationship = SpiceDB.Client.Relationship;

namespace SpiceDB.Client.Tests;

/// <summary>Client-wide per-operation retry policies (<see cref="ClientOptions.OperationRetryPolicies"/>).</summary>
public class OperationRetryPolicyTests
{
    private static readonly RetryPolicy RetryOnce = new() { MaxRetries = 1, RetryableCodes = [StatusCode.Unavailable], InitialBackoff = TimeSpan.FromMilliseconds(1) };
    private static readonly RetryPolicy RetryThrice = new() { MaxRetries = 3, RetryableCodes = [StatusCode.Unavailable], InitialBackoff = TimeSpan.FromMilliseconds(1) };

    private static SpiceDBClient Client(ClientOptions options, PermissionsService.PermissionsServiceClient? permissions = null, SchemaService.SchemaServiceClient? schema = null, WatchService.WatchServiceClient? watch = null) =>
        new(permissions ?? new Mock<PermissionsService.PermissionsServiceClient>().Object, schema ?? new Mock<SchemaService.SchemaServiceClient>().Object,
            watch ?? new Mock<WatchService.WatchServiceClient>().Object, new Mock<ExperimentalService.ExperimentalServiceClient>().Object, null, options);

    private static Dictionary<SpiceDBOperation, RetryPolicy> Policy(SpiceDBOperation operation, RetryPolicy policy) => new() { [operation] = policy };

    private static AsyncUnaryCall<T> Failing<T>() =>
        new(Task.FromException<T>(new RpcException(new GrpcStatus(StatusCode.Unavailable, "transient"))), Task.FromResult(new Metadata()), () => new GrpcStatus(StatusCode.Unavailable, "transient"), () => new Metadata(), () => { });

    private static (Mock<WatchService.WatchServiceClient> Mock, StreamCallScript<WatchResponse> Script) FailingThenWorkingWatch()
    {
        var script = new StreamCallScript<WatchResponse>().Then([], UnavailableError()).Then([new WatchResponse { ChangesThrough = new() { Token = "t" } }]);
        var watch = new Mock<WatchService.WatchServiceClient>();
        watch.Setup(w => w.Watch(It.IsAny<WatchRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>())).Returns(() => script.Next());
        return (watch, script);
    }

    [Fact]
    public async Task ZeroRetryWatchPolicyNeverReopensAWatchWithoutAPerCallOption()
    {
        var (watch, script) = FailingThenWorkingWatch();
        var reads = 0;
        var schema = new Mock<SchemaService.SchemaServiceClient>();
        schema.Setup(s => s.ReadSchemaAsync(It.IsAny<ReadSchemaRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => { reads++; return Failing<ReadSchemaResponse>(); });
        await using var client = Client(new ClientOptions { RetryPolicy = RetryThrice, OperationRetryPolicies = Policy(SpiceDBOperation.Updates, RetryPolicy.None) }, schema: schema.Object, watch: watch.Object);

        await Assert.ThrowsAsync<UnavailableException>(async () => { await foreach (var _ in client.UpdatesAsync([], null)) { } });
        script.CallCount.Should().Be(1, "the watch is opened once");

        // ...while another operation keeps the client-wide policy.
        await Assert.ThrowsAsync<UnavailableException>(() => client.ReadSchemaAsync());
        reads.Should().Be(4);
    }

    [Fact]
    public async Task PerCallPolicyWinsOverTheOperationPolicy()
    {
        var (watch, script) = FailingThenWorkingWatch();
        await using var client = Client(new ClientOptions { OperationRetryPolicies = Policy(SpiceDBOperation.Updates, RetryPolicy.None) }, watch: watch.Object);
        await foreach (var e in client.UpdatesAsync([], null, new WatchOptions { RetryPolicy = RetryOnce }))
        {
            e.ChangesThrough.Value.Should().Be("t");
            break;
        }
        script.CallCount.Should().Be(2);
    }

    [Fact]
    public async Task OperationPolicyGovernsStreamEstablishmentWhenStreamRetriesAreDisabled()
    {
        var (watch, script) = FailingThenWorkingWatch();
        await using var client = Client(new ClientOptions { StreamRetries = StreamRetries.Disabled, OperationRetryPolicies = Policy(SpiceDBOperation.Updates, RetryOnce) }, watch: watch.Object);
        await foreach (var e in client.UpdatesAsync([], null))
        {
            e.ChangesThrough.Value.Should().Be("t");
            break;
        }
        script.CallCount.Should().Be(2);
    }

    [Fact]
    public async Task WritePolicyRetriesOnlyIdempotentWritesAndLeavesOthersUnretried()
    {
        static (Mock<PermissionsService.PermissionsServiceClient>, Func<int>) FailFirst()
        {
            var writes = 0;
            var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
            permissions.Setup(p => p.WriteRelationshipsAsync(It.IsAny<WriteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
                .Returns(() => ++writes == 1 ? Failing<WriteRelationshipsResponse>() : MakeUnaryCall(new WriteRelationshipsResponse { WrittenAt = new() { Token = "w" } }));
            return (permissions, () => writes);
        }
        var options = new ClientOptions { OperationRetryPolicies = Policy(SpiceDBOperation.Write, RetryOnce) };
        var touch = new Transaction(); touch.Touch(Relationship.FromTuple("document:a#viewer@user:alice"));
        var create = new Transaction(); create.Create(Relationship.FromTuple("document:a#viewer@user:alice"));

        var (permissions, writes) = FailFirst();
        await using (var client = Client(options, permissions.Object))
            (await client.WriteAsync(touch)).Revision.Value.Should().Be("w");
        writes().Should().Be(2, "the touch-only write was retried");

        (permissions, writes) = FailFirst();
        await using (var client = Client(options, permissions.Object))
            await Assert.ThrowsAsync<UnavailableException>(() => client.WriteAsync(create));
        writes().Should().Be(1, "a create is not retried, and not rejected");

        // A per-call None still exempts an idempotent write.
        (permissions, writes) = FailFirst();
        await using (var client = Client(options, permissions.Object))
            await Assert.ThrowsAsync<UnavailableException>(() => client.WriteAsync(touch, new WriteOptions { RetryPolicy = RetryPolicy.None }));
        writes().Should().Be(1);
    }

    [Fact]
    public async Task SchemaWritePolicyRetries()
    {
        var calls = 0;
        var schema = new Mock<SchemaService.SchemaServiceClient>();
        schema.Setup(s => s.WriteSchemaAsync(It.IsAny<WriteSchemaRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => ++calls == 1 ? Failing<WriteSchemaResponse>() : MakeUnaryCall(new WriteSchemaResponse { WrittenAt = new() { Token = "s" } }));
        await using var client = Client(new ClientOptions { OperationRetryPolicies = Policy(SpiceDBOperation.WriteSchema, RetryOnce) }, schema: schema.Object);
        (await client.WriteSchemaAsync("definition user {}")).Value.Should().Be("s");
        calls.Should().Be(2);
    }

    [Theory]
    [InlineData(SpiceDBOperation.DeleteRelationships)]
    [InlineData(SpiceDBOperation.DeleteAllRelationshipsNonAtomic)]
    [InlineData(SpiceDBOperation.ImportRelationships)]
    [InlineData(SpiceDBOperation.ExperimentalRegisterRelationshipCounter)]
    [InlineData(SpiceDBOperation.ExperimentalUnregisterRelationshipCounter)]
    public void NeverIdempotentOperationsRejectARetryingPolicyWhenTheClientIsBuilt(SpiceDBOperation operation)
    {
        var retrying = new ClientOptions { OperationRetryPolicies = Policy(operation, RetryOnce) };
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreatePlaintext("localhost:50051", "key", retrying))
            .Message.Should().Contain(operation.ToString());
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreateFromCallInvoker(new Mock<CallInvoker>().Object, retrying));
        Assert.Throws<InvalidArgumentException>(() => Client(retrying));

        // A zero-retry policy is allowed for any operation.
        var none = new ClientOptions { OperationRetryPolicies = Policy(operation, RetryPolicy.None) };
        SpiceDBClient.CreateFromCallInvoker(new Mock<CallInvoker>().Object, none).Should().NotBeNull();
    }

    [Fact]
    public void InvalidOperationPoliciesAreRejected()
    {
        var invoker = new Mock<CallInvoker>().Object;
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreateFromCallInvoker(invoker, new ClientOptions { OperationRetryPolicies = Policy(SpiceDBOperation.Check, new RetryPolicy { MaxRetries = -1 }) }));
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreateFromCallInvoker(invoker, new ClientOptions { OperationRetryPolicies = Policy((SpiceDBOperation)999, RetryPolicy.None) }));
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreateFromCallInvoker(invoker, new ClientOptions { OperationRetryPolicies = Policy(SpiceDBOperation.Check, null!) }));
        Assert.Throws<InvalidArgumentException>(() => new ClientOptions { OperationRetryPolicies = null! });
    }

    [Fact]
    public void PoliciesAreSnapshotted()
    {
        var policies = Policy(SpiceDBOperation.Check, RetryPolicy.None);
        var options = new ClientOptions { OperationRetryPolicies = policies };
        policies[SpiceDBOperation.DeleteRelationships] = RetryOnce;
        options.OperationRetryPolicies.Should().ContainSingle();
    }

    /// <summary>The client looks policies up by method name, so every operation must name a real method and every operation method must have an operation.</summary>
    [Fact]
    public void EveryOperationNamesAClientMethod()
    {
        var methods = typeof(SpiceDBClient).GetMethods(BindingFlags.Public | BindingFlags.Instance | BindingFlags.DeclaredOnly)
            .Select(m => m.Name).Where(n => n.EndsWith("Async", StringComparison.Ordinal) && n is not ("DisposeAsync" or "CheckAnyAsync" or "CheckAllAsync"))
            .ToHashSet();
        methods.Should().BeEquivalentTo(Enum.GetNames<SpiceDBOperation>().Select(n => n + "Async"));
    }
}
