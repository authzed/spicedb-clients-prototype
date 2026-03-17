using FluentAssertions;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class ConsistencyTests
{
    [Fact]
    public void Full_ReturnsFullyConsistentStrategy()
    {
        var strategy = Consistency.Full();

        strategy.Should().NotBeNull();
        strategy.V1Consistency.Should().NotBeNull();
        strategy.V1Consistency.FullyConsistent.Should().BeTrue();
    }

    [Fact]
    public void MinLatency_ReturnsMinimizeLatencyStrategy()
    {
        var strategy = Consistency.MinLatency();

        strategy.Should().NotBeNull();
        strategy.V1Consistency.Should().NotBeNull();
        strategy.V1Consistency.MinimizeLatency.Should().BeTrue();
    }

    [Fact]
    public void AtLeast_ReturnsAtLeastAsFreshStrategy()
    {
        var strategy = Consistency.AtLeast("sometoken123");

        strategy.Should().NotBeNull();
        strategy.V1Consistency.AtLeastAsFresh.Should().NotBeNull();
        strategy.V1Consistency.AtLeastAsFresh.Token.Should().Be("sometoken123");
    }

    [Fact]
    public void AtLeast_ThrowsOnEmptyRevision()
    {
        var act = () => Consistency.AtLeast("");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void AtLeast_ThrowsOnNullRevision()
    {
        var act = () => Consistency.AtLeast(null!);
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void Snapshot_ReturnsExactSnapshotStrategy()
    {
        var strategy = Consistency.Snapshot("rev42");

        strategy.Should().NotBeNull();
        strategy.V1Consistency.AtExactSnapshot.Should().NotBeNull();
        strategy.V1Consistency.AtExactSnapshot.Token.Should().Be("rev42");
    }

    [Fact]
    public void Snapshot_ThrowsOnEmptyRevision()
    {
        var act = () => Consistency.Snapshot("");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void AtLeastOrFull_WithRevision_ReturnsAtLeast()
    {
        var strategy = Consistency.AtLeastOrFull("tok1");

        strategy.V1Consistency.AtLeastAsFresh.Should().NotBeNull();
        strategy.V1Consistency.AtLeastAsFresh.Token.Should().Be("tok1");
    }

    [Fact]
    public void AtLeastOrFull_WithNull_ReturnsFull()
    {
        var strategy = Consistency.AtLeastOrFull(null);

        strategy.V1Consistency.FullyConsistent.Should().BeTrue();
    }

    [Fact]
    public void AtLeastOrFull_WithEmpty_ReturnsFull()
    {
        var strategy = Consistency.AtLeastOrFull("");

        strategy.V1Consistency.FullyConsistent.Should().BeTrue();
    }

    [Fact]
    public void AtLeastOrMinLatency_WithRevision_ReturnsAtLeast()
    {
        var strategy = Consistency.AtLeastOrMinLatency("tok2");

        strategy.V1Consistency.AtLeastAsFresh.Should().NotBeNull();
        strategy.V1Consistency.AtLeastAsFresh.Token.Should().Be("tok2");
    }

    [Fact]
    public void AtLeastOrMinLatency_WithNull_ReturnsMinLatency()
    {
        var strategy = Consistency.AtLeastOrMinLatency(null);

        strategy.V1Consistency.MinimizeLatency.Should().BeTrue();
    }

    [Fact]
    public void AtLeastOrMinLatency_WithEmpty_ReturnsMinLatency()
    {
        var strategy = Consistency.AtLeastOrMinLatency("");

        strategy.V1Consistency.MinimizeLatency.Should().BeTrue();
    }

    [Fact]
    public void Strategy_IsSealed_Record()
    {
        // ConsistencyStrategy is a sealed record — verify value equality
        var a = Consistency.Full();
        var b = Consistency.Full();

        // Both should have the same shape (but not reference-equal proto objects)
        a.V1Consistency.FullyConsistent.Should().Be(b.V1Consistency.FullyConsistent);
    }
}
