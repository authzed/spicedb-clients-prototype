package com.authzed.spicedb.errors;

import com.google.rpc.ErrorInfo;
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

  private final String reason;
  private final String reasonDomain;

  // Always assigned from Map.of()/Map.copyOf(), both of which return serializable
  // implementations; only the declared interface type is what -Xlint:serial objects to.
  @SuppressWarnings("serial")
  private final Map<String, String> reasonMetadata;

  public SpiceDBException(String message) {
    super(message);
    this.reason = "";
    this.reasonDomain = "";
    this.reasonMetadata = Map.of();
  }

  public SpiceDBException(String message, Throwable cause) {
    super(message, cause);
    ErrorInfo info = errorInfoOf(cause);
    this.reason = info == null ? "" : info.getReason();
    this.reasonDomain = info == null ? "" : info.getDomain();
    this.reasonMetadata = info == null ? Map.of() : Map.copyOf(info.getMetadataMap());
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
