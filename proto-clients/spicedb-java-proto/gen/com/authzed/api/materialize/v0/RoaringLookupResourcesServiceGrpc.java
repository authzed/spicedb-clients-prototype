package com.authzed.api.materialize.v0;

import static io.grpc.MethodDescriptor.generateFullMethodName;

/**
 */
@io.grpc.stub.annotations.GrpcGenerated
public final class RoaringLookupResourcesServiceGrpc {

  private RoaringLookupResourcesServiceGrpc() {}

  public static final java.lang.String SERVICE_NAME = "authzed.api.materialize.v0.RoaringLookupResourcesService";

  // Static method descriptors that strictly reflect the proto.
  private static volatile io.grpc.MethodDescriptor<com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest,
      com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse> getExperimentalRoaringLookupResourcesMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "ExperimentalRoaringLookupResources",
      requestType = com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest.class,
      responseType = com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse.class,
      methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
  public static io.grpc.MethodDescriptor<com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest,
      com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse> getExperimentalRoaringLookupResourcesMethod() {
    io.grpc.MethodDescriptor<com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest, com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse> getExperimentalRoaringLookupResourcesMethod;
    if ((getExperimentalRoaringLookupResourcesMethod = RoaringLookupResourcesServiceGrpc.getExperimentalRoaringLookupResourcesMethod) == null) {
      synchronized (RoaringLookupResourcesServiceGrpc.class) {
        if ((getExperimentalRoaringLookupResourcesMethod = RoaringLookupResourcesServiceGrpc.getExperimentalRoaringLookupResourcesMethod) == null) {
          RoaringLookupResourcesServiceGrpc.getExperimentalRoaringLookupResourcesMethod = getExperimentalRoaringLookupResourcesMethod =
              io.grpc.MethodDescriptor.<com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest, com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.UNARY)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "ExperimentalRoaringLookupResources"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse.getDefaultInstance()))
              .setSchemaDescriptor(new RoaringLookupResourcesServiceMethodDescriptorSupplier("ExperimentalRoaringLookupResources"))
              .build();
        }
      }
    }
    return getExperimentalRoaringLookupResourcesMethod;
  }

  /**
   * Creates a new async stub that supports all call types for the service
   */
  public static RoaringLookupResourcesServiceStub newStub(io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<RoaringLookupResourcesServiceStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<RoaringLookupResourcesServiceStub>() {
        @java.lang.Override
        public RoaringLookupResourcesServiceStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new RoaringLookupResourcesServiceStub(channel, callOptions);
        }
      };
    return RoaringLookupResourcesServiceStub.newStub(factory, channel);
  }

  /**
   * Creates a new blocking-style stub that supports all types of calls on the service
   */
  public static RoaringLookupResourcesServiceBlockingV2Stub newBlockingV2Stub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<RoaringLookupResourcesServiceBlockingV2Stub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<RoaringLookupResourcesServiceBlockingV2Stub>() {
        @java.lang.Override
        public RoaringLookupResourcesServiceBlockingV2Stub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new RoaringLookupResourcesServiceBlockingV2Stub(channel, callOptions);
        }
      };
    return RoaringLookupResourcesServiceBlockingV2Stub.newStub(factory, channel);
  }

  /**
   * Creates a new blocking-style stub that supports unary and streaming output calls on the service
   */
  public static RoaringLookupResourcesServiceBlockingStub newBlockingStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<RoaringLookupResourcesServiceBlockingStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<RoaringLookupResourcesServiceBlockingStub>() {
        @java.lang.Override
        public RoaringLookupResourcesServiceBlockingStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new RoaringLookupResourcesServiceBlockingStub(channel, callOptions);
        }
      };
    return RoaringLookupResourcesServiceBlockingStub.newStub(factory, channel);
  }

  /**
   * Creates a new ListenableFuture-style stub that supports unary calls on the service
   */
  public static RoaringLookupResourcesServiceFutureStub newFutureStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<RoaringLookupResourcesServiceFutureStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<RoaringLookupResourcesServiceFutureStub>() {
        @java.lang.Override
        public RoaringLookupResourcesServiceFutureStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new RoaringLookupResourcesServiceFutureStub(channel, callOptions);
        }
      };
    return RoaringLookupResourcesServiceFutureStub.newStub(factory, channel);
  }

  /**
   */
  public interface AsyncService {

    /**
     * <pre>
     * EXPERIMENTAL: RoaringLookupResources returns a roaring64 bitmap of the IDs
     * of the resources of the given type on which the given subject has the
     * given permission. This API is experimental and subject to change or
     * removal.
     * The bitmap is serialized in the RoaringFormatSpec 64-bit portable format
     * (https://github.com/RoaringBitmap/RoaringFormatSpec#extention-for-64-bit-implementations),
     * which OpenSearch consumes directly via a `"value_type": "bitmap"` terms
     * query against a `long` field, once Base64-encoded.
     * The IDs in the bitmap are the resource object IDs exactly as they appear
     * in the relationships: no surrogate or internal ID is introduced, so the
     * caller can use the bitmap directly against its own data (for example, a
     * search index keyed by the same IDs).
     * For this API to be usable, every resource object ID of the requested type
     * must be a canonical decimal integer that fits in 44 bits -- at most
     * 17592186044415 (2^44 - 1). Canonical means the string round-trips through
     * uint64 formatting unchanged: `document:007` and `document:7` are distinct
     * objects that would collide as the integer 7, so non-canonical IDs are
     * rejected. If any resource object ID violates either rule, the call fails
     * with FAILED_PRECONDITION rather than returning a partial bitmap, since a
     * bitmap that is quietly too small is a wrong authorization answer. When the
     * permission relates a type to itself (for example `group#member` looked up
     * for a `group#member` subject), the subject is one of its own resources, so
     * the subject's object ID is held to the same rules and can itself be the ID
     * named in that error.
     * Response size: the whole bitmap is returned in a single unary message, and
     * most gRPC clients default to refusing messages larger than 4 MiB. Roaring
     * is compact -- a million sequential IDs encode in a few hundred bytes -- but
     * sparse IDs cost close to 10 bytes each, so a result of more than roughly
     * 400,000 widely-spread IDs can exceed that default and fail on the CLIENT
     * side with RESOURCE_EXHAUSTED ("received message larger than max"). This is
     * a limit of the caller's own gRPC configuration, not of the service: raise
     * it to match the largest result you expect (in Go,
     * grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(n)); other languages
     * have an equivalent channel option). The `cardinality` field is returned so
     * callers can see how large a result is once received; no server-side result
     * limit is applied.
     * </pre>
     */
    default void experimentalRoaringLookupResources(com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest request,
        io.grpc.stub.StreamObserver<com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getExperimentalRoaringLookupResourcesMethod(), responseObserver);
    }
  }

  /**
   * Base class for the server implementation of the service RoaringLookupResourcesService.
   */
  public static abstract class RoaringLookupResourcesServiceImplBase
      implements io.grpc.BindableService, AsyncService {

    @java.lang.Override public final io.grpc.ServerServiceDefinition bindService() {
      return RoaringLookupResourcesServiceGrpc.bindService(this);
    }
  }

  /**
   * A stub to allow clients to do asynchronous rpc calls to service RoaringLookupResourcesService.
   */
  public static final class RoaringLookupResourcesServiceStub
      extends io.grpc.stub.AbstractAsyncStub<RoaringLookupResourcesServiceStub> {
    private RoaringLookupResourcesServiceStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected RoaringLookupResourcesServiceStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new RoaringLookupResourcesServiceStub(channel, callOptions);
    }

    /**
     * <pre>
     * EXPERIMENTAL: RoaringLookupResources returns a roaring64 bitmap of the IDs
     * of the resources of the given type on which the given subject has the
     * given permission. This API is experimental and subject to change or
     * removal.
     * The bitmap is serialized in the RoaringFormatSpec 64-bit portable format
     * (https://github.com/RoaringBitmap/RoaringFormatSpec#extention-for-64-bit-implementations),
     * which OpenSearch consumes directly via a `"value_type": "bitmap"` terms
     * query against a `long` field, once Base64-encoded.
     * The IDs in the bitmap are the resource object IDs exactly as they appear
     * in the relationships: no surrogate or internal ID is introduced, so the
     * caller can use the bitmap directly against its own data (for example, a
     * search index keyed by the same IDs).
     * For this API to be usable, every resource object ID of the requested type
     * must be a canonical decimal integer that fits in 44 bits -- at most
     * 17592186044415 (2^44 - 1). Canonical means the string round-trips through
     * uint64 formatting unchanged: `document:007` and `document:7` are distinct
     * objects that would collide as the integer 7, so non-canonical IDs are
     * rejected. If any resource object ID violates either rule, the call fails
     * with FAILED_PRECONDITION rather than returning a partial bitmap, since a
     * bitmap that is quietly too small is a wrong authorization answer. When the
     * permission relates a type to itself (for example `group#member` looked up
     * for a `group#member` subject), the subject is one of its own resources, so
     * the subject's object ID is held to the same rules and can itself be the ID
     * named in that error.
     * Response size: the whole bitmap is returned in a single unary message, and
     * most gRPC clients default to refusing messages larger than 4 MiB. Roaring
     * is compact -- a million sequential IDs encode in a few hundred bytes -- but
     * sparse IDs cost close to 10 bytes each, so a result of more than roughly
     * 400,000 widely-spread IDs can exceed that default and fail on the CLIENT
     * side with RESOURCE_EXHAUSTED ("received message larger than max"). This is
     * a limit of the caller's own gRPC configuration, not of the service: raise
     * it to match the largest result you expect (in Go,
     * grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(n)); other languages
     * have an equivalent channel option). The `cardinality` field is returned so
     * callers can see how large a result is once received; no server-side result
     * limit is applied.
     * </pre>
     */
    public void experimentalRoaringLookupResources(com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest request,
        io.grpc.stub.StreamObserver<com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse> responseObserver) {
      io.grpc.stub.ClientCalls.asyncUnaryCall(
          getChannel().newCall(getExperimentalRoaringLookupResourcesMethod(), getCallOptions()), request, responseObserver);
    }
  }

  /**
   * A stub to allow clients to do synchronous rpc calls to service RoaringLookupResourcesService.
   */
  public static final class RoaringLookupResourcesServiceBlockingV2Stub
      extends io.grpc.stub.AbstractBlockingStub<RoaringLookupResourcesServiceBlockingV2Stub> {
    private RoaringLookupResourcesServiceBlockingV2Stub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected RoaringLookupResourcesServiceBlockingV2Stub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new RoaringLookupResourcesServiceBlockingV2Stub(channel, callOptions);
    }

    /**
     * <pre>
     * EXPERIMENTAL: RoaringLookupResources returns a roaring64 bitmap of the IDs
     * of the resources of the given type on which the given subject has the
     * given permission. This API is experimental and subject to change or
     * removal.
     * The bitmap is serialized in the RoaringFormatSpec 64-bit portable format
     * (https://github.com/RoaringBitmap/RoaringFormatSpec#extention-for-64-bit-implementations),
     * which OpenSearch consumes directly via a `"value_type": "bitmap"` terms
     * query against a `long` field, once Base64-encoded.
     * The IDs in the bitmap are the resource object IDs exactly as they appear
     * in the relationships: no surrogate or internal ID is introduced, so the
     * caller can use the bitmap directly against its own data (for example, a
     * search index keyed by the same IDs).
     * For this API to be usable, every resource object ID of the requested type
     * must be a canonical decimal integer that fits in 44 bits -- at most
     * 17592186044415 (2^44 - 1). Canonical means the string round-trips through
     * uint64 formatting unchanged: `document:007` and `document:7` are distinct
     * objects that would collide as the integer 7, so non-canonical IDs are
     * rejected. If any resource object ID violates either rule, the call fails
     * with FAILED_PRECONDITION rather than returning a partial bitmap, since a
     * bitmap that is quietly too small is a wrong authorization answer. When the
     * permission relates a type to itself (for example `group#member` looked up
     * for a `group#member` subject), the subject is one of its own resources, so
     * the subject's object ID is held to the same rules and can itself be the ID
     * named in that error.
     * Response size: the whole bitmap is returned in a single unary message, and
     * most gRPC clients default to refusing messages larger than 4 MiB. Roaring
     * is compact -- a million sequential IDs encode in a few hundred bytes -- but
     * sparse IDs cost close to 10 bytes each, so a result of more than roughly
     * 400,000 widely-spread IDs can exceed that default and fail on the CLIENT
     * side with RESOURCE_EXHAUSTED ("received message larger than max"). This is
     * a limit of the caller's own gRPC configuration, not of the service: raise
     * it to match the largest result you expect (in Go,
     * grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(n)); other languages
     * have an equivalent channel option). The `cardinality` field is returned so
     * callers can see how large a result is once received; no server-side result
     * limit is applied.
     * </pre>
     */
    public com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse experimentalRoaringLookupResources(com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest request) throws io.grpc.StatusException {
      return io.grpc.stub.ClientCalls.blockingV2UnaryCall(
          getChannel(), getExperimentalRoaringLookupResourcesMethod(), getCallOptions(), request);
    }
  }

  /**
   * A stub to allow clients to do limited synchronous rpc calls to service RoaringLookupResourcesService.
   */
  public static final class RoaringLookupResourcesServiceBlockingStub
      extends io.grpc.stub.AbstractBlockingStub<RoaringLookupResourcesServiceBlockingStub> {
    private RoaringLookupResourcesServiceBlockingStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected RoaringLookupResourcesServiceBlockingStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new RoaringLookupResourcesServiceBlockingStub(channel, callOptions);
    }

    /**
     * <pre>
     * EXPERIMENTAL: RoaringLookupResources returns a roaring64 bitmap of the IDs
     * of the resources of the given type on which the given subject has the
     * given permission. This API is experimental and subject to change or
     * removal.
     * The bitmap is serialized in the RoaringFormatSpec 64-bit portable format
     * (https://github.com/RoaringBitmap/RoaringFormatSpec#extention-for-64-bit-implementations),
     * which OpenSearch consumes directly via a `"value_type": "bitmap"` terms
     * query against a `long` field, once Base64-encoded.
     * The IDs in the bitmap are the resource object IDs exactly as they appear
     * in the relationships: no surrogate or internal ID is introduced, so the
     * caller can use the bitmap directly against its own data (for example, a
     * search index keyed by the same IDs).
     * For this API to be usable, every resource object ID of the requested type
     * must be a canonical decimal integer that fits in 44 bits -- at most
     * 17592186044415 (2^44 - 1). Canonical means the string round-trips through
     * uint64 formatting unchanged: `document:007` and `document:7` are distinct
     * objects that would collide as the integer 7, so non-canonical IDs are
     * rejected. If any resource object ID violates either rule, the call fails
     * with FAILED_PRECONDITION rather than returning a partial bitmap, since a
     * bitmap that is quietly too small is a wrong authorization answer. When the
     * permission relates a type to itself (for example `group#member` looked up
     * for a `group#member` subject), the subject is one of its own resources, so
     * the subject's object ID is held to the same rules and can itself be the ID
     * named in that error.
     * Response size: the whole bitmap is returned in a single unary message, and
     * most gRPC clients default to refusing messages larger than 4 MiB. Roaring
     * is compact -- a million sequential IDs encode in a few hundred bytes -- but
     * sparse IDs cost close to 10 bytes each, so a result of more than roughly
     * 400,000 widely-spread IDs can exceed that default and fail on the CLIENT
     * side with RESOURCE_EXHAUSTED ("received message larger than max"). This is
     * a limit of the caller's own gRPC configuration, not of the service: raise
     * it to match the largest result you expect (in Go,
     * grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(n)); other languages
     * have an equivalent channel option). The `cardinality` field is returned so
     * callers can see how large a result is once received; no server-side result
     * limit is applied.
     * </pre>
     */
    public com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse experimentalRoaringLookupResources(com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest request) {
      return io.grpc.stub.ClientCalls.blockingUnaryCall(
          getChannel(), getExperimentalRoaringLookupResourcesMethod(), getCallOptions(), request);
    }
  }

  /**
   * A stub to allow clients to do ListenableFuture-style rpc calls to service RoaringLookupResourcesService.
   */
  public static final class RoaringLookupResourcesServiceFutureStub
      extends io.grpc.stub.AbstractFutureStub<RoaringLookupResourcesServiceFutureStub> {
    private RoaringLookupResourcesServiceFutureStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected RoaringLookupResourcesServiceFutureStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new RoaringLookupResourcesServiceFutureStub(channel, callOptions);
    }

    /**
     * <pre>
     * EXPERIMENTAL: RoaringLookupResources returns a roaring64 bitmap of the IDs
     * of the resources of the given type on which the given subject has the
     * given permission. This API is experimental and subject to change or
     * removal.
     * The bitmap is serialized in the RoaringFormatSpec 64-bit portable format
     * (https://github.com/RoaringBitmap/RoaringFormatSpec#extention-for-64-bit-implementations),
     * which OpenSearch consumes directly via a `"value_type": "bitmap"` terms
     * query against a `long` field, once Base64-encoded.
     * The IDs in the bitmap are the resource object IDs exactly as they appear
     * in the relationships: no surrogate or internal ID is introduced, so the
     * caller can use the bitmap directly against its own data (for example, a
     * search index keyed by the same IDs).
     * For this API to be usable, every resource object ID of the requested type
     * must be a canonical decimal integer that fits in 44 bits -- at most
     * 17592186044415 (2^44 - 1). Canonical means the string round-trips through
     * uint64 formatting unchanged: `document:007` and `document:7` are distinct
     * objects that would collide as the integer 7, so non-canonical IDs are
     * rejected. If any resource object ID violates either rule, the call fails
     * with FAILED_PRECONDITION rather than returning a partial bitmap, since a
     * bitmap that is quietly too small is a wrong authorization answer. When the
     * permission relates a type to itself (for example `group#member` looked up
     * for a `group#member` subject), the subject is one of its own resources, so
     * the subject's object ID is held to the same rules and can itself be the ID
     * named in that error.
     * Response size: the whole bitmap is returned in a single unary message, and
     * most gRPC clients default to refusing messages larger than 4 MiB. Roaring
     * is compact -- a million sequential IDs encode in a few hundred bytes -- but
     * sparse IDs cost close to 10 bytes each, so a result of more than roughly
     * 400,000 widely-spread IDs can exceed that default and fail on the CLIENT
     * side with RESOURCE_EXHAUSTED ("received message larger than max"). This is
     * a limit of the caller's own gRPC configuration, not of the service: raise
     * it to match the largest result you expect (in Go,
     * grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(n)); other languages
     * have an equivalent channel option). The `cardinality` field is returned so
     * callers can see how large a result is once received; no server-side result
     * limit is applied.
     * </pre>
     */
    public com.google.common.util.concurrent.ListenableFuture<com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse> experimentalRoaringLookupResources(
        com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest request) {
      return io.grpc.stub.ClientCalls.futureUnaryCall(
          getChannel().newCall(getExperimentalRoaringLookupResourcesMethod(), getCallOptions()), request);
    }
  }

  private static final int METHODID_EXPERIMENTAL_ROARING_LOOKUP_RESOURCES = 0;

  private static final class MethodHandlers<Req, Resp> implements
      io.grpc.stub.ServerCalls.UnaryMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.ServerStreamingMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.ClientStreamingMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.BidiStreamingMethod<Req, Resp> {
    private final AsyncService serviceImpl;
    private final int methodId;

    MethodHandlers(AsyncService serviceImpl, int methodId) {
      this.serviceImpl = serviceImpl;
      this.methodId = methodId;
    }

    @java.lang.Override
    @java.lang.SuppressWarnings("unchecked")
    public void invoke(Req request, io.grpc.stub.StreamObserver<Resp> responseObserver) {
      switch (methodId) {
        case METHODID_EXPERIMENTAL_ROARING_LOOKUP_RESOURCES:
          serviceImpl.experimentalRoaringLookupResources((com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest) request,
              (io.grpc.stub.StreamObserver<com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse>) responseObserver);
          break;
        default:
          throw new AssertionError();
      }
    }

    @java.lang.Override
    @java.lang.SuppressWarnings("unchecked")
    public io.grpc.stub.StreamObserver<Req> invoke(
        io.grpc.stub.StreamObserver<Resp> responseObserver) {
      switch (methodId) {
        default:
          throw new AssertionError();
      }
    }
  }

  public static final io.grpc.ServerServiceDefinition bindService(AsyncService service) {
    return io.grpc.ServerServiceDefinition.builder(getServiceDescriptor())
        .addMethod(
          getExperimentalRoaringLookupResourcesMethod(),
          io.grpc.stub.ServerCalls.asyncUnaryCall(
            new MethodHandlers<
              com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest,
              com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse>(
                service, METHODID_EXPERIMENTAL_ROARING_LOOKUP_RESOURCES)))
        .build();
  }

  private static abstract class RoaringLookupResourcesServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoFileDescriptorSupplier, io.grpc.protobuf.ProtoServiceDescriptorSupplier {
    RoaringLookupResourcesServiceBaseDescriptorSupplier() {}

    @java.lang.Override
    public com.google.protobuf.Descriptors.FileDescriptor getFileDescriptor() {
      return com.authzed.api.materialize.v0.Roaringlookupresources.getDescriptor();
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.ServiceDescriptor getServiceDescriptor() {
      return getFileDescriptor().findServiceByName("RoaringLookupResourcesService");
    }
  }

  private static final class RoaringLookupResourcesServiceFileDescriptorSupplier
      extends RoaringLookupResourcesServiceBaseDescriptorSupplier {
    RoaringLookupResourcesServiceFileDescriptorSupplier() {}
  }

  private static final class RoaringLookupResourcesServiceMethodDescriptorSupplier
      extends RoaringLookupResourcesServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoMethodDescriptorSupplier {
    private final java.lang.String methodName;

    RoaringLookupResourcesServiceMethodDescriptorSupplier(java.lang.String methodName) {
      this.methodName = methodName;
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.MethodDescriptor getMethodDescriptor() {
      return getServiceDescriptor().findMethodByName(methodName);
    }
  }

  private static volatile io.grpc.ServiceDescriptor serviceDescriptor;

  public static io.grpc.ServiceDescriptor getServiceDescriptor() {
    io.grpc.ServiceDescriptor result = serviceDescriptor;
    if (result == null) {
      synchronized (RoaringLookupResourcesServiceGrpc.class) {
        result = serviceDescriptor;
        if (result == null) {
          serviceDescriptor = result = io.grpc.ServiceDescriptor.newBuilder(SERVICE_NAME)
              .setSchemaDescriptor(new RoaringLookupResourcesServiceFileDescriptorSupplier())
              .addMethod(getExperimentalRoaringLookupResourcesMethod())
              .build();
        }
      }
    }
    return result;
  }
}
