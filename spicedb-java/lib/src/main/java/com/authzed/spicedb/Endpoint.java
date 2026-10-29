package com.authzed.spicedb;

import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.LocalValidationException;
import io.grpc.CallOptions;
import io.grpc.Channel;
import io.grpc.ClientCall;
import io.grpc.ClientInterceptor;
import io.grpc.ForwardingClientCall;
import io.grpc.ForwardingClientCallListener;
import io.grpc.Metadata;
import io.grpc.MethodDescriptor;
import io.grpc.Status;
import java.util.Locale;

/**
 * An endpoint string parsed into the target the transport dials. See root DESIGN.md, "Accept the
 * endpoint forms deployments use, or reject them at construction".
 *
 * <p>Accepted forms:
 *
 * <ul>
 *   <li>{@code host:port} (also {@code [v6]:port}) -- the constructor picks plaintext or TLS; a
 *       bare host (or IPv6 literal) without a port is TLS on 443, and a plaintext constructor
 *       rejects it, so nothing silently dials a default plaintext port;
 *   <li>{@code http://host:port} -- plaintext; the port is required, so nothing silently dials 80;
 *   <li>{@code https://host[:port]} -- TLS; the port defaults to 443;
 *   <li>{@code dns:///host[:port]} -- gRPC's DNS target, which resolves every address of the host
 *       for client-side load balancing; the port rule is that of {@code host:port}, and the
 *       loopback check runs on the host;
 *   <li>{@code unix:path} or {@code unix:///absolute/path} -- a Unix domain socket, passed through
 *       to gRPC (it needs a native Netty transport: epoll on Linux).
 * </ul>
 *
 * <p>Anything else (another scheme, a path, query, fragment or userinfo, a non-numeric or
 * out-of-range port) fails with an {@link InvalidArgumentException} naming the problem, before any
 * transport or remote-plaintext check runs.
 */
record Endpoint(String raw, String target, Scheme scheme, boolean portless) {

  enum Scheme {
    /** {@code host:port}: the constructor chooses plaintext or TLS. */
    NONE,
    /** {@code http://}: plaintext. */
    HTTP,
    /** {@code https://}: TLS. */
    HTTPS,
    /** {@code unix:}: a Unix domain socket. */
    UNIX,
    /** {@code dns:///}: gRPC's DNS target; the constructor chooses plaintext or TLS. */
    DNS
  }

  static final String FORMS =
      "expected host:port, http://host:port (plaintext), https://host[:port] (TLS),"
          + " dns:///host:port or unix:/path/to/socket";

  private static final String DNS_PREFIX = "dns:///";

  static InvalidArgumentException invalid(String endpoint, String reason) {
    return new LocalValidationException(
        "spicedb: invalid endpoint \"" + endpoint + "\": " + reason + "; " + FORMS);
  }

  static Endpoint parse(String endpoint) {
    if (endpoint == null || endpoint.isEmpty())
      throw new LocalValidationException("spicedb: endpoint must not be empty; " + FORMS);
    if (endpoint.startsWith("unix:")) {
      if (endpoint.length() == "unix:".length()) throw invalid(endpoint, "missing socket path");
      return new Endpoint(endpoint, endpoint, Scheme.UNIX, false);
    }
    if (endpoint.regionMatches(true, 0, "dns:", 0, 4)) {
      if (!endpoint.regionMatches(true, 0, DNS_PREFIX, 0, DNS_PREFIX.length()))
        throw invalid(
            endpoint,
            "a dns: target must be dns:///host:port (a DNS server authority is not supported)");
      HostPort hp = checkAuthority(endpoint, endpoint.substring(DNS_PREFIX.length()));
      if (hp.host.indexOf(':') >= 0 && !hp.bracketed)
        throw invalid(endpoint, "an IPv6 address in a dns: target must be bracketed, as in [::1]");
      String host = hp.bracketed ? "[" + hp.host + "]" : hp.host;
      if (hp.port == null) return new Endpoint(endpoint, DNS_PREFIX + host, Scheme.DNS, true);
      return new Endpoint(endpoint, DNS_PREFIX + host + ":" + hp.port, Scheme.DNS, false);
    }

    int sep = endpoint.indexOf("://");
    if (sep < 0) {
      HostPort hp = checkAuthority(endpoint, endpoint);
      if (hp.port != null) return new Endpoint(endpoint, endpoint, Scheme.NONE, false);
      // No port: the transport decides (see forTransport). Bracket an IPv6 literal.
      String host = hp.host.indexOf(':') >= 0 ? "[" + hp.host + "]" : hp.host;
      return new Endpoint(endpoint, host, Scheme.NONE, true);
    }

    String schemeText = endpoint.substring(0, sep);
    Scheme scheme =
        switch (schemeText.toLowerCase(Locale.ROOT)) {
          case "http" -> Scheme.HTTP;
          case "https" -> Scheme.HTTPS;
          default -> throw invalid(endpoint, "unsupported scheme \"" + schemeText + "\"");
        };
    String authority = endpoint.substring(sep + 3);
    // One trailing '/' is accepted: deployment URLs often carry one.
    if (authority.endsWith("/")) authority = authority.substring(0, authority.length() - 1);
    HostPort hp = checkAuthority(endpoint, authority);
    if (hp.host.indexOf(':') >= 0 && !hp.bracketed)
      throw invalid(endpoint, "an IPv6 address in a URL must be bracketed, as in [::1]");
    String port = hp.port;
    if (port == null) {
      if (scheme == Scheme.HTTP)
        throw invalid(endpoint, "an http:// endpoint needs an explicit port");
      port = "443";
    }
    String host = hp.bracketed ? "[" + hp.host + "]" : hp.host;
    return new Endpoint(endpoint, host + ":" + port, scheme, false);
  }

