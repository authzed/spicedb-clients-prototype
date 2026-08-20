package com.authzed.spicedb.proto;

import io.grpc.CallCredentials;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;

import java.net.InetAddress;
import java.net.URI;
import java.net.UnknownHostException;
import java.util.concurrent.Executor;
import java.util.concurrent.TimeUnit;
import java.util.regex.Pattern;

import build.buf.gen.authzed.api.v1.ExperimentalServiceGrpc;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.SchemaServiceGrpc;
import build.buf.gen.authzed.api.v1.WatchServiceGrpc;

/**
 * Wraps all generated gRPC service stubs for SpiceDB.
 *
 * <p>Manages a single {@link ManagedChannel} and exposes blocking stubs for each
 * SpiceDB service. Implements {@link AutoCloseable} so the channel is shut down
 * when the client is closed.
 */
public class SpiceDBProtoClient implements AutoCloseable {

    private static final Metadata.Key<String> AUTHORIZATION_KEY =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);

    private static final Pattern IPV4_LITERAL = Pattern.compile("^\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}$");

    private final ManagedChannel channel;
    private final PermissionsServiceGrpc.PermissionsServiceBlockingStub permissionsStub;
    private final SchemaServiceGrpc.SchemaServiceBlockingStub schemaStub;
    private final WatchServiceGrpc.WatchServiceBlockingStub watchStub;
    private final ExperimentalServiceGrpc.ExperimentalServiceBlockingStub experimentalStub;

    /**
     * Creates a new SpiceDB proto client.
     *
     * @param endpoint the gRPC endpoint (host:port)
     * @param token    the bearer token for authentication
     * @param insecure if true, use plaintext (no TLS); for testing only
     */
    public SpiceDBProtoClient(String endpoint, String token, boolean insecure) {
        this(endpoint, token, insecure, false);
    }

    /**
     * Creates a new SpiceDB proto client.
     *
     * @param endpoint                       the gRPC endpoint (host:port)
     * @param token                          the bearer token for authentication
     * @param insecure                       if true, use plaintext (no TLS); for testing only. By
     *                                        itself, this only permits a plaintext connection to a
     *                                        loopback endpoint (localhost, 127.0.0.0/8, ::1, or a
     *                                        unix socket target) -- see root DESIGN.md, "RULE:
     *                                        Credentials over insecure transport require an
     *                                        explicit opt-in".
     * @param allowInsecureRemoteCredentials explicit, separately named opt-in required before
     *                                        {@code insecure} may be combined with a non-loopback
     *                                        {@code endpoint}. Named and separate from {@code
     *                                        insecure} on purpose: a reader must not be able to
     *                                        mistake it for a default.
     * @throws IllegalArgumentException if {@code insecure} is true, {@code endpoint} is not
     *                                   loopback, and {@code allowInsecureRemoteCredentials} is
     *                                   false -- thrown before any channel or credential is created.
     */
    public SpiceDBProtoClient(
            String endpoint, String token, boolean insecure, boolean allowInsecureRemoteCredentials) {
        this(endpoint, token, insecure, allowInsecureRemoteCredentials, null);
    }

    /**
     * Test-only seam: as the public constructor above, but lets a caller (the test source set)
     * override the underlying {@link ManagedChannelBuilder} -- e.g. with an in-process transport
     * -- while {@code endpoint} (what the guard above actually evaluates) stays independent and
     * can be an arbitrary non-loopback literal. Package-private: not part of the public API.
     */
    SpiceDBProtoClient(
            String endpoint,
            String token,
            boolean insecure,
            boolean allowInsecureRemoteCredentials,
            ManagedChannelBuilder<?> testChannelBuilder) {
        // See root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
        // opt-in". This is the guard for BearerTokenCredentials below: grpc-java's CallCredentials
        // contract has no built-in "refuse over an insecure channel" check the way some other
        // language bindings do, so nothing else here stops a bearer token from reaching an
        // arbitrary insecure host. Refuse before any channel, credential, or stub is created.
        if (insecure && !allowInsecureRemoteCredentials && !isLoopbackEndpoint(endpoint)) {
            throw new IllegalArgumentException(
                    "spicedb: refusing to send credentials over an insecure (plaintext) connection to "
                            + "non-loopback endpoint \"" + endpoint + "\": use TLS (insecure=false), or "
                            + "pass allowInsecureRemoteCredentials=true if you intend to send a bearer "
                            + "token in cleartext to a remote host");
        }

        ManagedChannelBuilder<?> builder =
                testChannelBuilder != null ? testChannelBuilder : ManagedChannelBuilder.forTarget(endpoint);
        if (insecure) {
            builder.usePlaintext();
        }
        this.channel = builder.build();

        CallCredentials callCredentials = new BearerTokenCredentials(token);

        this.permissionsStub = PermissionsServiceGrpc.newBlockingStub(channel)
                .withCallCredentials(callCredentials);
        this.schemaStub = SchemaServiceGrpc.newBlockingStub(channel)
                .withCallCredentials(callCredentials);
        this.watchStub = WatchServiceGrpc.newBlockingStub(channel)
                .withCallCredentials(callCredentials);
        this.experimentalStub = ExperimentalServiceGrpc.newBlockingStub(channel)
                .withCallCredentials(callCredentials);
    }

    /**
     * Returns the blocking stub for the PermissionsService.
     */
    public PermissionsServiceGrpc.PermissionsServiceBlockingStub permissions() {
        return permissionsStub;
    }

    /**
     * Returns the blocking stub for the SchemaService.
     */
    public SchemaServiceGrpc.SchemaServiceBlockingStub schema() {
        return schemaStub;
    }

    /**
     * Returns the blocking stub for the WatchService.
     */
    public WatchServiceGrpc.WatchServiceBlockingStub watch() {
        return watchStub;
    }

    /**
     * Returns the blocking stub for the ExperimentalService.
     */
    public ExperimentalServiceGrpc.ExperimentalServiceBlockingStub experimental() {
        return experimentalStub;
    }

    /**
     * Returns the underlying {@link ManagedChannel}.
     */
    public ManagedChannel getChannel() {
        return channel;
    }

    /**
     * Shuts down the underlying channel, waiting up to 5 seconds for graceful
     * termination.
     */
    @Override
    public void close() {
        channel.shutdown();
        try {
            if (!channel.awaitTermination(5, TimeUnit.SECONDS)) {
                channel.shutdownNow();
            }
        } catch (InterruptedException e) {
            channel.shutdownNow();
            Thread.currentThread().interrupt();
        }
    }

    /**
     * Reports whether the connection this client would actually open for {@code endpoint}
     * terminates on a loopback destination: the literal hostname "localhost", an IP in
     * 127.0.0.0/8, the IPv6 loopback ::1, or a unix domain socket target (a "unix:" prefix). A
     * unix socket never leaves the host's kernel, so it is loopback for this check even though it
     * has no IP at all.
     *
     * <p>That wording is deliberate. This method does not answer "does this string look like it
     * names a loopback host"; it answers "will the transport dial loopback". Those are the same
     * question only if this method and the transport agree on where the host ends and the rest of
     * the target begins -- and a hand-rolled string split will always disagree with a URI parser
     * somewhere. It used to: given {@code "127.0.0.1:443@evil.com"} a last-colon split yields host
     * "127.0.0.1" and reports loopback, while grpc-java's {@code DnsNameResolver} derives its host
     * as {@code URI.create("//" + name).getHost()} -- which reads "127.0.0.1:443" as URI
     * <i>userinfo</i> and returns "evil.com", then resolves and connects there on the default port
     * (an RPC against {@code "127.0.0.1:443@evil.invalid"} fails with "Unable to resolve host
     * evil.invalid", naming the host it actually went looking for). So the bearer token went to
     * evil.com in cleartext with this method reporting "loopback", and the {@code :authority}
     * header carried the whole undivided string, hiding it.
     *
     * <p>So the host is derived with the same {@code URI.create("//" + …).getHost()} expression
     * {@code DnsNameResolver} itself uses. There is one parse, so guard and transport cannot
     * disagree. Before that, anything that could move the authority under URI parsing ({@code @},
     * {@code /}, {@code ?}, {@code #}, whitespace) is refused outright: a legitimate SpiceDB target
     * contains none of those, and failing closed on a weird endpoint is the correct trade for a
     * credential leak.
     *
     * <p>This is the exemption in root DESIGN.md, "RULE: Credentials over insecure transport
     * require an explicit opt-in": loopback is the reason {@code insecure=true} exists at all
     * (local development, docker-compose, CI), so it must keep working with no extra ceremony.
     * Anything else requires {@code allowInsecureRemoteCredentials=true} -- see the constructor
     * above.
     *
     * <p>Never performs a DNS lookup: a numeric IPv4 literal is parsed by hand, an IPv6-shaped
     * literal (recognized by containing a ':', which no real hostname ever does) is handed to
     * {@link InetAddress#getByName}, which the JDK resolves purely by parsing for a literal
     * address, and anything else is treated as not loopback without ever consulting a resolver.
     */
    static boolean isLoopbackEndpoint(String endpoint) {
        // Checked first, and only on the raw string: a unix target is not a URI authority at all
        // (it carries a filesystem path, so it legitimately contains the '/' the
        // reserved-character check below refuses), and it never leaves the host's kernel
        // regardless of what the path says.
        if (endpoint.startsWith("unix:")) {
            return true;
        }

        // Fail closed on any character that can shift which part of the string the URI parser
        // treats as the authority: '@' (userinfo), '/' (path), '?' (query), '#' (fragment),
        // whitespace. Redundant with the URI parse below -- deliberately so. The parse is what
        // makes this method correct; this is what keeps it correct if some future edit ever
        // reaches for a manual split again.
        for (int i = 0; i < endpoint.length(); i++) {
            char c = endpoint.charAt(i);
            if (c == '@' || c == '/' || c == '?' || c == '#' || Character.isWhitespace(c)) {
                return false;
            }
        }

        // A bare IPv6 literal ("::1") is not a legal URI authority -- brackets are the only form
        // the transport can dial -- so bracket it (recognized by holding more than one ':', which
        // no host:port form and no real hostname ever does) and let the one parse below judge it,
        // rather than special-casing it out of the parse entirely.
        String authority = endpoint;
        if (!endpoint.startsWith("[") && endpoint.indexOf(':') != endpoint.lastIndexOf(':')) {
            authority = "[" + endpoint + "]";
        }

        String host;
        try {
            host = URI.create("//" + authority).getHost();
        } catch (IllegalArgumentException e) {
            return false;
        }
        if (host == null) {
            // URI could not find a server-based authority -- nothing the transport could dial
            // either, since DnsNameResolver rejects exactly this case ("Invalid DNS name").
            return false;
        }

        // URI#getHost keeps the brackets on an IPv6 literal; InetAddress#getByName below does not
        // accept them.
        if (host.startsWith("[") && host.endsWith("]")) {
            host = host.substring(1, host.length() - 1);
        }

        if (host.equalsIgnoreCase("localhost")) {
            return true;
        }

        if (IPV4_LITERAL.matcher(host).matches()) {
            String[] octets = host.split("\\.");
            for (String octet : octets) {
                int value = Integer.parseInt(octet);
                if (value < 0 || value > 255) {
                    return false;
                }
            }
            return Integer.parseInt(octets[0]) == 127;
        }

        if (host.indexOf(':') >= 0) {
            try {
                return InetAddress.getByName(host).isLoopbackAddress();
            } catch (UnknownHostException e) {
                return false;
            }
        }

        return false;
    }

    /**
     * CallCredentials implementation that injects a bearer token into every RPC. Nothing here
     * checks whether the underlying channel is secure -- see the constructor's guard above (root
     * DESIGN.md, "RULE: Credentials over insecure transport require an explicit opt-in") for what
     * actually stops this from shipping a bearer token to an arbitrary insecure host.
     */
    private static class BearerTokenCredentials extends CallCredentials {

        private final String token;

        BearerTokenCredentials(String token) {
            this.token = token;
        }

        @Override
        public void applyRequestMetadata(RequestInfo requestInfo, Executor appExecutor,
                                         MetadataApplier applier) {
            Metadata metadata = new Metadata();
            metadata.put(AUTHORIZATION_KEY, "Bearer " + token);
            applier.apply(metadata);
        }
    }
}
