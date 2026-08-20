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
    public void IsTransient_ResourceExhausted_ReturnsFalse()
    {
        // Inverted from "ReturnsTrue" -- RESOURCE_EXHAUSTED must NOT be
        // retried. In SpiceDB it signals memory load-shed or a
        // deterministic MaxDepthExceeded, never a transient hiccup. See
        // DESIGN.md, "Automatic retry is for idempotent operations only".
        var rpc = new RpcException(new Status(StatusCode.ResourceExhausted, ""));
        ErrorMapper.IsTransient(rpc).Should().BeFalse();
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
    public void IsTransient_TypedResourceExhaustedException_ReturnsFalse()
    {
        // Inverted from "ReturnsTrue" -- see IsTransient_ResourceExhausted_ReturnsFalse above.
        var ex = new ResourceExhaustedException("test");
        ErrorMapper.IsTransient(ex).Should().BeFalse();
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
        new UnauthenticatedException("").Should().BeAssignableTo<SpiceDBException>();
        new OutOfRangeException("").Should().BeAssignableTo<SpiceDBException>();
    }

    /// <summary>
    /// Returns the wire name of an <c>authzed.api.v1.ErrorReason</c> value, read off the generated
    /// enum's own descriptor rather than from a hand-written table — so a drift between the
    /// exposed reason string and the proto enum fails this test.
    /// </summary>
    private static string ProtoNameOf(Authzed.Api.V1.ErrorReason reason) =>
        Authzed.Api.V1.ErrorReasonReflection.Descriptor.EnumTypes[0]
            .FindValueByNumber((int)reason)!.Name;

    /// <summary>
    /// Builds an RpcException carrying a google.rpc.ErrorInfo detail, the shape SpiceDB uses to
    /// explain a failure.
    /// </summary>
    private static RpcException WithErrorInfo(
        Grpc.Core.StatusCode code, string message, Google.Rpc.ErrorInfo info)
    {
        var status = new Google.Rpc.Status
        {
            Code = (int)code,
            Message = message,
        };
        status.Details.Add(Google.Protobuf.WellKnownTypes.Any.Pack(info));
        return status.ToRpcException();
    }

    // OUT_OF_RANGE is SpiceDB's code for an expired or garbage-collected ZedToken. Recovery is
    // mechanical -- drop the token, re-read at full consistency -- so it must be distinguishable
    // by type rather than by message. See root DESIGN.md, "RULE: Error mapping must not lose the
    // server's detail".
    [Fact]
    public void ToSpiceDBException_MapsOutOfRangeToItsOwnType()
    {
        var rpc = new RpcException(
            new Grpc.Core.Status(StatusCode.OutOfRange, "revision no longer available"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<OutOfRangeException>();
        ex.Should().NotBeOfType<InvalidArgumentException>();
        ex.Message.Should().Be("revision no longer available");
    }

    // A wrong, expired, or rotated token must be distinguishable from an internal server fault, so
    // a caller can refresh credentials on one and page someone on the other.
    [Fact]
    public void ToSpiceDBException_MapsUnauthenticatedToItsOwnType()
    {
        var rpc = new RpcException(new Grpc.Core.Status(StatusCode.Unauthenticated, "bad token"));
        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<UnauthenticatedException>();
        ex.Should().NotBeOfType<PermissionDeniedException>();
    }

    [Fact]
    public void IsTransient_NeitherNewlyMappedCode_ReturnsFalse()
    {
        ErrorMapper.IsTransient(
            new RpcException(new Grpc.Core.Status(StatusCode.OutOfRange, "x"))).Should().BeFalse();
        ErrorMapper.IsTransient(
            new RpcException(new Grpc.Core.Status(StatusCode.Unauthenticated, "x"))).Should().BeFalse();
    }

    [Fact]
    public void ToSpiceDBException_SurfacesErrorReasonAndMetadata()
    {
        var rpc = WithErrorInfo(
            StatusCode.ResourceExhausted,
            "max depth exceeded",
            new Google.Rpc.ErrorInfo
            {
                Reason = "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
                Domain = "authzed.com",
                Metadata = { { "maximum_depth_allowed", "50" } },
            });

        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<ResourceExhaustedException>();
        // The exposed reason is exactly the authzed.api.v1.ErrorReason enum name, so a caller can
        // compare against the generated enum without this client carrying a hand-maintained copy
        // of it.
        ex.Reason.Should().Be("ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED");
        ex.Reason.Should().Be(ProtoNameOf(Authzed.Api.V1.ErrorReason.MaximumDepthExceeded));
        ex.ReasonDomain.Should().Be("authzed.com");
        ex.ReasonMetadata.Should().Contain("maximum_depth_allowed", "50");
    }

    [Fact]
    public void ToSpiceDBException_ReasonMetadataNamesTheFailingPrecondition()
    {
        var rpc = WithErrorInfo(
            StatusCode.FailedPrecondition,
            "precondition failed",
            new Google.Rpc.ErrorInfo
            {
                Reason = "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
                Domain = "authzed.com",
                Metadata =
                {
                    { "precondition_resource_id", "firstdoc" },
                    { "precondition_relation", "viewer" },
                },
            });

        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<FailedPreconditionException>();
        ex.Reason.Should().Be("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE");
        ex.ReasonMetadata.Should().Contain("precondition_resource_id", "firstdoc");
        ex.ReasonMetadata.Should().Contain("precondition_relation", "viewer");
    }

    // A reason a newer server knows and this client does not is server-supplied: root DESIGN.md's
    // "RULE: A conversion that cannot preserve meaning must fail" requires it to degrade safely,
    // not to throw.
    [Fact]
    public void ToSpiceDBException_UnrecognizedReasonPassesThroughWithoutThrowing()
    {
        var rpc = WithErrorInfo(
            StatusCode.InvalidArgument,
            "from the future",
            new Google.Rpc.ErrorInfo
            {
                Reason = "ERROR_REASON_INVENTED_BY_A_NEWER_SERVER",
                Domain = "authzed.com",
                Metadata = { { "k", "v" } },
            });

        var ex = ErrorMapper.ToSpiceDBException(rpc);

        ex.Should().BeOfType<InvalidArgumentException>();
        ex.Reason.Should().Be("ERROR_REASON_INVENTED_BY_A_NEWER_SERVER");
        ex.ReasonMetadata.Should().Contain("k", "v");
    }

    [Fact]
    public void ToSpiceDBException_AbsentErrorInfoLeavesReasonEmpty()
    {
        var ex = ErrorMapper.ToSpiceDBException(
            new RpcException(new Grpc.Core.Status(StatusCode.NotFound, "nope")));

        ex.Reason.Should().BeEmpty();
        ex.ReasonDomain.Should().BeEmpty();
        ex.ReasonMetadata.Should().BeEmpty();
    }

    [Fact]
    public void LocallyConstructedException_HasNoReason()
    {
        new SpiceDBException("local problem").Reason.Should().BeEmpty();
        new SpiceDBException("local problem").ReasonMetadata.Should().BeEmpty();
    }
}