  /**
   * What the loopback check judges: {@code host:port} without a {@code dns:///} prefix, or the
   * target itself.
   */
  String loopbackTarget() {
    return scheme == Scheme.DNS ? target.substring(DNS_PREFIX.length()) : target;
  }

  private record HostPort(String host, String port, boolean bracketed) {}

  /** Validates {@code authority} (host, optional port) and splits it. */
  private static HostPort checkAuthority(String endpoint, String authority) {
    for (int i = 0; i < authority.length(); i++) {
      char c = authority.charAt(i);
      if (c == '@') throw invalid(endpoint, "userinfo (user@) is not allowed");
      if (c == '/') throw invalid(endpoint, "a path is not allowed");
      if (c == '?') throw invalid(endpoint, "a query is not allowed");
      if (c == '#') throw invalid(endpoint, "a fragment is not allowed");
      if (Character.isWhitespace(c)) throw invalid(endpoint, "whitespace is not allowed");
    }
    if (authority.isEmpty()) throw invalid(endpoint, "missing host");

    String host;
    String port;
    boolean bracketed = false;
    if (authority.startsWith("[")) {
      int close = authority.indexOf(']');
      if (close < 0) throw invalid(endpoint, "unterminated IPv6 address");
      host = authority.substring(1, close);
      String rest = authority.substring(close + 1);
      if (rest.isEmpty()) port = null;
      else if (rest.startsWith(":")) port = rest.substring(1);
      else throw invalid(endpoint, "unexpected text after the IPv6 address");
      bracketed = true;
    } else if (authority.indexOf(':') != authority.lastIndexOf(':')) {
      // A bare IPv6 literal such as "::1": no port.
      return new HostPort(authority, null, false);
    } else {
      int colon = authority.lastIndexOf(':');
      host = colon < 0 ? authority : authority.substring(0, colon);
      port = colon < 0 ? null : authority.substring(colon + 1);
    }
    if (host.isEmpty()) throw invalid(endpoint, "missing host");
    if (port != null && !validPort(port))
      throw invalid(endpoint, "invalid port \"" + port + "\" (must be a number from 1 to 65535)");
    return new HostPort(host, port, bracketed);
  }

  private static boolean validPort(String port) {
    if (port.isEmpty() || port.length() > 5) return false;
    for (int i = 0; i < port.length(); i++) if (!Character.isDigit(port.charAt(i))) return false;
    int value = Integer.parseInt(port);
    return value >= 1 && value <= 65535;
  }

  /**
   * Fails when the URL scheme disagrees with the constructor's transport, or when a plaintext
   * endpoint has no port; returns the endpoint with a port-less TLS host dialing 443.
   */
  Endpoint forTransport(boolean plaintext, String tlsConstructor, String plaintextConstructor) {
    if (plaintext && scheme == Scheme.HTTPS)
      throw invalid(
          raw, "https:// selects TLS but this is a plaintext constructor; use " + tlsConstructor);
    if (!plaintext && scheme == Scheme.HTTP)
      throw invalid(
          raw,
          "http:// selects plaintext but this is a TLS constructor; use " + plaintextConstructor);
    if (!portless) return this;
    if (plaintext)
      throw invalid(raw, "a plaintext endpoint needs an explicit port, e.g. " + target + ":50051");
    return new Endpoint(raw, target + ":443", scheme, false);
  }

  /**
   * Rewrites transport-local connection failures (UNAVAILABLE with a local cause) so their message
   * names the endpoint and the root cause, which gRPC otherwise hides behind "io exception". Status
   * codes and causes are unchanged, so retry classification and {@code instanceof} checks still see
   * the same error.
   */
  ClientInterceptor connectionErrorInterceptor() {
    String shown = raw;
    return new ClientInterceptor() {
      @Override
      public <Q, R> ClientCall<Q, R> interceptCall(
          MethodDescriptor<Q, R> method, CallOptions callOptions, Channel next) {
        return new ForwardingClientCall.SimpleForwardingClientCall<>(
            next.newCall(method, callOptions)) {
          @Override
          public void start(Listener<R> listener, Metadata headers) {
            super.start(
                new ForwardingClientCallListener.SimpleForwardingClientCallListener<>(listener) {
                  @Override
                  public void onClose(Status status, Metadata trailers) {
                    super.onClose(describeConnectionFailure(shown, status), trailers);
                  }
                },
                headers);
          }
        };
      }
    };
  }

  static Status describeConnectionFailure(String endpoint, Status status) {
    // A status the server sent has no local cause; only transport failures carry one.
    if (status.getCode() != Status.Code.UNAVAILABLE || status.getCause() == null) return status;
    Throwable root = status.getCause();
    while (root.getCause() != null && root.getCause() != root) root = root.getCause();
    String rootText =
        root.getMessage() == null
            ? root.getClass().getSimpleName()
            : root.getClass().getSimpleName() + ": " + root.getMessage();
    String description = status.getDescription();
    // Netty appends the channel pipeline to "io exception"; it says nothing to a caller.
    if (description != null) {
      int pipeline = description.indexOf("Channel Pipeline:");
      if (pipeline >= 0) description = description.substring(0, pipeline).strip();
    }
    return status.withDescription(
        "connecting to SpiceDB at \""
            + endpoint
            + "\" failed: "
            + (description == null || description.isEmpty() ? "" : description + ": ")
            + rootText);
  }
}
