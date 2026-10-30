using FluentAssertions;
using Grpc.Net.Client;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

/// <summary>
/// An empty or whitespace-only preshared key (usually an unset environment variable) is a
/// local validation error at construction, never a bare "Bearer" header the server rejects
/// on the first call. Root DESIGN.md, "Reject unusable credentials and trust material at
/// construction".
/// </summary>
public class EmptyPresharedKeyTests
{
    [Theory]
    [InlineData("")]
    [InlineData(" ")]
    [InlineData("\t\n")]
    public void BlankKeyIsRejectedByEveryKeyedFactory(string key)
    {
        const string message = "presharedKey must not be empty or whitespace*";
        FluentActions.Invoking(() => SpiceDBClient.CreatePlaintext("localhost:50116", key))
            .Should().Throw<InvalidArgumentException>().WithMessage(message);
        FluentActions.Invoking(() => SpiceDBClient.CreateSystemTls("localhost:50116", key))
            .Should().Throw<InvalidArgumentException>().WithMessage(message);
        using var channel = GrpcChannel.ForAddress("http://localhost:50116");
        FluentActions.Invoking(() => SpiceDBClient.CreateFromChannel(channel, key))
            .Should().Throw<InvalidArgumentException>().WithMessage(message);
    }
}
