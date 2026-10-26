using Xunit;

namespace SpiceDB.Client.Tests;

public class DeleteProgressAccessorTests
{
    [Fact]
    public void NativeCancellationProgressIsAccessibleWithoutReplacingException()
    {
        using var source = new CancellationTokenSource();
        source.Cancel();
        Exception error = new OperationCanceledException(source.Token);
        var original = error;
        var progress = new DeleteProgress(2, new ZedToken("revision"), "cursor", 1, true);
        error.Data["SpiceDB.DeleteProgress"] = progress;
        Assert.Same(progress, error.GetDeleteProgress());
        Assert.Same(original, error);
        Assert.Equal(source.Token, Assert.IsType<OperationCanceledException>(error).CancellationToken);
    }

    [Fact]
    public void NormalClientErrorsExposeTheSameTypedProgress()
    {
        var progress = new DeleteProgress(2, new ZedToken("revision"), "cursor", 1, true);
        Exception error = new SpiceDBException("failed") { DeleteProgress = progress };
        Assert.Same(progress, error.GetDeleteProgress());
    }

    [Fact]
    public void ErrorsWithoutDeletionProgressReturnNull()
    {
        Assert.Null(new SpiceDBException("other operation").GetDeleteProgress());
        Assert.Null(new OperationCanceledException().GetDeleteProgress());
        Assert.Null(new Exception().GetDeleteProgress());
        var error = new OperationCanceledException();
        error.Data["SpiceDB.DeleteProgress"] = "invalid payload";
        Assert.Null(error.GetDeleteProgress());
    }

    [Fact]
    public void DeleteApiExposesExplicitNonAtomicNameAndRevision()
    {
        Assert.Null(typeof(SpiceDBClient).GetMethod("DeleteAllRelationshipsAsync"));
        Assert.NotNull(typeof(SpiceDBClient).GetMethod("DeleteAllRelationshipsNonAtomicAsync"));
        Assert.Null(typeof(DeleteResult).GetProperty("DeletedAt"));
        Assert.Equal(new ZedToken("revision"), new DeleteResult(new ZedToken("revision"), 1, true, null).Revision);
    }
}
