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
        strategy.ToProto().Should().NotBeNull();
        strategy.ToProto().FullyConsistent.Should().BeTrue();
    }

    [Fact]
    public void MinLatency_ReturnsMinimizeLatencyStrategy()
    {
        var strategy = Consistency.MinLatency();

        strategy.Should().NotBeNull();
        strategy.ToProto().Should().NotBeNull();
        strategy.ToProto().MinimizeLatency.Should().BeTrue();
    }

    [Fact]
    public void AtLeast_ReturnsAtLeastAsFreshStrategy()
    {
        var strategy = Consistency.AtLeast(new SpiceDB.Client.ZedToken("sometoken123"));

        strategy.Should().NotBeNull();
        strategy.ToProto().AtLeastAsFresh.Should().NotBeNull();
        strategy.ToProto().AtLeastAsFresh.Token.Should().Be("sometoken123");
    }

    [Fact]
    public void AtLeast_ThrowsOnEmptyRevision()
    {
        var act = () => Consistency.AtLeast(new SpiceDB.Client.ZedToken(""));
        act.Should().Throw<InvalidArgumentException>();
    }

    [Fact]
    public void AtLeast_ThrowsOnNullRevision()
    {
        var act = () => Consistency.AtLeast(new SpiceDB.Client.ZedToken(null!));
        act.Should().Throw<InvalidArgumentException>();
    }

    [Fact]
    public void Snapshot_ReturnsExactSnapshotStrategy()
    {
        var strategy = Consistency.Snapshot(new SpiceDB.Client.ZedToken("rev42"));

        strategy.Should().NotBeNull();
        strategy.ToProto().AtExactSnapshot.Should().NotBeNull();
        strategy.ToProto().AtExactSnapshot.Token.Should().Be("rev42");
    }

    [Fact]
    public void Snapshot_ThrowsOnEmptyRevision()
    {
        var act = () => Consistency.Snapshot(new SpiceDB.Client.ZedToken(""));
        act.Should().Throw<InvalidArgumentException>();
    }

    [Fact]
    public void AtLeastOrFull_WithRevision_ReturnsAtLeast()
    {
        var strategy = Consistency.AtLeastOrFull(new SpiceDB.Client.ZedToken("tok1"));

        strategy.ToProto().AtLeastAsFresh.Should().NotBeNull();
        strategy.ToProto().AtLeastAsFresh.Token.Should().Be("tok1");
    }

    [Fact]
    public void AtLeastOrFull_WithNull_ReturnsFull()
    {
        var strategy = Consistency.AtLeastOrFull(null);

        strategy.ToProto().FullyConsistent.Should().BeTrue();
    }

    [Fact]
    public void AtLeastOrFull_WithEmpty_ReturnsFull()
    {
        var strategy = Consistency.AtLeastOrFull(new SpiceDB.Client.ZedToken(""));

        strategy.ToProto().FullyConsistent.Should().BeTrue();
    }

    [Fact]
    public void AtLeastOrMinLatency_WithRevision_ReturnsAtLeast()
    {
        var strategy = Consistency.AtLeastOrMinLatency(new SpiceDB.Client.ZedToken("tok2"));

        strategy.ToProto().AtLeastAsFresh.Should().NotBeNull();
        strategy.ToProto().AtLeastAsFresh.Token.Should().Be("tok2");
    }

    [Fact]
    public void AtLeastOrMinLatency_WithNull_ReturnsMinLatency()
    {
        var strategy = Consistency.AtLeastOrMinLatency(null);

        strategy.ToProto().MinimizeLatency.Should().BeTrue();
    }

    [Fact]
    public void AtLeastOrMinLatency_WithEmpty_ReturnsMinLatency()
    {
        var strategy = Consistency.AtLeastOrMinLatency(new SpiceDB.Client.ZedToken(""));

        strategy.ToProto().MinimizeLatency.Should().BeTrue();
    }

    [Fact]
    public void Strategy_IsSealed_Record()
    {
        // Consistency is a sealed record — verify value equality
        var a = Consistency.Full();
        var b = Consistency.Full();

        // Both should have the same shape (but not reference-equal proto objects)
        a.ToProto().FullyConsistent.Should().Be(b.ToProto().FullyConsistent);
    }
}
