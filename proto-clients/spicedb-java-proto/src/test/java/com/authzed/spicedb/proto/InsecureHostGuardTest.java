package com.authzed.spicedb.proto;

import build.buf.gen.authzed.api.v1.CheckPermissionRequest;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import io.grpc.Metadata;
import io.grpc.Server;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;

import java.io.IOException;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Regression tests for root DESIGN.md, "RULE: Credentials over insecure transport require an
 * explicit opt-in".
 */
class InsecureHostGuardTest {

    @Test
    void isLoopbackEndpointTrueForLoopbackTargets() {
        String[] loopback = {
                "localhost:50051", "LOCALHOST:50051", "localhost",
                "127.0.0.1:50051", "127.0.0.1", "127.55.66.77:50051",
                "[::1]:50051", "::1",
                "unix:/var/run/spicedb.sock", "unix:///var/run/spicedb.sock",
        };
        for (String endpoint : loopback) {
            assertTrue(SpiceDBProtoClient.isLoopbackEndpoint(endpoint), endpoint);
        }
    }

    @Test
    void isLoopbackEndpointFalseForNonLoopbackTargets() {
        String[] notLoopback = {
                "example.com:443", "staging.internal:443",
                "10.0.0.5:50051", "8.8.8.8:443", "0.0.0.0:50051",
                // Typosquats/lookalikes: a future refactor toward String#contains
                // or String#endsWith on "localhost"/"127.0.0.1" would wrongly
                // treat these as loopback and reopen a credential leak. Must stay
                // non-loopback under exact-match host comparison.
                "localhost.evil.com:443", "127.0.0.1.evil.com:443", "evil-localhost:443",
        };
        for (String endpoint : notLoopback) {
            assertFalse(SpiceDBProtoClient.isLoopbackEndpoint(endpoint), endpoint);
        }
    }

    /**
     * Endpoints whose URI authority is not what a naive host:port split reads out of them.
     * grpc-java's {@code DnsNameResolver} takes its host from
     * {@code URI.create("//" + name).getHost()}, so {@code "127.0.0.1:443@evil.com"} resolves and
     * connects to <b>evil.com</b> while a last-colon split sees "127.0.0.1". Before the fix
     * {@link SpiceDBProtoClient#isLoopbackEndpoint} returned true for these, so an insecure client
     * was built with no opt-in and shipped its bearer token to the attacker-controlled host in
     * cleartext.
     */
    private static final String[] AUTHORITY_SHIFTING_ENDPOINTS = {
            "127.0.0.1:443@evil.com",
            "[::1]:443@evil.com",
            "[::1]:0@127.0.0.1:19999",
            "[localhost]:1@127.0.0.1:19999",
    };

    @Test
    void isLoopbackEndpointFalseForAuthorityShiftingTargets() {
        for (String endpoint : AUTHORITY_SHIFTING_ENDPOINTS) {
            assertFalse(SpiceDBProtoClient.isLoopbackEndpoint(endpoint), endpoint);
        }
        // Other endpoints whose parse a manual split can disagree with: userinfo with no port,
        // path/query/fragment, a trailing dot, embedded whitespace. All must fail closed.
        String[] alsoRefused = {
                "localhost@evil.com", "localhost/../evil.com", "localhost#@evil.com",
                "localhost?@evil.com", "localhost.", "localhost :50051", "127.0.0.1 :50051",
        };
        for (String endpoint : alsoRefused) {
            assertFalse(SpiceDBProtoClient.isLoopbackEndpoint(endpoint), endpoint);
        }
    }

    /**
     * The regression test for the loopback-guard bypass. Asserting only that the constructor throws
     * would be satisfied by an implementation that builds the channel, sends the token, and throws
     * afterwards -- so this asserts on the transport instead, exactly as
     * {@link #refusesInsecureNonLoopbackWithoutOptIn} does: the channel builder handed in is wired
     * to a REAL capturing server, and capturedAuth staying empty is what proves nothing capable of
     * carrying the credential was ever built or dialed.
     */
    @ParameterizedTest
    @MethodSource("authorityShiftingEndpoints")
    void refusesEndpointWhoseUriAuthorityShiftsTheHost(String endpoint) throws Exception {
        try (CapturingServer server = new CapturingServer("guard-bypass-" + endpoint.hashCode())) {
            IllegalArgumentException ex =
                    assertThrows(
                            IllegalArgumentException.class,
                            () ->
                                    new SpiceDBProtoClient(
                                            endpoint,
                                            "super-secret-token",
                                            true,
                                            false,
                                            server.channelBuilder()));

            assertTrue(ex.getMessage().contains(endpoint), ex.getMessage());
            assertTrue(ex.getMessage().contains("allowInsecureRemoteCredentials"), ex.getMessage());

            assertNull(
                    server.capturedAuth.poll(200, TimeUnit.MILLISECONDS),
                    "server must never have observed a call, so nothing should be captured");
        }
    }

