package com.authzed.spicedb;

import io.grpc.CallOptions;
import io.grpc.Channel;
import io.grpc.ClientCall;
import io.grpc.ClientInterceptor;
import io.grpc.ForwardingClientCall;
import io.grpc.ForwardingClientCallListener;
import io.grpc.Metadata;
import io.grpc.MethodDescriptor;
import io.grpc.Status;
import java.util.function.UnaryOperator;

/**
 * Per-operation transport escape hatch, accepted by every operation's options ({@code
 * .transport(...)}): adjust the gRPC {@link CallOptions} of each call and capture response
 * headers/trailers. Applies to every attempt, chunk and page of the operation.
 *
 * <p>This is the supported route to transport-level data, so callers do not need {@link
 * SpiceDBClient#rawChannel()} for it. The call-options hook sees the client's own deadline already
 * applied; replacing it overrides the operation's budget, so prefer the operation's {@code
 * timeout}.
 *
 * <pre>{@code
 * var md = new ResponseMetadata();
 * client.readSchema(CallOptions.builder()
 *     .transport(TransportOptions.builder().responseMetadata(md).build())
 *     .build());
 * String version = md.headers().get(Metadata.Key.of("io.spicedb.version", ASCII_STRING_MARSHALLER));
 * }</pre>
 */
public final class TransportOptions {
  private static final TransportOptions NONE = builder().build();

  private final UnaryOperator<CallOptions> callOptions;
  private final ResponseMetadata responseMetadata;
  private final ClientInterceptor interceptor;

  private TransportOptions(Builder b) {
    callOptions = b.callOptions;
    responseMetadata = b.responseMetadata;
    interceptor = callOptions == null && responseMetadata == null ? null : new Interceptor();
  }

  /** No transport adjustments (the default). */
  public static TransportOptions none() {
    return NONE;
  }

  public static Builder builder() {
    return new Builder();
  }

  /** The call-options hook, or {@code null}. */
  public UnaryOperator<CallOptions> callOptions() {
    return callOptions;
  }

  /** The metadata capture, or {@code null}. */
  public ResponseMetadata responseMetadata() {
    return responseMetadata;
  }

  /** The interceptor applying these options, or {@code null} when there is nothing to apply. */
  ClientInterceptor interceptor() {
    return interceptor;
  }

  private final class Interceptor implements ClientInterceptor {
    @Override
    public <Q, R> ClientCall<Q, R> interceptCall(
        MethodDescriptor<Q, R> method, CallOptions options, Channel next) {
      CallOptions effective = callOptions == null ? options : callOptions.apply(options);
      ClientCall<Q, R> call = next.newCall(method, effective);
      if (responseMetadata == null) return call;
      return new ForwardingClientCall.SimpleForwardingClientCall<Q, R>(call) {
        @Override
        public void start(Listener<R> listener, Metadata headers) {
          super.start(
              new ForwardingClientCallListener.SimpleForwardingClientCallListener<R>(listener) {
                @Override
                public void onHeaders(Metadata h) {
                  responseMetadata.recordHeaders(h);
                  super.onHeaders(h);
                }

                @Override
                public void onClose(Status status, Metadata trailers) {
                  responseMetadata.recordTrailers(trailers);
                  super.onClose(status, trailers);
                }
              },
              headers);
        }
      };
    }
  }

  public static final class Builder {
    private UnaryOperator<CallOptions> callOptions;
    private ResponseMetadata responseMetadata;

    private Builder() {}

    /** Adjusts each call's gRPC {@link CallOptions} (compression, wait-for-ready, custom keys). */
    public Builder callOptions(UnaryOperator<CallOptions> value) {
      callOptions = value;
      return this;
    }

    /** Records response headers and trailers into {@code value}. */
    public Builder responseMetadata(ResponseMetadata value) {
      responseMetadata = value;
      return this;
    }

    public TransportOptions build() {
      return new TransportOptions(this);
    }
  }
}
