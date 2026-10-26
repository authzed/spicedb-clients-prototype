using Xunit;
namespace SpiceDB.Client.Tests;

public class ValueOptionTests
{
    [Theory]
    [InlineData(typeof(CheckOptions), "Tracing")]
    [InlineData(typeof(LookupOptions), "Debug")]
    [InlineData(typeof(ClientOptions), "RemotePlaintextCredentials")]
    [InlineData(typeof(WatchOptions), "Content")]
    public void BehavioralOptionsAreValidatedEnums(Type optionsType, string propertyName)
    {
        var property = optionsType.GetProperty(propertyName);
        Assert.NotNull(property);
        Assert.True(property.PropertyType.IsEnum);
        var options = Activator.CreateInstance(optionsType);
        var error = Assert.Throws<System.Reflection.TargetInvocationException>(() => property.SetValue(options, Enum.ToObject(property.PropertyType, 999)));
        Assert.IsType<InvalidArgumentException>(error.InnerException);
    }

    [Theory]
    [InlineData("  opaque+/=\n雪\0token  ")]
    [InlineData("")]
    public void TokensRoundTripLosslessly(string text)
    {
        var token = new ZedToken(text);
        Assert.Equal(text, token.Value);
        Assert.Equal(text, token.ToString());
        Assert.Equal(token, new ZedToken(token.ToString()));
        if (text.Length > 0)
        {
            Assert.Equal(text, Consistency.AtLeast(token).ToProto().AtLeastAsFresh.Token);
            Assert.Equal(text, Consistency.Snapshot(token).ToProto().AtExactSnapshot.Token);
        }
    }

    [Theory]
    [InlineData("remote.example:50051")]
    [InlineData("192.0.2.10:50051")]
    [InlineData("[2001:db8::10]:50051")]
    public async Task RemotePlaintextRequiresExplicitPolicy(string endpoint)
    {
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreatePlaintext(endpoint, "secret"));
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreatePlaintext(endpoint, "secret",
            new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Reject }));
        await using var allowed = SpiceDBClient.CreatePlaintext(endpoint, "secret",
            new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow });
    }

    [Fact]
    public void DefaultModesAreSecureAndDiagnosticsAreDisabled()
    {
        Assert.Equal(RemotePlaintextCredentials.Reject, new ClientOptions().RemotePlaintextCredentials);
        Assert.Equal(CheckTracing.Disabled, new CheckOptions().Tracing);
        Assert.Equal(LookupDebug.Disabled, new LookupOptions().Debug);
        Assert.Equal(WatchContent.Relationships, new WatchOptions().Content);
        Assert.Throws<InvalidArgumentException>(() => new WatchOptions { Content = 0 });
    }
}
