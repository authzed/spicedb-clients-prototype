package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;

/**
 * Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server".
 *
 * <p>Before this class, {@code spicedb-java} had <b>no TLS test at any tier</b> — a grep for {@code
 * Tls|TLS|useTransportSecurity|handshake} across {@code lib/src/test/} returned nothing. The
 * sibling clients that did have one mostly asserted that a lazily-connecting constructor returned
 * non-null, which passes with an empty trust store; this client asserted nothing at all.
 *
 * <p>Gated with {@link EnabledIfEnvironmentVariable} because it needs the network. JUnit reports a
 * disabled test as <b>skipped</b> rather than passed, which is what makes the CI step's check
 * honest: a gate that reported "passed" while doing nothing would reproduce this rule's own failure
 * mode one layer up (clause 3).
 */
@EnabledIfEnvironmentVariable(named = "SPICEDB_TLS_INTEGRATION", matches = "1")
class SystemTlsHandshakeTest {

  private static final String ENDPOINT = "grpc.authzed.com:443";

  /**
   * Substrings of a failure that happened <em>before</em> any server could answer.
   *
   * <p>Matched as prose rather than by status code on purpose: gRPC surfaces a failed TLS handshake
   * and a live server's "no healthy upstream" alike, so the status cannot discriminate between "the
   * trust store is empty" and "the server replied". The message can.
   */
  private static final List<String> TRUST_STORE_FAILURE_SIGNATURES =
      List.of(
          "sslhandshakeexception",
          "unable to find valid certification path",
          "pkix path building failed",
          "certificate",
          "certpathbuilderexception");

  /**
   * Substrings meaning the endpoint was never reached at all. Kept separate from the trust-store
   * signatures so a network outage in CI reports as an outage rather than as a TLS regression.
   */
  private static final List<String> UNREACHABLE_SIGNATURES =
      List.of(
          "unknownhostexception",
          "connection refused",
          "no route to host",
          "network is unreachable");

  /**
   * Drives {@link SpiceDBClient#createSystemTls} against a real public endpoint and requires the
   * TLS handshake to complete.
   *
   * <p>Any gRPC reply proves the handshake completed: producing one at all requires the far side to
   * have accepted our TLS session and spoken HTTP/2 back. As of writing an unauthenticated caller
   * gets {@code UNAVAILABLE "no healthy upstream"} from Authzed's edge rather than {@code
   * UNAUTHENTICATED}, so pinning a status code here would assert a deployment detail of someone
   * else's service. What gets pinned is the distinction the rule cares about: did we reach a
   * server, or did we fail on trust material.
   */
  @Test
  void createSystemTls_completesRealHandshake() {
    SpiceDBClient client =
        SpiceDBClient.createSystemTls(ENDPOINT, "not-a-real-token", Duration.ofSeconds(30));
    String detail;
    try {
      // ManagedChannelBuilder connects lazily, so the constructor proves nothing by itself. This
      // RPC is what forces the connection, and with it the handshake -- clause 2: "where the
      // constructor is lazy, force the connection inside the test".
      client.readSchema();
      return; // a successful RPC is, a fortiori, a completed handshake
    } catch (Exception e) {
      // The whole chain: grpc-java wraps the SSL failure in a StatusRuntimeException cause, and
      // this client maps that again, so the certificate detail sits several levels below the
      // message on top.
      detail = flatten(e).toLowerCase(Locale.ROOT);
    } finally {
      client.close();
    }

    for (String signature : TRUST_STORE_FAILURE_SIGNATURES) {
      assertFalse(
          detail.contains(signature),
          () ->
              "system TLS handshake failed -- the JDK truststore (cacerts) is probably not loaded,"
                  + " or the client is supplying its own (empty) root set: "
                  + detail);
    }
    for (String signature : UNREACHABLE_SIGNATURES) {
      assertFalse(
          detail.contains(signature),
          () ->
              "could not reach "
                  + ENDPOINT
                  + " at all: this is a network problem, not a TLS result, and says nothing about"
                  + " the truststore: "
                  + detail);
    }
  }

  private static String flatten(Throwable t) {
    List<String> parts = new ArrayList<>();
    for (Throwable current = t; current != null; current = current.getCause()) {
      parts.add(current.getClass().getSimpleName() + ": " + current.getMessage());
      if (current.getCause() == current) {
        break;
      }
    }
    return String.join(" <- ", parts);
  }
}
