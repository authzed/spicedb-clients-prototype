using System.Text.Json;
using FluentAssertions;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

/// <summary>
/// A <see cref="Consistency"/> is observable: <c>ToString</c> names the strategy and token, and
/// <c>System.Text.Json</c> writes a structured form, never <c>Consistency { }</c> or <c>{}</c>
/// (root DESIGN.md, "Make native values observable and testable without the server").
/// </summary>
public class ConsistencyFormTests
{
    private static readonly ZedToken Token = new("GhUKEzE2");

    public static TheoryData<Consistency, string, string> Forms => new()
    {
        { Consistency.Full(), "Full", """{"strategy":"full"}""" },
        { Consistency.MinLatency(), "MinLatency", """{"strategy":"minLatency"}""" },
        { Consistency.AtLeast(Token), "AtLeast(GhUKEzE2)", """{"strategy":"atLeast","token":"GhUKEzE2"}""" },
        { Consistency.Snapshot(Token), "Snapshot(GhUKEzE2)", """{"strategy":"snapshot","token":"GhUKEzE2"}""" },
        // The fallback helpers return the strategy they chose.
        { Consistency.AtLeastOrFull(Token), "AtLeast(GhUKEzE2)", """{"strategy":"atLeast","token":"GhUKEzE2"}""" },
        { Consistency.AtLeastOrFull(null), "Full", """{"strategy":"full"}""" },
        { Consistency.AtLeastOrMinLatency(new ZedToken(" ")), "MinLatency", """{"strategy":"minLatency"}""" },
    };

    [Theory]
    [MemberData(nameof(Forms))]
    public void ToStringAndJsonNameTheStrategy(Consistency consistency, string text, string json)
    {
        consistency.ToString().Should().Be(text);
        $"{consistency}".Should().Be(text);
        JsonSerializer.Serialize(consistency).Should().Be(json);
        JsonSerializer.Deserialize<Consistency>(json).Should().Be(consistency);
    }

    [Fact]
    public void NestedInAValueItSerializesStructured()
    {
        var check = new PermissionCheck(new ObjectRef("document", "1"), "view", new ObjectRef("user", "alice"), Consistency.AtLeast(Token));
        JsonSerializer.Serialize(check).Should().Contain("""
            "Consistency":{"strategy":"atLeast","token":"GhUKEzE2"}
            """.Trim());
    }

    [Theory]
    [InlineData("""{"strategy":"atLeast"}""")]
    [InlineData("""{"strategy":"atLeast","token":" "}""")]
    [InlineData("""{"strategy":"full","token":"GhUKEzE2"}""")]
    [InlineData("""{"strategy":"eventual"}""")]
    [InlineData("""{"token":"GhUKEzE2"}""")]
    [InlineData("""{"strategy":"full","extra":1}""")]
    [InlineData("\"full\"")]
    public void MalformedJsonIsRejected(string json)
    {
        var act = () => JsonSerializer.Deserialize<Consistency>(json);
        act.Should().Throw<JsonException>();
    }
}