    static String[] authorityShiftingEndpoints() {
        return AUTHORITY_SHIFTING_ENDPOINTS;
    }

    private static final Metadata.Key<String> AUTHORIZATION_KEY =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);

    /**
     * Starts a real in-process gRPC server that records the "authorization" metadata it observes
     * on every call (via a server-level interceptor, so it fires before any handler -- even an
     * unimplemented one) and closes every call with UNIMPLEMENTED. {@link #channelBuilder()}
     * hands out a fresh in-process channel builder to the SAME server -- production code decides
     * whether that builder is ever built and used at all.
     */
    private static final class CapturingServer implements AutoCloseable {
        final Server server;
        final String name;
        final LinkedBlockingQueue<String> capturedAuth = new LinkedBlockingQueue<>();

        CapturingServer(String name) throws IOException {
            this.name = name;
            ServerInterceptor interceptor = new ServerInterceptor() {
                @Override
                public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
                        ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
                    capturedAuth.add(headers.get(AUTHORIZATION_KEY));
                    call.close(Status.UNIMPLEMENTED, new Metadata());
                    return new ServerCall.Listener<ReqT>() {};
                }
            };
            this.server =
                    InProcessServerBuilder.forName(name)
                            .directExecutor()
                            .addService(new PermissionsServiceGrpc.PermissionsServiceImplBase() {})
                            .intercept(interceptor)
                            .build()
                            .start();
        }

        InProcessChannelBuilder channelBuilder() {
            return InProcessChannelBuilder.forName(name).directExecutor();
        }

        @Override
        public void close() {
            server.shutdownNow();
        }
    }

    private static void attemptCheckPermission(SpiceDBProtoClient client) {
        try {
            client.permissions().checkPermission(CheckPermissionRequest.getDefaultInstance());
        } catch (StatusRuntimeException e) {
            // Expected -- the fake server always closes with UNIMPLEMENTED. Only the captured
            // metadata matters to these tests, not the RPC's outcome.
        }
    }

    /**
     * The regression test: a rejected combination must never reach the point of building or using
     * the channel that would carry the credential -- proving the token never reaches anything
     * capable of putting it on the wire, not merely that an exception was raised. The channel
     * builder passed in here is wired to a REAL capturing server exactly as
     * loopbackAllowsInsecureWithNoOptInAndSendsToken's is; capturedAuth staying empty after the
     * constructor throws is what proves the guard fired before that channel was ever built or
     * dialed, not merely that it eventually threw. An implementation that built the channel, sent
     * the token, and only THEN threw would still fail a bare assertThrows check but would fail
     * this one, since the server would have recorded the token.
     */
    @Test
    void refusesInsecureNonLoopbackWithoutOptIn() throws Exception {
        try (CapturingServer server = new CapturingServer("insecure-host-guard-refuse")) {
            IllegalArgumentException ex =
                    assertThrows(
                            IllegalArgumentException.class,
                            () ->
                                    new SpiceDBProtoClient(
                                            "evil.example.com:1234",
                                            "super-secret-token",
                                            true,
                                            false,
                                            server.channelBuilder()));

            assertTrue(ex.getMessage().contains("evil.example.com:1234"), ex.getMessage());
            assertTrue(ex.getMessage().contains("allowInsecureRemoteCredentials"), ex.getMessage());

            assertNull(
                    server.capturedAuth.poll(200, TimeUnit.MILLISECONDS),
                    "server must never have observed a call, so nothing should be captured");
        }
    }

    @Test
    void loopbackAllowsInsecureWithNoOptInAndSendsToken() throws Exception {
        try (CapturingServer server = new CapturingServer("insecure-host-guard-loopback")) {
            try (SpiceDBProtoClient client =
                    new SpiceDBProtoClient(
                            "localhost:50051", "test-token", true, false, server.channelBuilder())) {
                attemptCheckPermission(client);
            }

            String got = server.capturedAuth.poll(5, TimeUnit.SECONDS);
            assertEquals("Bearer test-token", got);
        }
    }

    @Test
    void allowInsecureRemoteCredentialsSendsTokenToNonLoopback() throws Exception {
        try (CapturingServer server = new CapturingServer("insecure-host-guard-optin")) {
            try (SpiceDBProtoClient client =
                    new SpiceDBProtoClient(
                            "evil.example.com:1234",
                            "remote-token",
                            true,
                            true,
                            server.channelBuilder())) {
                attemptCheckPermission(client);
            }

            String got = server.capturedAuth.poll(5, TimeUnit.SECONDS);
            assertEquals("Bearer remote-token", got);
        }
    }
}
