package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.errors.InvalidArgumentException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * Demonstrates the opt-in a plaintext connection to a remote host requires -- see root DESIGN.md,
 * "RULE: Credentials over insecure transport require an explicit opt-in".
 *
 * <p>The failure this rule exists to prevent is mundane and common: a developer copies a plaintext
 * constructor out of a localhost example into a staging config, and a long-lived SpiceDB token -- a
 * complete authorization bypass in anyone else's hands -- goes onto the wire in cleartext with
 * nothing signalling that it happened. So {@code createPlaintext} is loopback-only, and reaching a
 * remote host over plaintext takes a second, separately-named argument the caller cannot supply by
 * accident.
 *
 * <p>The sharpest case is the last one. The rule requires the guard's answer to be <em>the
 * transport's</em> answer -- here {@code URI.create("//" + name)}, what grpc-java's {@code
 * DnsNameResolver} uses -- rather than a hand-rolled string split. Given {@code
 * 127.0.0.1:443@evil.com}, a last-colon split reads the host as {@code 127.0.0.1} and waves it
 * through, while a URI parser reads {@code 127.0.0.1:443} as <em>userinfo</em> and the authority as
 * {@code evil.com}.
 */
class InsecureOptInTest {

  @Test
  void loopbackPlaintextNeedsNoOptIn() {
    // The case the rule deliberately leaves ergonomic: a token on a loopback socket never leaves
    // the machine, so requiring ceremony here would only train developers to reach for the opt-in
    // reflexively.
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext(
            SpiceDBIntegrationTest.ENDPOINT, SpiceDBIntegrationTest.TOKEN)) {
      // Prove the client is usable, not merely constructed: the channel connects lazily, so a
      // constructor returning a client that could not talk to anything would still satisfy a
      // "did not throw" assertion.
      //
      // The schema below is narrower than the shared one, and every example runs against the same
      // SpiceDB. SpiceDB refuses a WriteSchema that drops a relation while a relationship still
      // exists under it, so clear first -- an earlier example leaving document:report#editor
      // behind is enough to fail this outright, which is exactly how it failed the first time.
      SpiceDBIntegrationTest.clearDocumentRelationships(client);
      client.writeSchema(
          """
            definition user {}

            definition document {
                relation viewer: user
                permission view = viewer
            }""");
    }
  }

  @Test
  void remotePlaintextIsRefusedWithoutTheOptIn() {
    // No connection is attempted: the refusal happens during construction, so the token never
    // reaches a socket. This is not about whether the host exists -- example.com is refused
    // because it is not loopback, full stop.
    assertThatThrownBy(
            () -> SpiceDBClient.createPlaintext("example.com:50051", SpiceDBIntegrationTest.TOKEN))
        .as("SECURITY: a bearer token was accepted for cleartext delivery to a non-loopback host")
        // This client's own typed argument error, the same one a filter the wire cannot express
        // uses -- not a language-native IllegalArgumentException. Root DESIGN.md, "RULE:
        // Credentials over insecure transport require an explicit opt-in", clause 4.
        .isInstanceOf(InvalidArgumentException.class)
        .hasMessageContaining("example.com:50051");
  }

  @Test
  void remotePlaintextIsAllowedWithTheNamedOptIn() {
    // Two arguments, not one, and that separation is the point: selecting the plaintext transport
    // and accepting the credential exposure that follows are different decisions, and clause 1
    // forbids one boolean from doing both jobs.
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("example.com:50051", SpiceDBIntegrationTest.TOKEN, true)) {
      assertThat(client).isNotNull();
    }
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "127.0.0.1:443@evil.com",
        "127.0.0.1:50051/../evil.com",
        "127.0.0.1:50051?x=evil.com",
        "127.0.0.1:50051#evil.com",
      })
  void authorityMovingEndpointsAreRefused(String endpoint) {
    // Fail closed on anything whose authority could move under URI parsing. A client that split on
    // the last colon would call 127.0.0.1:443@evil.com loopback and hand the token to evil.com.
    assertThatThrownBy(() -> SpiceDBClient.createPlaintext(endpoint, SpiceDBIntegrationTest.TOKEN))
        .as("SECURITY: %s was accepted as loopback", endpoint)
        .isInstanceOf(InvalidArgumentException.class);
  }
}
