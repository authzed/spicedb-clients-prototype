using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class PermissionCheckBuilderTests
{
    [Fact]
    public void BuildsIndependentSnapshots()
    {
        var nested = new List<object?> { "original" };
        var context = new Dictionary<string, object?> { ["values"] = nested };
        var resource = new ObjectRef("document", "readme");
        var subject = SubjectRef.Direct(new ObjectRef("user", "alice"));
        var builder = PermissionCheck.CreateBuilder().WithResource(resource).WithPermission("view")
            .WithSubject(subject).WithConsistency(Consistency.Full()).WithContext(context);
        nested.Add("changed");
        var first = builder.Build();
        var second = builder.WithPermission("edit").WithContext(null).Build();
        Assert.Equal(resource, first.Resource);
        Assert.Equal(subject, first.Subject);
        Assert.Equal("view", first.Permission);
        Assert.Equal(new object?[] { "original" }, Assert.IsAssignableFrom<IEnumerable<object?>>(first.Context!["values"]));
        Assert.Equal("edit", second.Permission);
        Assert.Null(second.Context);
    }

    [Fact]
    public void RejectsMissingRequiredFields()
    {
        var builder = PermissionCheck.CreateBuilder();
        Assert.Throws<InvalidArgumentException>(() => builder.Build());
        builder.WithResource(new ObjectRef("document", "readme"));
        Assert.Throws<InvalidArgumentException>(() => builder.Build());
        builder.WithPermission("view");
        Assert.Throws<InvalidArgumentException>(() => builder.Build());
        builder.WithSubject(SubjectRef.Direct(new ObjectRef("user", "alice")));
        Assert.Throws<InvalidArgumentException>(() => builder.Build());
        builder.WithConsistency(Consistency.Full());
        Assert.NotNull(builder.Build());
        Assert.Throws<InvalidArgumentException>(() => builder.WithPermission("").Build());
    }
}
