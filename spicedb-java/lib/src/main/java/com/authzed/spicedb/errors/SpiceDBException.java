package com.authzed.spicedb.errors;

import com.google.rpc.ErrorInfo;
import io.grpc.Status;
import io.grpc.StatusException;
import io.grpc.StatusRuntimeException;
import io.grpc.protobuf.StatusProto;
import java.util.Map;

/**
 * Base unchecked exception for all SpiceDB errors.
 *
 * <p>All SpiceDB-specific exceptions extend this class, allowing callers to catch all SpiceDB
 * errors with a single {@code catch (SpiceDBException e)}.
 *
 * <p>Beyond the message, a SpiceDB error carries the server's structured explanation of the failure
 * when the server sent one -- the {@code google.rpc.ErrorInfo} detail attached to the status. That
 * explanation is derived here from the preserved {@code cause}, so it can never drift from the
 * status the exception was built out of. See root DESIGN.md, "RULE: Error mapping must not lose the
 * server's detail".
 */
public class SpiceDBException extends RuntimeException {

  private com.authzed.spicedb.DeleteProgress deleteProgress;

  public com.authzed.spicedb.DeleteProgress deleteProgress() {
    return deleteProgress;
  }

  public void attachDeleteProgress(com.authzed.spicedb.DeleteProgress progress) {
    this.deleteProgress = progress;
  }

  private final String reason;
  private final String reasonDomain;

  // Always assigned from Map.of()/Map.copyOf(), both of which return serializable
  // implementations; only the declared interface type is what -Xlint:serial objects to.
  @SuppressWarnings("serial")
  private final Map<String, String> reasonMetadata;

  private final Status.Code code;
  private final String serverMessage;

  /** Builds an error with code {@code UNKNOWN}; prefer {@link ErrorMapper#newError}. */
  public SpiceDBException(String message) {
    this(Status.Code.UNKNOWN, message, null);
  }

  /**
   * Builds an error from a cause. When the cause chain carries a gRPC status (a server error), that
   * status is this error's code; otherwise the code is {@code UNKNOWN}.
   */
  public SpiceDBException(String message, Throwable cause) {
    this(Status.Code.UNKNOWN, message, cause);
  }

  /**
   * Builds an error with an explicit code. Subclasses pass their own code.
   *
   * <p>If {@code cause}'s chain already holds a gRPC status (the error came from the server or the
   * transport), that status is kept as the cause and is authoritative. Otherwise -- a locally
   * raised or caller-constructed error -- a {@link StatusRuntimeException} with {@code code} and
   * {@code message} is inserted as the direct cause (wrapping {@code cause}, if any), so gRPC's own
   * {@link Status#fromThrowable(Throwable)} and {@link io.grpc.protobuf.StatusProto} report the
   * matching code for every SpiceDB error (root DESIGN.md, "Native errors interoperate with the
   * ecosystem's standard status inspection in both directions").
   */
  protected SpiceDBException(Status.Code code, String message, Throwable cause) {
    super(message, withStatus(code, message, cause));
    Status status = statusIn(getCause());
    this.code = status.getCode();
    this.serverMessage = statusIn(cause) == null ? null : status.getDescription();
    ErrorInfo info = errorInfoOf(getCause());
    this.reason = info == null ? "" : info.getReason();
    this.reasonDomain = info == null ? "" : info.getDomain();
    this.reasonMetadata = info == null ? Map.of() : Map.copyOf(info.getMetadataMap());
  }

  private static Throwable withStatus(Status.Code code, String message, Throwable cause) {
    if (statusIn(cause) != null) return cause;
    return Status.fromCode(code == null ? Status.Code.UNKNOWN : code)
        .withDescription(message)
        .withCause(cause)
        .asRuntimeException();
  }

  /** The first gRPC status in {@code t}'s cause chain, or {@code null}. */
  static Status statusIn(Throwable t) {
    for (Throwable c = t; c != null; c = c.getCause()) {
      if (c instanceof StatusRuntimeException sre) return sre.getStatus();
      if (c instanceof StatusException se) return se.getStatus();
    }
    return null;
  }

  /**
   * The gRPC status code this error corresponds to: the server's code for server errors, and the
   * subclass's code for locally raised or caller-constructed errors (for example {@code
   * INVALID_ARGUMENT} for {@link InvalidArgumentException}). Agrees with {@code
   * Status.fromThrowable(this).getCode()}.
   */
  public Status.Code code() {
    return code;
  }

  /** The gRPC status behind this error, as {@link Status#fromThrowable(Throwable)} reports it. */
  public Status status() {
    return Status.fromThrowable(this);
  }

  /**
   * The status message exactly as received, without any operation or item context this client adds
   * to {@link #getMessage()}. For a server error this is verbatim what SpiceDB said, so a proxy can
   * forward it unchanged. {@code null} for errors raised locally or built by callers.
   */
  public String serverMessage() {
    return serverMessage;
  }

  /**
   * A {@link StatusRuntimeException} equivalent to this error, for code that rethrows to a gRPC
   * layer. Server errors return the original exception (status details and trailers intact).
   */
  public StatusRuntimeException toStatusRuntimeException() {
    for (Throwable c = getCause(); c != null; c = c.getCause())
      if (c instanceof StatusRuntimeException sre) return sre;
    return status().asRuntimeException();
  }

  /**
   * SpiceDB's structured explanation for this failure: the name of an {@code
   * authzed.api.v1.ErrorReason} enum value, e.g. {@code "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"}.
   * Empty when the server attached no {@code ErrorInfo}.
   *
   * <p>The value is surfaced exactly as the server sent it. A reason a newer server knows and this
   * client does not is passed through unchanged rather than coerced or rejected: it is
   * server-supplied, and root DESIGN.md's "RULE: A conversion that cannot preserve meaning must
   * fail" requires server-supplied unknowns to degrade safely rather than throw.
   *
   * @return the reason name, or an empty string
   */
  public String getReason() {
    return reason;
  }

  /**
   * Who produced the reason. SpiceDB uses {@code "authzed.com"}.
   *
   * @return the domain, or an empty string
   */
  public String getReasonDomain() {
    return reasonDomain;
  }

  /**
   * The specifics behind the reason -- which precondition failed, what depth limit was hit. Empty
   * when the server attached no {@code ErrorInfo}.
   *
   * @return an immutable map of reason metadata
   */
  public Map<String, String> getReasonMetadata() {
    return reasonMetadata;
  }

  /**
   * Returns the {@code google.rpc.ErrorInfo} detail carried by {@code cause}, or {@code null}.
   *
   * <p>{@link StatusProto#fromThrowable} does the trailer lookup and decoding, so this reads the
   * structured status gRPC already parsed rather than anything reconstructed from a message.
   * Details of other types are skipped, so an unfamiliar detail never hides the familiar one, and a
   * detail that will not decode yields {@code null} rather than costing the caller a typed
   * exception.
   */
  private static ErrorInfo errorInfoOf(Throwable cause) {
    com.google.rpc.Status status = StatusProto.fromThrowable(cause);
    if (status == null) {
      return null;
    }
    for (com.google.protobuf.Any detail : status.getDetailsList()) {
      if (detail.is(ErrorInfo.class)) {
        try {
          return detail.unpack(ErrorInfo.class);
        } catch (com.google.protobuf.InvalidProtocolBufferException e) {
          return null;
        }
      }
    }
    return null;
  }
}
