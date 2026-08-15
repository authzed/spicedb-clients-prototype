using FluentAssertions;
using Grpc.Core;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class ErrorsTests
{
    [Fact]
    public void ToSpiceDBException_MapsPermissionDenied()
    {
        var rpc = new RpcException(new Status(StatusCode.PermissionDenied, "denied"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<PermissionDeniedException>();
        ex.Message.Should().Be("denied");
        ex.InnerException.Should().BeSameAs(rpc);
    }

    [Fact]
    public void ToSpiceDBException_MapsNotFound()
    {
        var rpc = new RpcException(new Status(StatusCode.NotFound, "not found"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<NotFoundException>();
        ex.Message.Should().Be("not found");
    }

    [Fact]
    public void ToSpiceDBException_MapsAlreadyExists()
    {
        var rpc = new RpcException(new Status(StatusCode.AlreadyExists, "exists"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<AlreadyExistsException>();
    }

    [Fact]
    public void ToSpiceDBException_MapsInvalidArgument()
    {
        var rpc = new RpcException(new Status(StatusCode.InvalidArgument, "bad arg"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<InvalidArgumentException>();
    }

    [Fact]
    public void ToSpiceDBException_MapsFailedPrecondition()
    {
        var rpc = new RpcException(new Status(StatusCode.FailedPrecondition, "precondition"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<FailedPreconditionException>();
    }

    [Fact]
    public void ToSpiceDBException_MapsUnavailable()
    {
        var rpc = new RpcException(new Status(StatusCode.Unavailable, "unavailable"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<UnavailableException>();
    }

    [Fact]
    public void ToSpiceDBException_MapsCancelled()
    {
        var rpc = new RpcException(new Status(StatusCode.Cancelled, "cancelled"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<CancelledException>();
    }

    [Fact]
    public void ToSpiceDBException_MapsResourceExhausted()
    {
        var rpc = new RpcException(new Status(StatusCode.ResourceExhausted, "exhausted"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<ResourceExhaustedException>();
    }

    [Fact]
    public void ToSpiceDBException_MapsDeadlineExceeded()
    {
        var rpc = new RpcException(new Status(StatusCode.DeadlineExceeded, "deadline"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<DeadlineExceededException>();
    }

    [Fact]
    public void ToSpiceDBException_MapsAborted()
    {
        var rpc = new RpcException(new Status(StatusCode.Aborted, "aborted"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<AbortedException>();
        ex.Should().BeAssignableTo<SpiceDBException>();
    }

    [Fact]
    public void ToSpiceDBException_UnknownCode_ReturnsBaseException()
    {
        var rpc = new RpcException(new Status(StatusCode.Internal, "internal error"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<SpiceDBException>();
        ex.Message.Should().Be("internal error");
    }

    [Fact]
    public void ToSpiceDBException_ThrowsOnNull()
    {
        var act = () => ErrorMapper.ToSpiceDBException(null!);
        act.Should().Throw<ArgumentNullException>();
    }

    [Fact]
    public void IsTransient_Unavailable_ReturnsTrue()
    {
        var rpc = new RpcException(new Status(StatusCode.Unavailable, ""));
        ErrorMapper.IsTransient(rpc).Should().BeTrue();
    }

    [Fact]
    public void IsTransient_DeadlineExceeded_ReturnsFalse()
    {
        var rpc = new RpcException(new Status(StatusCode.DeadlineExceeded, ""));
        ErrorMapper.IsTransient(rpc).Should().BeFalse();
    }

    [Fact]
    public void IsTransient_ResourceExhausted_ReturnsTrue()
    {
        var rpc = new RpcException(new Status(StatusCode.ResourceExhausted, ""));
        ErrorMapper.IsTransient(rpc).Should().BeTrue();
    }

    [Fact]
    public void IsTransient_Aborted_ReturnsTrue()
    {
        var rpc = new RpcException(new Status(StatusCode.Aborted, ""));
        ErrorMapper.IsTransient(rpc).Should().BeTrue();
    }

    [Fact]
    public void IsTransient_PermissionDenied_ReturnsFalse()
    {
        var rpc = new RpcException(new Status(StatusCode.PermissionDenied, ""));
        ErrorMapper.IsTransient(rpc).Should().BeFalse();
    }

    [Fact]
    public void IsTransient_TypedUnavailableException_ReturnsTrue()
    {
        var ex = new UnavailableException("test");
        ErrorMapper.IsTransient(ex).Should().BeTrue();
    }

    [Fact]
    public void IsTransient_TypedDeadlineExceededException_ReturnsFalse()
    {
        var ex = new DeadlineExceededException("test");
        ErrorMapper.IsTransient(ex).Should().BeFalse();
    }

    [Fact]
    public void IsTransient_TypedResourceExhaustedException_ReturnsTrue()
    {
        var ex = new ResourceExhaustedException("test");
        ErrorMapper.IsTransient(ex).Should().BeTrue();
    }

    [Fact]
    public void IsTransient_TypedAbortedException_ReturnsTrue()
    {
        var ex = new AbortedException("test");
        ErrorMapper.IsTransient(ex).Should().BeTrue();
    }

    [Fact]
    public void IsTransient_GenericException_ReturnsFalse()
    {
        var ex = new Exception("generic");
        ErrorMapper.IsTransient(ex).Should().BeFalse();
    }

    [Fact]
    public void ExceptionHierarchy_AllDeriveFromSpiceDBException()
    {
        new PermissionDeniedException("").Should().BeAssignableTo<SpiceDBException>();
        new NotFoundException("").Should().BeAssignableTo<SpiceDBException>();
        new AlreadyExistsException("").Should().BeAssignableTo<SpiceDBException>();
        new InvalidArgumentException("").Should().BeAssignableTo<SpiceDBException>();
        new FailedPreconditionException("").Should().BeAssignableTo<SpiceDBException>();
        new UnavailableException("").Should().BeAssignableTo<SpiceDBException>();
        new CancelledException("").Should().BeAssignableTo<SpiceDBException>();
        new ResourceExhaustedException("").Should().BeAssignableTo<SpiceDBException>();
        new DeadlineExceededException("").Should().BeAssignableTo<SpiceDBException>();
        new AbortedException("").Should().BeAssignableTo<SpiceDBException>();
    }
}
