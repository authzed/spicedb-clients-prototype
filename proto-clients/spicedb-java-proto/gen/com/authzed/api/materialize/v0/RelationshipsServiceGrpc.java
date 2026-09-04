package com.authzed.api.materialize.v0;

import static io.grpc.MethodDescriptor.generateFullMethodName;

/**
 */
@io.grpc.stub.annotations.GrpcGenerated
public final class RelationshipsServiceGrpc {

  private RelationshipsServiceGrpc() {}

  public static final java.lang.String SERVICE_NAME = "authzed.api.materialize.v0.RelationshipsService";

  // Static method descriptors that strictly reflect the proto.
  private static volatile io.grpc.MethodDescriptor<com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest,
      com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse> getExperimentalCountRelationshipsByFilterMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "ExperimentalCountRelationshipsByFilter",
      requestType = com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest.class,
      responseType = com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse.class,
      methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
  public static io.grpc.MethodDescriptor<com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest,
      com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse> getExperimentalCountRelationshipsByFilterMethod() {
    io.grpc.MethodDescriptor<com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest, com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse> getExperimentalCountRelationshipsByFilterMethod;
    if ((getExperimentalCountRelationshipsByFilterMethod = RelationshipsServiceGrpc.getExperimentalCountRelationshipsByFilterMethod) == null) {
      synchronized (RelationshipsServiceGrpc.class) {
        if ((getExperimentalCountRelationshipsByFilterMethod = RelationshipsServiceGrpc.getExperimentalCountRelationshipsByFilterMethod) == null) {
          RelationshipsServiceGrpc.getExperimentalCountRelationshipsByFilterMethod = getExperimentalCountRelationshipsByFilterMethod =
              io.grpc.MethodDescriptor.<com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest, com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.UNARY)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "ExperimentalCountRelationshipsByFilter"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse.getDefaultInstance()))
              .setSchemaDescriptor(new RelationshipsServiceMethodDescriptorSupplier("ExperimentalCountRelationshipsByFilter"))
              .build();
        }
      }
    }
    return getExperimentalCountRelationshipsByFilterMethod;
  }

  /**
   * Creates a new async stub that supports all call types for the service
   */
  public static RelationshipsServiceStub newStub(io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<RelationshipsServiceStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<RelationshipsServiceStub>() {
        @java.lang.Override
        public RelationshipsServiceStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new RelationshipsServiceStub(channel, callOptions);
        }
      };
    return RelationshipsServiceStub.newStub(factory, channel);
  }

  /**
   * Creates a new blocking-style stub that supports all types of calls on the service
   */
  public static RelationshipsServiceBlockingV2Stub newBlockingV2Stub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<RelationshipsServiceBlockingV2Stub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<RelationshipsServiceBlockingV2Stub>() {
        @java.lang.Override
        public RelationshipsServiceBlockingV2Stub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new RelationshipsServiceBlockingV2Stub(channel, callOptions);
        }
      };
    return RelationshipsServiceBlockingV2Stub.newStub(factory, channel);
  }

  /**
   * Creates a new blocking-style stub that supports unary and streaming output calls on the service
   */
  public static RelationshipsServiceBlockingStub newBlockingStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<RelationshipsServiceBlockingStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<RelationshipsServiceBlockingStub>() {
        @java.lang.Override
        public RelationshipsServiceBlockingStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new RelationshipsServiceBlockingStub(channel, callOptions);
        }
      };
    return RelationshipsServiceBlockingStub.newStub(factory, channel);
  }

  /**
   * Creates a new ListenableFuture-style stub that supports unary calls on the service
   */
  public static RelationshipsServiceFutureStub newFutureStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<RelationshipsServiceFutureStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<RelationshipsServiceFutureStub>() {
        @java.lang.Override
        public RelationshipsServiceFutureStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new RelationshipsServiceFutureStub(channel, callOptions);
        }
      };
    return RelationshipsServiceFutureStub.newStub(factory, channel);
  }

  /**
   */
  public interface AsyncService {

    /**
     * <pre>
     * EXPERIMENTAL: CountRelationships returns the count of relationships for a given filter.
     * </pre>
     */
    default void experimentalCountRelationshipsByFilter(com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest request,
        io.grpc.stub.StreamObserver<com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getExperimentalCountRelationshipsByFilterMethod(), responseObserver);
    }
  }

  /**
   * Base class for the server implementation of the service RelationshipsService.
   */
  public static abstract class RelationshipsServiceImplBase
      implements io.grpc.BindableService, AsyncService {

    @java.lang.Override public final io.grpc.ServerServiceDefinition bindService() {
      return RelationshipsServiceGrpc.bindService(this);
    }
  }

  /**
   * A stub to allow clients to do asynchronous rpc calls to service RelationshipsService.
   */
  public static final class RelationshipsServiceStub
      extends io.grpc.stub.AbstractAsyncStub<RelationshipsServiceStub> {
    private RelationshipsServiceStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected RelationshipsServiceStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new RelationshipsServiceStub(channel, callOptions);
    }

    /**
     * <pre>
     * EXPERIMENTAL: CountRelationships returns the count of relationships for a given filter.
     * </pre>
     */
    public void experimentalCountRelationshipsByFilter(com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest request,
        io.grpc.stub.StreamObserver<com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse> responseObserver) {
      io.grpc.stub.ClientCalls.asyncUnaryCall(
          getChannel().newCall(getExperimentalCountRelationshipsByFilterMethod(), getCallOptions()), request, responseObserver);
    }
  }

  /**
   * A stub to allow clients to do synchronous rpc calls to service RelationshipsService.
   */
  public static final class RelationshipsServiceBlockingV2Stub
      extends io.grpc.stub.AbstractBlockingStub<RelationshipsServiceBlockingV2Stub> {
    private RelationshipsServiceBlockingV2Stub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected RelationshipsServiceBlockingV2Stub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new RelationshipsServiceBlockingV2Stub(channel, callOptions);
    }

    /**
     * <pre>
     * EXPERIMENTAL: CountRelationships returns the count of relationships for a given filter.
     * </pre>
     */
    public com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse experimentalCountRelationshipsByFilter(com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest request) throws io.grpc.StatusException {
      return io.grpc.stub.ClientCalls.blockingV2UnaryCall(
          getChannel(), getExperimentalCountRelationshipsByFilterMethod(), getCallOptions(), request);
    }
  }

  /**
   * A stub to allow clients to do limited synchronous rpc calls to service RelationshipsService.
   */
  public static final class RelationshipsServiceBlockingStub
      extends io.grpc.stub.AbstractBlockingStub<RelationshipsServiceBlockingStub> {
    private RelationshipsServiceBlockingStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected RelationshipsServiceBlockingStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new RelationshipsServiceBlockingStub(channel, callOptions);
    }

    /**
     * <pre>
     * EXPERIMENTAL: CountRelationships returns the count of relationships for a given filter.
     * </pre>
     */
    public com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse experimentalCountRelationshipsByFilter(com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest request) {
      return io.grpc.stub.ClientCalls.blockingUnaryCall(
          getChannel(), getExperimentalCountRelationshipsByFilterMethod(), getCallOptions(), request);
    }
  }

  /**
   * A stub to allow clients to do ListenableFuture-style rpc calls to service RelationshipsService.
   */
  public static final class RelationshipsServiceFutureStub
      extends io.grpc.stub.AbstractFutureStub<RelationshipsServiceFutureStub> {
    private RelationshipsServiceFutureStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected RelationshipsServiceFutureStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new RelationshipsServiceFutureStub(channel, callOptions);
    }

    /**
     * <pre>
     * EXPERIMENTAL: CountRelationships returns the count of relationships for a given filter.
     * </pre>
     */
    public com.google.common.util.concurrent.ListenableFuture<com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse> experimentalCountRelationshipsByFilter(
        com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest request) {
      return io.grpc.stub.ClientCalls.futureUnaryCall(
          getChannel().newCall(getExperimentalCountRelationshipsByFilterMethod(), getCallOptions()), request);
    }
  }

  private static final int METHODID_EXPERIMENTAL_COUNT_RELATIONSHIPS_BY_FILTER = 0;

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
        case METHODID_EXPERIMENTAL_COUNT_RELATIONSHIPS_BY_FILTER:
          serviceImpl.experimentalCountRelationshipsByFilter((com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest) request,
              (io.grpc.stub.StreamObserver<com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse>) responseObserver);
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
          getExperimentalCountRelationshipsByFilterMethod(),
          io.grpc.stub.ServerCalls.asyncUnaryCall(
            new MethodHandlers<
              com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest,
              com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse>(
                service, METHODID_EXPERIMENTAL_COUNT_RELATIONSHIPS_BY_FILTER)))
        .build();
  }

  private static abstract class RelationshipsServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoFileDescriptorSupplier, io.grpc.protobuf.ProtoServiceDescriptorSupplier {
    RelationshipsServiceBaseDescriptorSupplier() {}

    @java.lang.Override
    public com.google.protobuf.Descriptors.FileDescriptor getFileDescriptor() {
      return com.authzed.api.materialize.v0.Relationships.getDescriptor();
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.ServiceDescriptor getServiceDescriptor() {
      return getFileDescriptor().findServiceByName("RelationshipsService");
    }
  }

  private static final class RelationshipsServiceFileDescriptorSupplier
      extends RelationshipsServiceBaseDescriptorSupplier {
    RelationshipsServiceFileDescriptorSupplier() {}
  }

  private static final class RelationshipsServiceMethodDescriptorSupplier
      extends RelationshipsServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoMethodDescriptorSupplier {
    private final java.lang.String methodName;

    RelationshipsServiceMethodDescriptorSupplier(java.lang.String methodName) {
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
      synchronized (RelationshipsServiceGrpc.class) {
        result = serviceDescriptor;
        if (result == null) {
          serviceDescriptor = result = io.grpc.ServiceDescriptor.newBuilder(SERVICE_NAME)
              .setSchemaDescriptor(new RelationshipsServiceFileDescriptorSupplier())
              .addMethod(getExperimentalCountRelationshipsByFilterMethod())
              .build();
        }
      }
    }
    return result;
  }
}
