package com.authzed.spicedb;

import build.buf.gen.authzed.api.v1.*;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.Context;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.protobuf.StatusProto;
import io.grpc.stub.MetadataUtils;
import io.grpc.stub.StreamObserver;
import java.net.URI;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.TimeUnit;
import java.util.stream.Stream;
import java.util.stream.StreamSupport;

/**
 * Idiomatic Java client for SpiceDB.
 *
 * <p>Implements {@link AutoCloseable} for use with try-with-resources. All streaming methods return
 * {@link Stream} instances that should also be closed when done.
 *
 * <p>Use the static factory methods to create instances:
 *
 * <pre>{@code
 * try (var client = SpiceDBClient.createPlaintext("localhost:50051", "testtoken")) {
 *     CheckResult result = client.checkPermission(
 *         Consistency.full(), "view",
 *         Relationship.of("document", "doc1", "viewer", "user", "alice"));
 *     boolean allowed = result.hasPermission();
 * }
 * }</pre>
 */
public final class SpiceDBClient implements AutoCloseable {

  private static final int DEFAULT_READ_PAGE_SIZE = 512;
  private static final int DEFAULT_LOOKUP_PAGE_SIZE = 512;
  private static final int DEFAULT_DELETE_PAGE_SIZE = 1_000;
  private static final int DEFAULT_IMPORT_BATCH_SIZE = 1_000;
  private static final int DEFAULT_EXPORT_PAGE_SIZE = 512;

  private static final int MAX_RETRIES = 4;
  private static final long INITIAL_BACKOFF_MS = 100;

  /**
   * Applied to every unary call that does not pass its own {@code timeout} override.
   *
   * <p>Mirrors {@code authzed-node}'s {@code DEFAULT_DEADLINE_MS = 30_000} (its comment cites
   * {@code grpc/grpc-node#541}, a known gRPC failure mode where a channel that accepts a connection
   * but never answers produces no error at all). Without a finite default, a wedged SpiceDB hangs
   * every caller that didn't opt in to a timeout -- in practice, most callers -- forever: the
   * connection looks fine at the transport level, so nothing ever times out and nothing is ever
   * produced to retry. See root DESIGN.md, "RULE: A unary call must have a deadline".
   *
   * <p>Deliberately NOT applied to server-streaming calls ({@link #readRelationships}, {@link
   * #lookupResources}, {@link #lookupSubjects}, {@link #updates}, {@link #exportRelationships}) --
   * those are long-lived by design, and applying this default to them would make the stream itself
   * the outage -- NOR to the client-streaming {@link #importRelationships(Iterable)}, whose
   * duration scales with the size of the caller's dataset rather than server latency, so no fixed
   * default is correct for it either (see DESIGN.md, "Streaming calls MUST NOT inherit the unary
   * default").
   */
  public static final Duration DEFAULT_TIMEOUT = Duration.ofSeconds(30);

  private final ManagedChannel channel;
  private final PermissionsServiceGrpc.PermissionsServiceBlockingStub permissionsStub;
  private final SchemaServiceGrpc.SchemaServiceBlockingStub schemaStub;
  private final WatchServiceGrpc.WatchServiceBlockingStub watchStub;
  private final ExperimentalServiceGrpc.ExperimentalServiceBlockingStub experimentalStub;
  private final PermissionsServiceGrpc.PermissionsServiceStub permissionsAsyncStub;
  private final Duration defaultTimeout;

  private SpiceDBClient(ManagedChannel channel, Metadata metadata) {
    this(channel, metadata, DEFAULT_TIMEOUT);
  }

  private SpiceDBClient(ManagedChannel channel, Metadata metadata, Duration defaultTimeout) {
    this.channel = channel;
    this.defaultTimeout = defaultTimeout;
    this.permissionsStub =
        PermissionsServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.schemaStub =
        SchemaServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.watchStub =
        WatchServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.experimentalStub =
        ExperimentalServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.permissionsAsyncStub =
        PermissionsServiceGrpc.newStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
  }

  /**
   * Creates a client with an insecure (plaintext) connection. Use this for testing only — the lack
   * of TLS is made obvious by the name.
   *
   * <p>By itself, this only works against a loopback {@code endpoint} (localhost, 127.0.0.0/8, ::1,
   * or a unix socket target) -- see root DESIGN.md, "RULE: Credentials over insecure transport
   * require an explicit opt-in". For a non-loopback endpoint, use {@link #createPlaintext(String,
   * String, boolean)}.
   */
  public static SpiceDBClient createPlaintext(String endpoint, String presharedKey) {
    return createPlaintext(endpoint, presharedKey, DEFAULT_TIMEOUT, false);
  }

  /**
   * As {@link #createPlaintext(String, String)}, with an explicit opt-in permitting a non-loopback
   * {@code endpoint}.
   *
   * @param allowInsecureRemoteCredentials explicit, separately named opt-in required before this
   *     plaintext connection may target a non-loopback {@code endpoint}. Named and separate from
   *     the choice to call this method at all, so a reader cannot mistake it for a default: pass
   *     {@code true} only if you genuinely mean to send a bearer token in cleartext to a remote
   *     host.
   * @throws IllegalArgumentException if {@code endpoint} is not loopback and {@code
   *     allowInsecureRemoteCredentials} is false -- thrown before any channel is created.
   */
  public static SpiceDBClient createPlaintext(
      String endpoint, String presharedKey, boolean allowInsecureRemoteCredentials) {
    return createPlaintext(endpoint, presharedKey, DEFAULT_TIMEOUT, allowInsecureRemoteCredentials);
  }

  /**
   * Creates a client with an insecure (plaintext) connection and a client-wide {@code
   * defaultTimeout} applied to every unary call that doesn't pass its own {@code timeout} override
   * (see {@link #DEFAULT_TIMEOUT}). Use this for testing only — the lack of TLS is made obvious by
   * the name.
   *
   * <p>By itself, this only works against a loopback {@code endpoint} (localhost, 127.0.0.0/8, ::1,
   * or a unix socket target) -- see root DESIGN.md, "RULE: Credentials over insecure transport
   * require an explicit opt-in". For a non-loopback endpoint, use {@link #createPlaintext(String,
   * String, Duration, boolean)}.
   */
  public static SpiceDBClient createPlaintext(
      String endpoint, String presharedKey, Duration defaultTimeout) {
    return createPlaintext(endpoint, presharedKey, defaultTimeout, false);
  }

  /**
   * As {@link #createPlaintext(String, String, Duration)}, with an explicit opt-in permitting a
   * non-loopback {@code endpoint}. See {@link #createPlaintext(String, String, boolean)} for what
   * {@code allowInsecureRemoteCredentials} means.
   *
   * @throws IllegalArgumentException if {@code endpoint} is not loopback and {@code
   *     allowInsecureRemoteCredentials} is false -- thrown before any channel is created.
   */
  public static SpiceDBClient createPlaintext(
      String endpoint,
      String presharedKey,
      Duration defaultTimeout,
      boolean allowInsecureRemoteCredentials) {
    requireInsecureTransportAllowed(endpoint, true, allowInsecureRemoteCredentials);
    ManagedChannel channel = ManagedChannelBuilder.forTarget(endpoint).usePlaintext().build();
    return new SpiceDBClient(channel, bearerMetadata(presharedKey), defaultTimeout);
  }

  /**
   * Creates a client using the system's TLS certificate pool. Use this for production connections.
   */
  public static SpiceDBClient createSystemTls(String endpoint, String presharedKey) {
    return createSystemTls(endpoint, presharedKey, DEFAULT_TIMEOUT);
  }

  /**
   * Creates a client using the system's TLS certificate pool and a client-wide {@code
   * defaultTimeout} applied to every unary call that doesn't pass its own {@code timeout} override
   * (see {@link #DEFAULT_TIMEOUT}). Use this for production connections.
   */
  public static SpiceDBClient createSystemTls(
      String endpoint, String presharedKey, Duration defaultTimeout) {
    ManagedChannel channel =
        ManagedChannelBuilder.forTarget(endpoint).useTransportSecurity().build();
    return new SpiceDBClient(channel, bearerMetadata(presharedKey), defaultTimeout);
  }

  /**
   * Creates a client with custom options.
   *
   * <p>{@link ClientOption}s may include advanced escape-hatch options that expose the underlying
   * gRPC channel builder for configuration not covered by the primary API. Most users should prefer
   * {@link #createPlaintext} or {@link #createSystemTls}.
   *
   * <p><b>Security note (root DESIGN.md, "RULE: Credentials over insecure transport require an
   * explicit opt-in"):</b> this method refuses to combine {@link #withInsecure()} with a
   * non-loopback {@code endpoint} unless {@link #allowInsecureRemoteCredentials()} is also among
   * {@code options} -- but it recognizes only those two named options themselves, by identity, not
   * by inspecting what any option actually does to the builder ({@code
   * io.grpc.ManagedChannelBuilder} exposes no way to read back whether plaintext was configured). A
   * custom {@link ClientOption} lambda that calls {@code builder.usePlaintext()} directly bypasses
   * this guard entirely and is not detected -- that lambda is the raw escape hatch documented on
   * {@link ClientOption#apply}; the full credential-safety burden for what it does to the builder
   * falls on whoever writes it.
   *
   * @param endpoint the SpiceDB endpoint
   * @param presharedKey the bearer token
   * @param options additional configuration options
   */
  public static SpiceDBClient create(
      String endpoint, String presharedKey, ClientOption... options) {
    return create(endpoint, presharedKey, DEFAULT_TIMEOUT, options);
  }

  /**
   * Creates a client with custom options and a client-wide {@code defaultTimeout} applied to every
   * unary call that doesn't pass its own {@code timeout} override (see {@link #DEFAULT_TIMEOUT}).
   *
   * <p>See {@link #create(String, String, ClientOption...)} for the security note on what this
   * guard can and cannot see among custom {@code options}.
   *
   * @param endpoint the SpiceDB endpoint
   * @param presharedKey the bearer token
   * @param defaultTimeout the client-wide default unary-call timeout
   * @param options additional configuration options
   */
  public static SpiceDBClient create(
      String endpoint, String presharedKey, Duration defaultTimeout, ClientOption... options) {
    return create(endpoint, presharedKey, defaultTimeout, null, options);
  }

  /**
   * Test-only seam: as {@link #create(String, String, Duration, ClientOption...)}, but lets a
   * caller (the test source set) override the underlying {@link ManagedChannelBuilder} -- e.g. with
   * an in-process or a real local transport -- while {@code endpoint} (what the guard below
   * actually evaluates) stays independent and can be an arbitrary non-loopback literal.
   * Package-private: not part of the public API.
   */
  static SpiceDBClient create(
      String endpoint,
      String presharedKey,
      Duration defaultTimeout,
      ManagedChannelBuilder<?> testChannelBuilder,
      ClientOption... options) {
    // Detect the two well-known named options by reference -- see root DESIGN.md, "RULE:
    // Credentials over insecure transport require an explicit opt-in", and the public security
    // notes on create(...) and ClientOption#apply above (not just this comment) for what this
    // does and does not catch: a fully custom ClientOption lambda that calls
    // builder.usePlaintext() directly is invisible to this check.
    boolean insecureRequested = false;
    boolean allowInsecureRemoteCredentials = false;
    for (ClientOption option : options) {
      if (option == INSECURE_OPTION) {
        insecureRequested = true;
      }
      if (option == ALLOW_INSECURE_REMOTE_CREDENTIALS_OPTION) {
        allowInsecureRemoteCredentials = true;
      }
    }
    requireInsecureTransportAllowed(endpoint, insecureRequested, allowInsecureRemoteCredentials);

    var builder =
        testChannelBuilder != null ? testChannelBuilder : ManagedChannelBuilder.forTarget(endpoint);
    for (ClientOption option : options) {
      option.apply(builder);
    }
    return new SpiceDBClient(builder.build(), bearerMetadata(presharedKey), defaultTimeout);
  }

  /**
   * Test-only factory that wires a client directly to a pre-built {@link ManagedChannel} (e.g. an
   * in-process transport for tests). Package-private: not part of the public API surface.
   */
  static SpiceDBClient forChannel(ManagedChannel channel) {
    return new SpiceDBClient(channel, new Metadata());
  }

  /**
   * Test-only factory: as {@link #forChannel(ManagedChannel)}, with an explicit {@code
   * defaultTimeout}. Package-private: not part of the public API surface.
   */
  static SpiceDBClient forChannel(ManagedChannel channel, Duration defaultTimeout) {
    return new SpiceDBClient(channel, new Metadata(), defaultTimeout);
  }

  /** Functional option for customizing the client. */
  @FunctionalInterface
  public interface ClientOption {
    /**
     * Applies this option to the underlying gRPC {@link ManagedChannelBuilder}.
     *
     * <p><b>Advanced escape hatch:</b> this method exposes {@code io.grpc.ManagedChannelBuilder}
     * directly for configuration not covered by the primary API. Most users should prefer {@link
     * #createPlaintext} or {@link #createSystemTls} and the standard {@code withInsecure()} option.
     *
     * <p><b>Security note:</b> {@link #create}'s insecure-transport guard (root DESIGN.md, "RULE:
     * Credentials over insecure transport require an explicit opt-in") only recognizes {@code
     * withInsecure()}/{@code allowInsecureRemoteCredentials()} by identity, because {@code
     * ManagedChannelBuilder} has no public getter to read back whether a given option called {@code
     * usePlaintext()}. A custom implementation of this method that calls {@code
     * builder.usePlaintext()} is invisible to that guard -- it can send a bearer token to any host
     * in cleartext with no refusal at all. Do not implement this method to toggle plaintext
     * yourself; use {@code withInsecure()}/{@code allowInsecureRemoteCredentials()} instead.
     *
     * @param builder the channel builder to configure
     */
    void apply(ManagedChannelBuilder<?> builder);
  }

  // Explicit singleton instances (not bare lambdas/method references) so create(...) can detect
  // them by reference equality below -- see requireInsecureTransportAllowed's call site. This is
  // identity-based, not behavioral, because ManagedChannelBuilder (verified against grpc-java
  // 1.79.0's public API) exposes no getter for whether usePlaintext() was called -- there is
  // nothing to inspect after a custom ClientOption has been applied. See the security notes on
  // ClientOption#apply and create(...) above for what this does and does not catch.
  private static final ClientOption INSECURE_OPTION = ManagedChannelBuilder::usePlaintext;
  private static final ClientOption ALLOW_INSECURE_REMOTE_CREDENTIALS_OPTION = builder -> {};

  /**
   * Option to disable TLS (plaintext). Use only for testing.
   *
   * <p>By itself, this only permits a plaintext connection to a loopback endpoint (localhost,
   * 127.0.0.0/8, ::1, or a unix socket target) -- see root DESIGN.md, "RULE: Credentials over
   * insecure transport require an explicit opt-in". Combine with {@link
   * #allowInsecureRemoteCredentials()} for a non-loopback endpoint.
   *
   * <p>This guard only fires when {@code withInsecure()} itself is passed to {@link #create}. A
   * custom {@link ClientOption} that calls {@code builder.usePlaintext()} directly is a different,
   * undetected path to the same insecure state -- see the security note on {@link
   * ClientOption#apply}.
   */
  public static ClientOption withInsecure() {
    return INSECURE_OPTION;
  }

  /**
   * Explicit, separately named opt-in permitting {@link #withInsecure()} to target a non-loopback
   * endpoint via {@link #create}. Named and separate from {@link #withInsecure()} on purpose: root
   * DESIGN.md, "RULE: Credentials over insecure transport require an explicit opt-in" requires an
   * option a reader cannot mistake for a default. Pass this only if you genuinely mean to send a
   * bearer token in cleartext to a remote host.
   */
  public static ClientOption allowInsecureRemoteCredentials() {
    return ALLOW_INSECURE_REMOTE_CREDENTIALS_OPTION;
  }

  /**
   * Reports whether the connection this client would actually open for {@code endpoint} terminates
   * on a loopback destination: the literal hostname "localhost", an IP in 127.0.0.0/8, the IPv6
   * loopback ::1, or a unix domain socket target (a "unix:" prefix). A unix socket never leaves the
   * host's kernel, so it is loopback for this check even though it has no IP at all.
   *
   * <p>That wording is deliberate. This method does not answer "does this string look like it names
   * a loopback host"; it answers "will the transport dial loopback". Those are the same question
   * only if this method and the transport agree on where the host ends and the rest of the target
   * begins -- and a hand-rolled string split will always disagree with a URI parser somewhere. It
   * used to: given {@code "127.0.0.1:443@evil.com"} a last-colon split yields host "127.0.0.1" and
   * reports loopback, while grpc-java's {@code DnsNameResolver} derives its host as {@code
   * URI.create("//" + name).getHost()} -- which reads "127.0.0.1:443" as URI <i>userinfo</i> and
   * returns "evil.com", then resolves and connects there on the default port (an RPC against {@code
   * "127.0.0.1:443@evil.invalid"} fails with "Unable to resolve host evil.invalid", naming the host
   * it actually went looking for). So the bearer token went to evil.com in cleartext with this
   * method reporting "loopback", and the {@code :authority} header carried the whole undivided
   * string, hiding it.
   *
   * <p>So the host is derived with the same {@code URI.create("//" + …).getHost()} expression
   * {@code DnsNameResolver} itself uses. There is one parse, so guard and transport cannot
   * disagree. Before that, anything that could move the authority under URI parsing ({@code @},
   * {@code /}, {@code ?}, {@code #}, whitespace) is refused outright: a legitimate SpiceDB target
   * contains none of those, and failing closed on a weird endpoint is the correct trade for a
   * credential leak.
   *
   * <p>This is the exemption in root DESIGN.md, "RULE: Credentials over insecure transport require
   * an explicit opt-in": loopback is the reason a plaintext connection exists at all (local
   * development, docker-compose, CI), so it must keep working with no extra ceremony.
   *
   * <p>Never performs a DNS lookup: a numeric IPv4 literal is parsed by hand, an IPv6-shaped
   * literal (recognized by containing a ':', which no real hostname ever does) is handed to {@link
   * java.net.InetAddress#getByName}, which the JDK resolves purely by parsing for a literal
   * address, and anything else is treated as not loopback without ever consulting a resolver.
   */
  static boolean isLoopbackEndpoint(String endpoint) {
    // Checked first, and only on the raw string: a unix target is not a URI authority at all (it
    // carries a filesystem path, so it legitimately contains the '/' the reserved-character check
    // below refuses), and it never leaves the host's kernel regardless of what the path says.
    if (endpoint.startsWith("unix:")) {
      return true;
    }

    // Fail closed on any character that can shift which part of the string the URI parser treats
    // as the authority: '@' (userinfo), '/' (path), '?' (query), '#' (fragment), whitespace.
    // Redundant with the URI parse below -- deliberately so. The parse is what makes this method
    // correct; this is what keeps it correct if some future edit ever reaches for a manual split
    // again.
    for (int i = 0; i < endpoint.length(); i++) {
      char c = endpoint.charAt(i);
      if (c == '@' || c == '/' || c == '?' || c == '#' || Character.isWhitespace(c)) {
        return false;
      }
    }

    // A bare IPv6 literal ("::1") is not a legal URI authority -- brackets are the only form the
    // transport can dial -- so bracket it (recognized by holding more than one ':', which no
    // host:port form and no real hostname ever does) and let the one parse below judge it, rather
    // than special-casing it out of the parse entirely.
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
      // URI could not find a server-based authority -- nothing the transport could dial either,
      // since DnsNameResolver rejects exactly this case ("Invalid DNS name").
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
        return java.net.InetAddress.getByName(host).isLoopbackAddress();
      } catch (java.net.UnknownHostException e) {
        return false;
      }
    }

    return false;
  }

  private static final java.util.regex.Pattern IPV4_LITERAL =
      java.util.regex.Pattern.compile("^\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}$");

  /**
   * Refuses an insecure connection to a non-loopback endpoint. See root DESIGN.md, "RULE:
   * Credentials over insecure transport require an explicit opt-in". Call this before creating any
   * channel -- a rejected combination must never get far enough to put a bearer token on the wire.
   *
   * @throws IllegalArgumentException if {@code insecure} is true, {@code endpoint} is not loopback,
   *     and {@code allowInsecureRemoteCredentials} is false.
   */
  private static void requireInsecureTransportAllowed(
      String endpoint, boolean insecure, boolean allowInsecureRemoteCredentials) {
    if (insecure && !allowInsecureRemoteCredentials && !isLoopbackEndpoint(endpoint)) {
      throw new IllegalArgumentException(
          "spicedb: refusing to send credentials over an insecure (plaintext) connection to "
              + "non-loopback endpoint \""
              + endpoint
              + "\": use TLS, or pass "
              + "allowInsecureRemoteCredentials=true (or the allowInsecureRemoteCredentials() "
              + "ClientOption) if you intend to send a bearer token in cleartext to a remote "
              + "host");
    }
  }

  // -----------------------------------------------------------------------
  // Checks — all use BulkCheckPermissions under the hood
  // -----------------------------------------------------------------------

  /**
   * Checks a single permission, returning a {@link CheckResult} carrying the server's full
   * three-valued answer, the caveat context that was missing (if any), and the {@link ZedToken}
   * revision the check was evaluated at. Uses BulkCheckPermissions under the hood.
   *
   * <p><b>RULE (root DESIGN.md, "Only an unconditional grant is true"):</b> prefer {@link
   * CheckResult#hasPermission()} over comparing {@link CheckResult#permissionship()} directly — a
   * {@code CONDITIONAL_PERMISSION} result means the server needed caveat context that was not
   * supplied, and is NOT a grant.
   */
  public CheckResult checkPermission(Consistency consistency, String permission, Relationship r) {
    List<CheckResult> results = checkPermissions(consistency, permission, r);
    return results.get(0);
  }

  /**
   * As {@link #checkPermission(Consistency, String, Relationship)}, with a per-call {@code timeout}
   * overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   */
  public CheckResult checkPermission(
      Consistency consistency, String permission, Relationship r, Duration timeout) {
    List<CheckResult> results = checkPermissionsImpl(consistency, permission, null, timeout, r);
    return results.get(0);
  }

  /**
   * Checks a single permission with a caveat CHECK-TIME {@code context}, in addition to any
   * per-item context set via {@link Relationship#withCheckContext} on {@code r} itself (item wins
   * per-key over this call-level default; see {@link #checkPermissions(Consistency, String, Map,
   * Relationship...)} for the merge rule). Distinct from {@code r}'s write-time {@code
   * caveatContext} (set via {@link Relationship#withCaveat}) — this context is never written, only
   * used to evaluate the caveat for this check.
   *
   * <p>See {@link #checkPermission(Consistency, String, Relationship)} for the RULE governing how
   * to interpret the result.
   */
  public CheckResult checkPermission(
      Consistency consistency, String permission, Relationship r, Map<String, Object> context) {
    List<CheckResult> results = checkPermissions(consistency, permission, context, r);
    return results.get(0);
  }

  /**
   * Checks permissions for multiple relationships, returning a {@link CheckResult} for each. All
   * checks use BulkCheckPermissions under the hood.
   *
   * <p>See {@link #checkPermission} for the RULE governing how to interpret each result.
   */
  public List<CheckResult> checkPermissions(
      Consistency consistency, String permission, Relationship... relationships) {
    return checkPermissions(consistency, permission, (Map<String, Object>) null, relationships);
  }

  /**
   * As {@link #checkPermissions(Consistency, String, Relationship...)}, with a per-call {@code
   * timeout} overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   */
  public List<CheckResult> checkPermissions(
      Consistency consistency, String permission, Duration timeout, Relationship... relationships) {
    return checkPermissionsImpl(consistency, permission, null, timeout, relationships);
  }

  /**
   * Checks permissions for multiple relationships with a caveat CHECK-TIME {@code context} applied,
   * by default, to every relationship — plus any per-item context set via {@link
   * Relationship#withCheckContext} on individual relationships.
   *
   * <p><b>Merge rule (key-level, item wins):</b> for each relationship, the context sent to the
   * server is the call-level {@code context} map with that relationship's own {@link
   * Relationship#checkContext()} entries overwriting matching keys — call-level keys absent from
   * the item are retained, never wholesale-replaced. For example, call-level {@code {now: 42,
   * region: "us"}} plus a per-item {@code {region: "eu"}} sends {@code {now: 42, region: "eu"}} for
   * that item, while a sibling relationship with no per-item context still sends {@code {now: 42,
   * region: "us"}}. When neither call-level nor per-item context is supplied for a relationship, no
   * {@code context} field is set on the wire at all (not an empty {@code Struct}).
   *
   * <p>Distinct from write-time {@code caveatContext} (set via {@link Relationship#withCaveat}) —
   * this context is never written, only used to evaluate the caveat for this check.
   *
   * <p>See {@link #checkPermission} for the RULE governing how to interpret each result.
   */
  public List<CheckResult> checkPermissions(
      Consistency consistency,
      String permission,
      Map<String, Object> context,
      Relationship... relationships) {
    return checkPermissionsImpl(consistency, permission, context, null, relationships);
  }

  /**
   * Shared implementation behind every {@code checkPermission(s)} overload -- request-building and
   * response-mapping, including the call-level {@code context} merge, live here once.
   */
  private List<CheckResult> checkPermissionsImpl(
      Consistency consistency,
      String permission,
      Map<String, Object> context,
      Duration timeout,
      Relationship... relationships) {
    if (relationships.length == 0) {
      return List.of();
    }

    var items = new ArrayList<CheckBulkPermissionsRequestItem>(relationships.length);
    for (Relationship r : relationships) {
      items.add(checkItemFromRel(r, permission, context));
    }

    long timeoutMs = effectiveTimeout(timeout).toMillis();
    CheckBulkPermissionsResponse resp =
        withRetry(
            () ->
                permissionsStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .checkBulkPermissions(
                        CheckBulkPermissionsRequest.newBuilder()
                            .setConsistency(consistency.toProto())
                            .addAllItems(items)
                            .build()));

    // CheckBulkPermissionsResponseItem carries no per-item checked_at of its own — the token lives
    // once on the enclosing response and applies to every pair in it.
    String checkedAt = resp.getCheckedAt().getToken();

    // The proto guarantees pairs are returned in request order but says nothing about count. A
    // short (or long) response would otherwise silently desync results[i] from relationships[i]
    // for every item after the gap — one resource's answer attributed to another. Fail loudly
    // instead of returning a misaligned-but-"successful" List.
    if (resp.getPairsCount() != items.size()) {
      throw new SpiceDBException(
          "checkBulkPermissions returned "
              + resp.getPairsCount()
              + " pair(s) for "
              + items.size()
              + " request item(s)");
    }

    var results = new ArrayList<CheckResult>(resp.getPairsCount());
    for (int i = 0; i < resp.getPairsCount(); i++) {
      CheckBulkPermissionsPair pair = resp.getPairs(i);
      if (pair.hasError()) {
        // Route the per-item error through ErrorMapper (by way of a StatusRuntimeException
        // carrying the item's own status) so callers get the SPECIFIC typed exception (e.g.
        // PermissionDeniedException) instead of the untyped base SpiceDBException — the item's
        // code was previously discarded here. The item index is preserved in the message,
        // matching spicedb-go's `fmt.Sprintf("check item %d", i)`.
        throw ErrorMapper.toSpiceDBException(
            perItemStatusException(pair.getError(), "check item " + i + ": "));
      } else if (pair.hasItem()) {
        results.add(checkResultFromBulkItem(pair.getItem(), checkedAt));
      } else {
        // The proto's `response` is a oneof — a well-behaved server always sets it to item or
        // error, so this should be unreachable in practice. Silently skipping the index here
        // would shrink `results` below `items.size()`, desyncing every subsequent results[i]
        // from relationships[i] for the rest of the batch. Fail loudly instead of returning a
        // misaligned-but-"successful" List. Mirrors spicedb-rust's malformed-oneof guard.
        throw new SpiceDBException(
            "check item "
                + i
                + ": malformed CheckBulkPermissionsPair (neither item nor error"
                + " set)");
      }
    }
    return results;
  }

  /**
   * Returns true if any of the given relationships have the permission unconditionally. A {@code
   * CONDITIONAL_PERMISSION} result does not count as granted — only {@link
   * CheckResult#hasPermission()} results are considered (RULE, clause 3).
   */
  public boolean checkAny(
      Consistency consistency, String permission, Relationship... relationships) {
    return checkAny(consistency, permission, (Map<String, Object>) null, relationships);
  }

  /**
   * As {@link #checkAny(Consistency, String, Relationship...)}, with a per-call {@code timeout}
   * overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   */
  public boolean checkAny(
      Consistency consistency, String permission, Duration timeout, Relationship... relationships) {
    List<CheckResult> results =
        checkPermissionsImpl(consistency, permission, null, timeout, relationships);
    for (CheckResult r : results) {
      if (r.hasPermission()) return true;
    }
    return false;
  }

  /**
   * Returns true if any of the given relationships have the permission unconditionally, evaluating
   * caveats with the given call-level/per-item CHECK-TIME {@code context} (see {@link
   * #checkPermissions(Consistency, String, Map, Relationship...)} for the merge rule). A {@code
   * CONDITIONAL_PERMISSION} result does not count as granted — only {@link
   * CheckResult#hasPermission()} results are considered (RULE, clause 3).
   */
  public boolean checkAny(
      Consistency consistency,
      String permission,
      Map<String, Object> context,
      Relationship... relationships) {
    List<CheckResult> results = checkPermissions(consistency, permission, context, relationships);
    for (CheckResult r : results) {
      if (r.hasPermission()) return true;
    }
    return false;
  }

  /**
   * Returns true if all of the given relationships have the permission unconditionally. A {@code
   * CONDITIONAL_PERMISSION} result does not count as granted — every result must be {@link
   * CheckResult#hasPermission()} for this to return true (RULE, clause 3).
   */
  public boolean checkAll(
      Consistency consistency, String permission, Relationship... relationships) {
    return checkAll(consistency, permission, (Map<String, Object>) null, relationships);
  }

  /**
   * As {@link #checkAll(Consistency, String, Relationship...)}, with a per-call {@code timeout}
   * overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   *
   * <p>Returns false, not the vacuous true a for-loop over zero relationships would fall through
   * to, when {@code relationships} is empty (RULE: "An aggregate over zero checks is not a grant").
   */
  public boolean checkAll(
      Consistency consistency, String permission, Duration timeout, Relationship... relationships) {
    if (relationships.length == 0) {
      return false;
    }
    List<CheckResult> results =
        checkPermissionsImpl(consistency, permission, null, timeout, relationships);
    for (CheckResult r : results) {
      if (!r.hasPermission()) return false;
    }
    return true;
  }

  /**
   * Returns true if all of the given relationships have the permission unconditionally, evaluating
   * caveats with the given call-level/per-item CHECK-TIME {@code context} (see {@link
   * #checkPermissions(Consistency, String, Map, Relationship...)} for the merge rule). A {@code
   * CONDITIONAL_PERMISSION} result does not count as granted — every result must be {@link
   * CheckResult#hasPermission()} for this to return true (RULE, clause 3).
   */
  public boolean checkAll(
      Consistency consistency,
      String permission,
      Map<String, Object> context,
      Relationship... relationships) {
    // A for-loop over zero relationships never executes its body and falls through to `true` —
    // vacuously true, like every language's all/every primitive on an empty sequence. Guard the
    // empty case explicitly so "no checks to run" is never treated as "all checks passed" (RULE:
    // "An aggregate over zero checks is not a grant").
    if (relationships.length == 0) {
      return false;
    }
    List<CheckResult> results = checkPermissions(consistency, permission, context, relationships);
    for (CheckResult r : results) {
      if (!r.hasPermission()) return false;
    }
    return true;
  }

  // -----------------------------------------------------------------------
  // Writes
  // -----------------------------------------------------------------------

  /**
   * Commits a transaction of relationship mutations to SpiceDB, returning the revision at which the
   * write occurred.
   */
  public String write(Transaction txn) {
    return write(txn, null);
  }

  /**
   * As {@link #write(Transaction)}, with a per-call {@code timeout} overriding the client's default
   * (see {@link #DEFAULT_TIMEOUT}).
   */
  public String write(Transaction txn, Duration timeout) {
    var reqBuilder = WriteRelationshipsRequest.newBuilder();

    for (Transaction.Mutation m : txn.mutations()) {
      reqBuilder.addUpdates(toRelationshipUpdate(m));
    }

    for (Transaction.Precondition p : txn.preconditions()) {
      reqBuilder.addOptionalPreconditions(toPrecondition(p));
    }

    long timeoutMs = effectiveTimeout(timeout).toMillis();
    WriteRelationshipsResponse resp =
        callOnce(
            () ->
                permissionsStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .writeRelationships(reqBuilder.build()));
    return resp.getWrittenAt().getToken();
  }

  // -----------------------------------------------------------------------
  // Read Relationships — cursor-based auto-pagination (512-item pages)
  // -----------------------------------------------------------------------

  /**
   * Returns a stream over relationships matching the given filter. Cursors are handled
   * transparently — the client automatically re-fetches pages of 512 relationships.
   *
   * <p>The returned stream should be closed when done (it is AutoCloseable).
   */
  public Stream<Relationship> readRelationships(Consistency consistency, Filter filter) {
    return paginatedRelationshipStream(consistency, filter, DEFAULT_READ_PAGE_SIZE);
  }

  // -----------------------------------------------------------------------
  // Delete Relationships — auto-paging 1,000-item batches
  // -----------------------------------------------------------------------

  /**
   * Optional preconditions and page-size override for {@link #deleteRelationships(Filter,
   * DeleteOptions)}.
   *
   * <p>Immutable — {@code withMustMatch}/{@code withMustNotMatch}/{@code withLimit} each return a
   * new instance, mirroring {@link Filter}'s builder style. Start from {@link #none()}, which is
   * exactly the behavior of the single-argument {@link #deleteRelationships(Filter)} overload.
   *
   * <p>Preconditions are a per-request proto field, so when a delete spans multiple pages (i.e.
   * more matches than the page size), they are re-evaluated by the server on every page — there is
   * no "check-once, apply-to-all-pages" semantics. This means a delete that starts successfully can
   * still fail partway through if the guarded state changes between pages, after earlier pages have
   * already been deleted. For a single-shot, all-or-nothing guarded delete, pair the precondition
   * with a {@link #withLimit} large enough to cover every matching relationship in one call.
   * Mirrors {@code spicedb-go}'s {@code WithDeleteMustMatch}/{@code WithDeleteMustNotMatch}/{@code
   * WithDeleteLimit} (client/relationships.go).
   *
   * <pre>{@code
   * var options = SpiceDBClient.DeleteOptions.none()
   *     .withMustMatch(existsFilter)
   *     .withLimit(500);
   * client.deleteRelationships(filter, options);
   * }</pre>
   */
  public record DeleteOptions(
      List<Filter> mustMatch, List<Filter> mustNotMatch, Integer limit, Duration timeout) {

    public DeleteOptions {
      mustMatch = mustMatch == null ? List.of() : List.copyOf(mustMatch);
      mustNotMatch = mustNotMatch == null ? List.of() : List.copyOf(mustNotMatch);
      if (limit != null && limit <= 0) {
        throw new IllegalArgumentException("limit must be positive");
      }
    }

    /**
     * No preconditions, default page size (1,000) — identical behavior to {@link
     * #deleteRelationships(Filter)}.
     */
    public static DeleteOptions none() {
      return new DeleteOptions(List.of(), List.of(), null, null);
    }

    /**
     * Adds a MUST_MATCH precondition: the server rejects the delete (and deletes nothing) unless at
     * least one relationship matching {@code filter} exists at evaluation time. Multiple calls
     * accumulate; all are sent with every page of the delete.
     */
    public DeleteOptions withMustMatch(Filter filter) {
      var updated = new ArrayList<>(mustMatch);
      updated.add(filter);
      return new DeleteOptions(updated, mustNotMatch, limit, timeout);
    }

    /**
     * Adds a MUST_NOT_MATCH precondition: the server rejects the delete (and deletes nothing) if
     * any relationship matching {@code filter} exists at evaluation time. Multiple calls
     * accumulate; all are sent with every page of the delete.
     */
    public DeleteOptions withMustNotMatch(Filter filter) {
      var updated = new ArrayList<>(mustNotMatch);
      updated.add(filter);
      return new DeleteOptions(mustMatch, updated, limit, timeout);
    }

    /** Overrides the per-request page size used by the auto-paging delete loop (default 1,000). */
    public DeleteOptions withLimit(int limit) {
      return new DeleteOptions(mustMatch, mustNotMatch, limit, timeout);
    }

    /**
     * Overrides the client's default unary-call timeout (see {@link SpiceDBClient#DEFAULT_TIMEOUT})
     * for EACH page's call.
     */
    public DeleteOptions withTimeout(Duration timeout) {
      return new DeleteOptions(mustMatch, mustNotMatch, limit, timeout);
    }
  }

  /**
   * Deletes all relationships matching the given filter, guarded by optional preconditions and with
   * an optional page-size override supplied via {@code options}. Returns the revision of the final
   * deletion. See {@link DeleteOptions} for precondition/paging semantics.
   */
  public String deleteRelationships(Filter filter, DeleteOptions options) {
    var preconditions = new ArrayList<Precondition>();
    for (Filter f : options.mustMatch()) {
      preconditions.add(
          toPrecondition(
              new Transaction.Precondition(Transaction.PreconditionOperation.MUST_MATCH, f)));
    }
    for (Filter f : options.mustNotMatch()) {
      preconditions.add(
          toPrecondition(
              new Transaction.Precondition(Transaction.PreconditionOperation.MUST_NOT_MATCH, f)));
    }
    int pageSize = options.limit() != null ? options.limit() : DEFAULT_DELETE_PAGE_SIZE;
    long timeoutMs = effectiveTimeout(options.timeout()).toMillis();

    String revision = "";
    while (true) {
      DeleteRelationshipsResponse resp =
          callOnce(
              () ->
                  permissionsStub
                      .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                      .deleteRelationships(
                          DeleteRelationshipsRequest.newBuilder()
                              .setRelationshipFilter(toRelationshipFilter(filter))
                              .addAllOptionalPreconditions(preconditions)
                              .setOptionalLimit(pageSize)
                              .setOptionalAllowPartialDeletions(true)
                              .build()));
      revision = resp.getDeletedAt().getToken();
      if (resp.getDeletionProgress()
          == DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_COMPLETE) {
        return revision;
      }
    }
  }

  /**
   * Deletes all relationships matching the given filter. Large result sets are automatically paged
   * in batches of 1,000. Returns the revision of the final deletion.
   */
  public String deleteRelationships(Filter filter) {
    return deleteRelationships(filter, DeleteOptions.none());
  }

  // -----------------------------------------------------------------------
  // Lookups — cursor-based auto-pagination (512-item pages)
  // -----------------------------------------------------------------------

  /**
   * Returns a stream over resources of the given type that the subject has the specified permission
   * on. Each result carries the permissionship (full grant vs conditional on caveat context) and,
   * for conditional results, which caveat context was missing. Cursors are handled transparently.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<LookupResult.LookupResource> lookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      String subjectType,
      String subjectID) {
    Context.CancellableContext cancelCtx = Context.current().withCancellation();
    Iterator<LookupResult.LookupResource> iterator =
        new Iterator<>() {
          private Cursor cursor = null;
          private Iterator<LookupResourcesResponse> currentPage = Collections.emptyIterator();
          private boolean done = false;
          private int pageCount = 0;

          @Override
          public boolean hasNext() {
            if (currentPage.hasNext()) return true;
            if (done) return false;
            fetchNextPage();
            return currentPage.hasNext();
          }

          @Override
          public LookupResult.LookupResource next() {
            if (!hasNext()) throw new NoSuchElementException();
            LookupResourcesResponse resp = currentPage.next();
            pageCount++;
            cursor = resp.getAfterResultCursor();
            return lookupResourceFromProto(resp);
          }

          private void fetchNextPage() {
            var reqBuilder =
                LookupResourcesRequest.newBuilder()
                    .setConsistency(consistency.toProto())
                    .setResourceObjectType(resourceType)
                    .setPermission(permission)
                    .setSubject(
                        SubjectReference.newBuilder()
                            .setObject(
                                ObjectReference.newBuilder()
                                    .setObjectType(subjectType)
                                    .setObjectId(subjectID)
                                    .build())
                            .build())
                    .setOptionalLimit(DEFAULT_LOOKUP_PAGE_SIZE);

            if (cursor != null) {
              reqBuilder.setOptionalCursor(cursor);
            }

            var responses = new ArrayList<LookupResourcesResponse>();
            Iterator<LookupResourcesResponse> serverStream;
            Context previous = cancelCtx.attach();
            try {
              serverStream =
                  openStreamWithRetry(() -> permissionsStub.lookupResources(reqBuilder.build()));
            } finally {
              cancelCtx.detach(previous);
            }
            // The first hasNext() is already primed by openStreamWithRetry (retried above); every
            // poll from here on is mapped-but-not-retried, since an item may already have been
            // appended to `responses` by the time a later one fails.
            while (mapStreamErrors(serverStream::hasNext)) {
              responses.add(mapStreamErrors(serverStream::next));
            }

            currentPage = responses.iterator();
            if (responses.size() < DEFAULT_LOOKUP_PAGE_SIZE) {
              done = true;
            }
            if (pageCount > 0 && responses.isEmpty()) {
              done = true;
            }
            pageCount = 0;
          }
        };

    return cancelOnClose(
        StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false),
        cancelCtx);
  }

  /**
   * Returns a stream over subjects of the given type that have the specified permission on the
   * resource. Unlike lookupResources, this does not use cursor-based pagination (not supported in
   * SpiceDB yet) and streams all results in a single call.
   *
   * <p>When a yielded {@link LookupResult.LookupSubject#subject} is the wildcard {@code "*"}, the
   * server has granted the permission to every subject of the requested subject type EXCEPT those
   * listed in {@link LookupResult.LookupSubject#excludedSubjects}. Callers MUST check {@code
   * excludedSubjects} before treating a wildcard match as a blanket grant, or they risk granting
   * access to subjects the server explicitly excluded.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<LookupResult.LookupSubject> lookupSubjects(
      Consistency consistency,
      String resourceType,
      String resourceID,
      String permission,
      String subjectType) {
    Iterator<LookupSubjectsResponse> serverStream =
        openStreamWithRetry(
            () ->
                permissionsStub.lookupSubjects(
                    LookupSubjectsRequest.newBuilder()
                        .setConsistency(consistency.toProto())
                        .setResource(
                            ObjectReference.newBuilder()
                                .setObjectType(resourceType)
                                .setObjectId(resourceID)
                                .build())
                        .setPermission(permission)
                        .setSubjectObjectType(subjectType)
                        .build()));
    // The first hasNext() is already primed by openStreamWithRetry (retried above); every poll
    // from here on is mapped-but-not-retried, since an item may already have been added to
    // `responses` by the time a later one fails.
    var responses = new ArrayList<LookupSubjectsResponse>();
    while (mapStreamErrors(serverStream::hasNext)) {
      responses.add(mapStreamErrors(serverStream::next));
    }

    return responses.stream().map(SpiceDBClient::lookupSubjectFromProto);
  }

  // -----------------------------------------------------------------------
  // Schema
  // -----------------------------------------------------------------------

  /** Result of a {@link #readSchema()} call. */
  public record SchemaResult(String schema, String revision) {}

  /** Returns the current SpiceDB schema. */
  public SchemaResult readSchema() {
    return readSchema(null);
  }

  /**
   * As {@link #readSchema()}, with a per-call {@code timeout} overriding the client's default (see
   * {@link #DEFAULT_TIMEOUT}).
   */
  public SchemaResult readSchema(Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    ReadSchemaResponse resp =
        withRetry(
            () ->
                schemaStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .readSchema(ReadSchemaRequest.getDefaultInstance()));
    return new SchemaResult(resp.getSchemaText(), resp.getReadAt().getToken());
  }

  /** Writes a new schema to SpiceDB, returning the revision. */
  public String writeSchema(String schema) {
    return writeSchema(schema, null);
  }

  /**
   * As {@link #writeSchema(String)}, with a per-call {@code timeout} overriding the client's
   * default (see {@link #DEFAULT_TIMEOUT}).
   */
  public String writeSchema(String schema, Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    WriteSchemaResponse resp =
        callOnce(
            () ->
                schemaStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .writeSchema(WriteSchemaRequest.newBuilder().setSchema(schema).build()));
    return resp.getWrittenAt().getToken();
  }

  /** A definition in a SpiceDB schema. */
  public record SchemaDefinition(
      String name,
      String comment,
      List<SchemaRelation> relations,
      List<SchemaPermission> permissions) {}

  /** A relation within a schema definition. */
  public record SchemaRelation(String name, String comment, String parentDefinitionName) {}

  /** A permission within a schema definition. */
  public record SchemaPermission(String name, String comment, String parentDefinitionName) {}

  /** A caveat defined in a SpiceDB schema. */
  public record SchemaCaveat(
      String name, String comment, String expression, List<SchemaCaveatParameter> parameters) {}

  /** A parameter of a caveat. */
  public record SchemaCaveatParameter(String name, String type, String parentCaveatName) {}

  /** Result of a {@link #reflectSchema(Consistency)} call. */
  public record ReflectSchemaResult(
      List<SchemaDefinition> definitions, List<SchemaCaveat> caveats, String revision) {}

  /** Returns the definitions and caveats in the current schema. */
  public ReflectSchemaResult reflectSchema(Consistency consistency) {
    return reflectSchema(consistency, null);
  }

  /**
   * As {@link #reflectSchema(Consistency)}, with a per-call {@code timeout} overriding the client's
   * default (see {@link #DEFAULT_TIMEOUT}).
   */
  public ReflectSchemaResult reflectSchema(Consistency consistency, Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    ReflectSchemaResponse resp =
        withRetry(
            () ->
                schemaStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .reflectSchema(
                        ReflectSchemaRequest.newBuilder()
                            .setConsistency(consistency.toProto())
                            .build()));

    var definitions = new ArrayList<SchemaDefinition>();
    for (var def : resp.getDefinitionsList()) {
      var relations = new ArrayList<SchemaRelation>();
      for (var rel : def.getRelationsList()) {
        relations.add(
            new SchemaRelation(rel.getName(), rel.getComment(), rel.getParentDefinitionName()));
      }
      var permissions = new ArrayList<SchemaPermission>();
      for (var perm : def.getPermissionsList()) {
        permissions.add(
            new SchemaPermission(
                perm.getName(), perm.getComment(), perm.getParentDefinitionName()));
      }
      definitions.add(
          new SchemaDefinition(
              def.getName(), def.getComment(), List.copyOf(relations), List.copyOf(permissions)));
    }

    var caveats = new ArrayList<SchemaCaveat>();
    for (var cav : resp.getCaveatsList()) {
      var params = new ArrayList<SchemaCaveatParameter>();
      for (var param : cav.getParametersList()) {
        params.add(
            new SchemaCaveatParameter(
                param.getName(), param.getType(), param.getParentCaveatName()));
      }
      caveats.add(
          new SchemaCaveat(
              cav.getName(), cav.getComment(), cav.getExpression(), List.copyOf(params)));
    }

    return new ReflectSchemaResult(
        List.copyOf(definitions), List.copyOf(caveats), resp.getReadAt().getToken());
  }

  /** Identifies a relation or permission on a definition. */
  public record RelationReference(
      String definitionName, String relationName, boolean isPermission) {}

  /** Result of a {@link #computablePermissions} call. */
  public record ComputablePermissionsResult(List<RelationReference> permissions, String revision) {}

  /** Returns the permissions that are computable for the given relation. */
  public ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName) {
    return computablePermissions(consistency, definitionName, relationName, null);
  }

  /**
   * As {@link #computablePermissions(Consistency, String, String)}, with a per-call {@code timeout}
   * overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   */
  public ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName, Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    ComputablePermissionsResponse resp =
        withRetry(
            () ->
                schemaStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .computablePermissions(
                        ComputablePermissionsRequest.newBuilder()
                            .setConsistency(consistency.toProto())
                            .setDefinitionName(definitionName)
                            .setRelationName(relationName)
                            .build()));

    var refs = new ArrayList<RelationReference>();
    for (var perm : resp.getPermissionsList()) {
      refs.add(
          new RelationReference(
              perm.getDefinitionName(), perm.getRelationName(), perm.getIsPermission()));
    }
    return new ComputablePermissionsResult(List.copyOf(refs), resp.getReadAt().getToken());
  }

  /** Result of a {@link #dependentRelations} call. */
  public record DependentRelationsResult(List<RelationReference> relations, String revision) {}

  /** Returns the relations that the given permission depends on. */
  public DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName) {
    return dependentRelations(consistency, definitionName, permissionName, null);
  }

  /**
   * As {@link #dependentRelations(Consistency, String, String)}, with a per-call {@code timeout}
   * overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   */
  public DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName, Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    DependentRelationsResponse resp =
        withRetry(
            () ->
                schemaStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .dependentRelations(
                        DependentRelationsRequest.newBuilder()
                            .setConsistency(consistency.toProto())
                            .setDefinitionName(definitionName)
                            .setPermissionName(permissionName)
                            .build()));

    var refs = new ArrayList<RelationReference>();
    for (var rel : resp.getRelationsList()) {
      refs.add(
          new RelationReference(
              rel.getDefinitionName(), rel.getRelationName(), rel.getIsPermission()));
    }
    return new DependentRelationsResult(List.copyOf(refs), resp.getReadAt().getToken());
  }

  /** A single difference between two schemas. */
  public record SchemaDiff(
      String kind,
      String definitionName,
      String relationName,
      String permissionName,
      String caveatName) {}

  /** Result of a {@link #diffSchema} call. */
  public record DiffSchemaResult(List<SchemaDiff> diffs, String revision) {}

  /** Compares the current schema against the given comparison schema. */
  public DiffSchemaResult diffSchema(Consistency consistency, String comparisonSchema) {
    return diffSchema(consistency, comparisonSchema, null);
  }

  /**
   * As {@link #diffSchema(Consistency, String)}, with a per-call {@code timeout} overriding the
   * client's default (see {@link #DEFAULT_TIMEOUT}).
   */
  public DiffSchemaResult diffSchema(
      Consistency consistency, String comparisonSchema, Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    DiffSchemaResponse resp =
        withRetry(
            () ->
                schemaStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .diffSchema(
                        DiffSchemaRequest.newBuilder()
                            .setConsistency(consistency.toProto())
                            .setComparisonSchema(comparisonSchema)
                            .build()));

    var diffs = new ArrayList<SchemaDiff>();
    for (var d : resp.getDiffsList()) {
      diffs.add(schemaDiffFromProto(d));
    }
    return new DiffSchemaResult(List.copyOf(diffs), resp.getReadAt().getToken());
  }

  // -----------------------------------------------------------------------
  // Expand
  // -----------------------------------------------------------------------

  /** Result of an {@link #expandPermissionTree} call. */
  public record ExpandResult(PermissionTree tree, String revision) {}

  /**
   * Expands the permission tree for the given resource and permission, returning the full tree of
   * subjects with access.
   */
  public ExpandResult expandPermissionTree(
      Consistency consistency, String resourceType, String resourceID, String permission) {
    return expandPermissionTree(consistency, resourceType, resourceID, permission, null);
  }

  /**
   * As {@link #expandPermissionTree(Consistency, String, String, String)}, with a per-call {@code
   * timeout} overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   */
  public ExpandResult expandPermissionTree(
      Consistency consistency,
      String resourceType,
      String resourceID,
      String permission,
      Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    ExpandPermissionTreeResponse resp =
        withRetry(
            () ->
                permissionsStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .expandPermissionTree(
                        ExpandPermissionTreeRequest.newBuilder()
                            .setConsistency(consistency.toProto())
                            .setResource(
                                ObjectReference.newBuilder()
                                    .setObjectType(resourceType)
                                    .setObjectId(resourceID)
                                    .build())
                            .setPermission(permission)
                            .build()));
    return new ExpandResult(toPermissionTree(resp.getTreeRoot()), resp.getExpandedAt().getToken());
  }

  // -----------------------------------------------------------------------
  // Bulk Import / Export
  // -----------------------------------------------------------------------

  /**
   * Streams relationships to SpiceDB for bulk import, returning the number of relationships loaded.
   * Relationships are automatically batched into chunks of 1,000.
   *
   * <p>{@code ImportBulkRelationships} is client-streaming: its duration scales with the size of
   * {@code relationships}, not with server latency, so unlike every other method on this client,
   * this call is NOT bounded by {@link #DEFAULT_TIMEOUT} (root DESIGN.md, "RULE: A unary call must
   * have a deadline", clause 3). It is unbounded; use {@link #importRelationships(Iterable,
   * Duration)} to bound it explicitly.
   */
  public long importRelationships(Iterable<Relationship> relationships) {
    return importRelationships(relationships, null);
  }

  /**
   * As {@link #importRelationships(Iterable)}, with an explicit per-call {@code timeout}. There is
   * no client default to override here (see {@link #importRelationships(Iterable)}) -- this is the
   * only way to bound this call at all.
   */
  public long importRelationships(Iterable<Relationship> relationships, Duration timeout) {
    // Deliberately NOT effectiveTimeout(timeout) -- no client default applies here, only an
    // explicit per-call timeout.
    var stub =
        timeout != null
            ? permissionsAsyncStub.withDeadlineAfter(timeout.toMillis(), TimeUnit.MILLISECONDS)
            : permissionsAsyncStub;

    var resultHolder = new long[1];
    var errorHolder = new Throwable[1];
    var latch = new java.util.concurrent.CountDownLatch(1);

    StreamObserver<ImportBulkRelationshipsResponse> responseObserver =
        new StreamObserver<>() {
          @Override
          public void onNext(ImportBulkRelationshipsResponse resp) {
            resultHolder[0] = resp.getNumLoaded();
          }

          @Override
          public void onError(Throwable t) {
            errorHolder[0] = t;
            latch.countDown();
          }

          @Override
          public void onCompleted() {
            latch.countDown();
          }
        };

    StreamObserver<ImportBulkRelationshipsRequest> requestObserver =
        stub.importBulkRelationships(responseObserver);

    var batch = new ArrayList<build.buf.gen.authzed.api.v1.Relationship>(DEFAULT_IMPORT_BATCH_SIZE);
    for (Relationship r : relationships) {
      batch.add(toProtoRelationship(r));
      if (batch.size() >= DEFAULT_IMPORT_BATCH_SIZE) {
        requestObserver.onNext(
            ImportBulkRelationshipsRequest.newBuilder().addAllRelationships(batch).build());
        batch.clear();
      }
    }

    if (!batch.isEmpty()) {
      requestObserver.onNext(
          ImportBulkRelationshipsRequest.newBuilder().addAllRelationships(batch).build());
    }

    requestObserver.onCompleted();

    try {
      latch.await();
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new SpiceDBException("import interrupted", e);
    }

    if (errorHolder[0] != null) {
      if (errorHolder[0] instanceof StatusRuntimeException sre) {
        throw ErrorMapper.toSpiceDBException(sre);
      }
      throw new SpiceDBException("import failed", errorHolder[0]);
    }

    return resultHolder[0];
  }

  /**
   * Returns a stream over all relationships matching the optional filter, streamed from SpiceDB in
   * bulk.
   *
   * <p>Unlike every other paginated RPC on this client (whose {@code optional_limit} bounds the
   * WHOLE stream, ending the call once that many results have been returned), {@code
   * ExportBulkRelationships}' {@code optional_limit} bounds only the number of relationships the
   * server puts in a SINGLE response MESSAGE ("page") -- the server keeps streaming further
   * messages on the SAME call until the whole dataset has been sent. The loop shape that is correct
   * for {@link #updates}/every lookup method (drain the current page's server stream, check the
   * count against the page size to decide whether to reissue with a new cursor) is therefore wrong
   * here: it would drain the ENTIRE export -- however many relationships that is -- into one
   * in-memory buffer before this method's {@code Stream} produced its first element, which is an
   * OOM risk in the one API most likely to face the largest dataset in the system (a full
   * 10M-relationship export). Instead, this pulls exactly ONE response message (up to {@link
   * #DEFAULT_EXPORT_PAGE_SIZE} relationships) per underlying {@code hasNext()}/{@code next()}
   * refill, mirroring {@link #updates}' single-message-at-a-time model, and only opens a new call
   * -- with establishment retry, same as {@link #updates} -- lazily, on first use.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<Relationship> exportRelationships(Consistency consistency, Filter filter) {
    Context.CancellableContext cancelCtx = Context.current().withCancellation();
    Iterator<Relationship> iterator =
        new Iterator<>() {
          // Opened lazily on the first hasNext() call, then read from across
          // many next() calls until the server closes it -- see updates()'s
          // Iterator<WatchResponse> serverStream for why eager priming would
          // be wrong (not applicable to export's bounded stream the same
          // way, but kept consistent: establishment retry only applies to
          // this one open, same reasoning as updates()).
          private Iterator<ExportBulkRelationshipsResponse> serverStream;
          private Cursor cursor = null;
          private final List<Relationship> buffer = new ArrayList<>();
          private int bufferIndex = 0;

          @Override
          public boolean hasNext() {
            if (bufferIndex < buffer.size()) return true;
            fetchNextBatch();
            return bufferIndex < buffer.size();
          }

          @Override
          public Relationship next() {
            if (!hasNext()) throw new NoSuchElementException();
            return buffer.get(bufferIndex++);
          }

          private void fetchNextBatch() {
            buffer.clear();
            bufferIndex = 0;

            if (serverStream == null) {
              var reqBuilder =
                  ExportBulkRelationshipsRequest.newBuilder()
                      .setConsistency(consistency.toProto())
                      .setOptionalLimit(DEFAULT_EXPORT_PAGE_SIZE);

              if (filter != null) {
                reqBuilder.setOptionalRelationshipFilter(toRelationshipFilter(filter));
              }
              if (cursor != null) {
                reqBuilder.setOptionalCursor(cursor);
              }

              Context previous = cancelCtx.attach();
              try {
                serverStream =
                    openStreamWithRetry(
                        () -> permissionsStub.exportBulkRelationships(reqBuilder.build()));
              } finally {
                cancelCtx.detach(previous);
              }
            }

            // Pull exactly ONE response message -- see the Javadoc above for
            // why draining serverStream::hasNext() in a loop here (the
            // previous implementation) buffers the entire export before the
            // caller's Stream yields anything.
            if (mapStreamErrors(serverStream::hasNext)) {
              ExportBulkRelationshipsResponse resp = mapStreamErrors(serverStream::next);
              cursor = resp.getAfterResultCursor();
              for (var r : resp.getRelationshipsList()) {
                buffer.add(fromProtoRelationship(r));
              }
            } else {
              // The server closed the stream: every matching relationship
              // has been sent (ExportBulkRelationships' single call is
              // exhaustive -- see the Javadoc above), so there is nothing
              // left to fetch.
              serverStream = null;
            }
          }
        };

    return cancelOnClose(
        StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false),
        cancelCtx);
  }

  // -----------------------------------------------------------------------
  // Watch
  // -----------------------------------------------------------------------

  /** A relationship update from the watch API. */
  public record Update(UpdateOperation operation, Relationship relationship) {}

  /** The type of mutation in an Update. */
  public enum UpdateOperation {
    /**
     * The server sent an operation this client does not recognize -- either {@code
     * OPERATION_UNSPECIFIED} on the wire, or a future operation value added after this client
     * shipped. Never treat this as a write: a cache or index mirror consuming the watch stream that
     * upserts on an unrecognized operation could turn a delete it doesn't understand into a silent
     * write.
     */
    UNSPECIFIED,
    CREATE,
    TOUCH,
    DELETE
  }

  /**
   * A single event from the watch API, corresponding to one {@code WatchResponse} from the server.
   *
   * <p>{@code changesThrough} is always populated -- proto: "the ZedToken that represents the point
   * in time that the watch response is current through. This token can be used in a subsequent
   * WatchRequest to resume watching from this point." Pass it as {@code startRevision} to a later
   * {@link #updates(List, String)} call to resume after a dropped stream, instead of restarting
   * from the original {@code startRevision} (reprocessing everything since, possibly past the GC
   * window) or from head (silently losing every change in the gap).
   *
   * <p>{@code isCheckpoint} is true for a checkpoint event, which carries no {@code updates} -- it
   * exists only to advertise a fresh {@code changesThrough} and, behind a proxy that aborts idle
   * connections, to keep the stream alive. Checkpoints are only sent when {@code
   * includeCheckpoints} is passed to {@link #updates(List, String, boolean)}.
   */
  public record WatchEvent(List<Update> updates, String changesThrough, boolean isCheckpoint) {
    public WatchEvent {
      updates = updates == null ? List.of() : List.copyOf(updates);
    }
  }

  /**
   * Returns a stream over watch events from SpiceDB's watch API, starting from the given revision.
   * Equivalent to {@code updates(objectTypes, startRevision, false)} -- does not request
   * checkpoints.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<WatchEvent> updates(List<String> objectTypes, String startRevision) {
    return updates(objectTypes, startRevision, false);
  }

  /**
   * As {@link #updates(List, String)}, but with {@code includeCheckpoints} to also request periodic
   * checkpoint events ({@link WatchEvent#isCheckpoint()}, no updates). Recommended if this SpiceDB
   * instance is running behind a proxy that aborts idle connections, since a checkpoint keeps the
   * stream alive even when there are no changes.
   *
   * <p>The returned stream should be closed when done.
   */
  public Stream<WatchEvent> updates(
      List<String> objectTypes, String startRevision, boolean includeCheckpoints) {
    var reqBuilder = WatchRequest.newBuilder();
    if (objectTypes != null) {
      reqBuilder.addAllOptionalObjectTypes(objectTypes);
    }
    if (startRevision != null && !startRevision.isEmpty()) {
      reqBuilder.setOptionalStartCursor(ZedToken.newBuilder().setToken(startRevision).build());
    }
    if (includeCheckpoints) {
      // optionalUpdateKinds is empty-means-default (relationship updates only, for backwards
      // compatibility) -- a non-empty list is the exact set requested, so asking for checkpoints
      // must also spell out relationship updates or the server would stop sending them.
      reqBuilder.addOptionalUpdateKinds(WatchKind.WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES);
      reqBuilder.addOptionalUpdateKinds(WatchKind.WATCH_KIND_INCLUDE_CHECKPOINTS);
    }

    Context.CancellableContext cancelCtx = Context.current().withCancellation();

    Iterator<WatchEvent> iterator =
        new Iterator<>() {
          // Opened lazily, on the first hasNext() call below — not eagerly here in updates()
          // itself. This matters for correctness, not just style: grpc-java's blocking-stub
          // priming call (which openStreamWithRetry needs, to make retry effective — see its
          // Javadoc) blocks until the first message OR the call terminates. For watch, "the first
          // message" can be arbitrarily far in the future (it's an indefinite live feed), so
          // priming eagerly would turn updates() into a call that can hang for however long
          // there's no relationship change — a real behavioral regression for a method whose
          // whole contract is "return a stream promptly, block only when the caller pulls from
          // it". Deferring the open (and its retry) to the caller's first pull preserves that
          // contract while still making establishment retry effective once the caller does pull.
          private Iterator<WatchResponse> serverStream;

          @Override
          public boolean hasNext() {
            if (serverStream == null) {
              // Establishment retry: applies ONLY to this first open. Once serverStream is set,
              // every later call skips straight to mapStreamErrors below (no retry) — so a
              // transient error after the watch has connected (mid-watch) is mapped and rethrown,
              // never retried, since retrying would replay/duplicate already-delivered updates.
              Context previous = cancelCtx.attach();
              try {
                serverStream = openStreamWithRetry(() -> watchStub.watch(reqBuilder.build()));
              } finally {
                cancelCtx.detach(previous);
              }
            }
            return mapStreamErrors(serverStream::hasNext);
          }

          @Override
          public WatchEvent next() {
            if (!hasNext()) throw new NoSuchElementException();
            WatchResponse resp = mapStreamErrors(serverStream::next);
            List<Update> batch = new ArrayList<>(resp.getUpdatesCount());
            for (var u : resp.getUpdatesList()) {
              batch.add(updateFromProto(u));
            }
            return new WatchEvent(
                batch, resp.getChangesThrough().getToken(), resp.getIsCheckpoint());
          }
        };

    return cancelOnClose(
        StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false),
        cancelCtx);
  }

  // -----------------------------------------------------------------------
  // Experimental — these APIs may change without following the backwards
  // compatibility mandate
  // -----------------------------------------------------------------------

  /**
   * Registers a named counter that tracks relationships matching the given filter. The counter is
   * computed asynchronously by SpiceDB.
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public void experimentalRegisterRelationshipCounter(String name, Filter filter) {
    experimentalRegisterRelationshipCounter(name, filter, null);
  }

  /**
   * As {@link #experimentalRegisterRelationshipCounter(String, Filter)}, with a per-call {@code
   * timeout} overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public void experimentalRegisterRelationshipCounter(
      String name, Filter filter, Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    callOnce(
        () ->
            experimentalStub
                .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                .experimentalRegisterRelationshipCounter(
                    ExperimentalRegisterRelationshipCounterRequest.newBuilder()
                        .setName(name)
                        .setRelationshipFilter(toRelationshipFilter(filter))
                        .build()));
  }

  /** Result of an {@link #experimentalCountRelationships} call. */
  public record CountResult(long relationshipCount, String revision, boolean stillCalculating) {}

  /**
   * Reads the value of a previously registered relationship counter.
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public CountResult experimentalCountRelationships(String name) {
    return experimentalCountRelationships(name, null);
  }

  /**
   * As {@link #experimentalCountRelationships(String)}, with a per-call {@code timeout} overriding
   * the client's default (see {@link #DEFAULT_TIMEOUT}).
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public CountResult experimentalCountRelationships(String name, Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    ExperimentalCountRelationshipsResponse resp =
        withRetry(
            () ->
                experimentalStub
                    .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                    .experimentalCountRelationships(
                        ExperimentalCountRelationshipsRequest.newBuilder().setName(name).build()));

    if (resp.getCounterStillCalculating()) {
      return new CountResult(0, "", true);
    }

    var cv = resp.getReadCounterValue();
    return new CountResult(cv.getRelationshipCount(), cv.getReadAt().getToken(), false);
  }

  /**
   * Removes a previously registered relationship counter.
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public void experimentalUnregisterRelationshipCounter(String name) {
    experimentalUnregisterRelationshipCounter(name, null);
  }

  /**
   * As {@link #experimentalUnregisterRelationshipCounter(String)}, with a per-call {@code timeout}
   * overriding the client's default (see {@link #DEFAULT_TIMEOUT}).
   *
   * <p><b>Experimental:</b> this API may change without notice.
   */
  public void experimentalUnregisterRelationshipCounter(String name, Duration timeout) {
    long timeoutMs = effectiveTimeout(timeout).toMillis();
    callOnce(
        () ->
            experimentalStub
                .withDeadlineAfter(timeoutMs, TimeUnit.MILLISECONDS)
                .experimentalUnregisterRelationshipCounter(
                    ExperimentalUnregisterRelationshipCounterRequest.newBuilder()
                        .setName(name)
                        .build()));
  }

  // -----------------------------------------------------------------------
  // AutoCloseable
  // -----------------------------------------------------------------------

  @Override
  public void close() {
    channel.shutdown();
    try {
      if (!channel.awaitTermination(5, TimeUnit.SECONDS)) {
        channel.shutdownNow();
      }
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      channel.shutdownNow();
    }
  }

  // -----------------------------------------------------------------------
  // Internal helpers
  // -----------------------------------------------------------------------

  /**
   * Resolves a per-call {@code timeout} override against {@link #defaultTimeout}. {@code null}
   * means "use the client default" -- there is deliberately no way to make an unbounded unary call.
   * See root DESIGN.md, "RULE: A unary call must have a deadline".
   */
  private Duration effectiveTimeout(Duration timeout) {
    return timeout != null ? timeout : defaultTimeout;
  }

  private static Metadata bearerMetadata(String presharedKey) {
    Metadata metadata = new Metadata();
    metadata.put(
        Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER),
        "Bearer " + presharedKey);
    return metadata;
  }

  /** Retry with exponential backoff for transient gRPC errors. */
  @FunctionalInterface
  private interface RetryableCall<T> {
    T call();
  }

  /**
   * Runs a blocking mid-stream operation (e.g. {@code serverStream.hasNext()}/{@code next()}),
   * mapping any {@link StatusRuntimeException} it throws to a typed {@link SpiceDBException}.
   *
   * <p>Unlike {@link #withRetry}, this does NOT retry — mid-stream errors (including transient
   * ones) are not safely retryable without re-issuing the whole call, since some results may have
   * already been delivered to the consumer.
   */
  private static <T> T mapStreamErrors(java.util.function.Supplier<T> op) {
    try {
      return op.get();
    } catch (StatusRuntimeException e) {
      throw ErrorMapper.toSpiceDBException(e);
    }
  }

  /**
   * Registers an {@code onClose} handler that cancels {@code cancelCtx} (and, transitively, any
   * gRPC call bound to it) when the returned stream is closed. Used by the lazy streaming methods
   * to make {@code close()} actually cancel the underlying server-streaming call, rather than
   * leaving it open server-side.
   */
  private static <T> Stream<T> cancelOnClose(
      Stream<T> stream, Context.CancellableContext cancelCtx) {
    return stream.onClose(
        () ->
            cancelCtx.cancel(
                Status.CANCELLED.withDescription("stream closed by caller").asRuntimeException()));
  }

  /**
   * Full-jitter backoff delay in milliseconds: {@code uniform(0, cap)} rather than the fixed {@code
   * cap}. Without jitter, every client in a fleet retries on the same schedule after a server
   * restart, turning the recovery into a thundering herd; sampling uniformly under the cap spreads
   * retries out instead.
   */
  private static long jitteredBackoffMs(long cap) {
    return (long) (ThreadLocalRandom.current().nextDouble() * cap);
  }

  /**
   * Retries {@code call} with full-jitter exponential backoff for transient gRPC errors. Only for
   * idempotent (read) calls -- see {@link #callOnce} for mutations.
   */
  private <T> T withRetry(RetryableCall<T> call) {
    long backoff = INITIAL_BACKOFF_MS;
    for (int attempt = 0; attempt < MAX_RETRIES; attempt++) {
      try {
        return call.call();
      } catch (StatusRuntimeException e) {
        if (!ErrorMapper.isTransient(e) || attempt == MAX_RETRIES - 1) {
          throw ErrorMapper.toSpiceDBException(e);
        }
        try {
          Thread.sleep(jitteredBackoffMs(backoff));
        } catch (InterruptedException ie) {
          Thread.currentThread().interrupt();
          throw ErrorMapper.toSpiceDBException(e);
        }
        backoff *= 2;
      }
    }
    throw new SpiceDBException("unreachable");
  }

  /**
   * Runs {@code call} once, converting a {@link StatusRuntimeException}, but never retrying.
   *
   * <p>For mutations. A {@code WriteRelationships} containing {@code OPERATION_CREATE}, or any
   * request carrying preconditions, is not idempotent: if it commits and the response is lost (a
   * rolling restart, a proxy dropping the connection), a retry would surface {@code
   * ALREADY_EXISTS}/{@code FAILED_PRECONDITION} for a write that in fact succeeded, and the caller
   * would wrongly conclude it had failed. See root DESIGN.md, "Automatic retry is for idempotent
   * operations only".
   */
  private <T> T callOnce(RetryableCall<T> call) {
    try {
      return call.call();
    } catch (StatusRuntimeException e) {
      throw ErrorMapper.toSpiceDBException(e);
    }
  }

  /**
   * Opens a server-streaming RPC and makes stream/page ESTABLISHMENT effectively retryable on
   * transient errors, reusing {@link #withRetry}'s transient predicate, {@code MAX_RETRIES}, and
   * backoff verbatim (no divergent retry policy).
   *
   * <p>For grpc-java's blocking stub, {@code stub.someStreamingMethod(request)} never throws — it
   * only enqueues the call and returns an {@link Iterator}; the RPC's actual outcome (including a
   * transient {@code UNAVAILABLE}/{@code ABORTED}) only surfaces on the iterator's first {@code
   * hasNext()}/{@code next()} call. Wrapping only the stub call in {@link #withRetry} (the old
   * code) therefore never actually retries anything — the exception always escapes on the caller's
   * first poll, past the retry loop. This method fixes that by folding the priming {@code
   * hasNext()} call INTO the retried unit of work: if it throws a transient error, {@link
   * #withRetry} re-issues {@code openCall} (a fresh RPC, e.g. from the same page cursor) after
   * backoff, exactly as it would for a unary call.
   *
   * <p><b>No-replay guarantee:</b> this method must only ever be used to open a stream/page BEFORE
   * any item has been handed to the caller. Once it returns, the returned iterator is primed (its
   * first {@code hasNext()} result is already cached), and every subsequent poll MUST go through
   * {@link #mapStreamErrors} — not this method — since re-opening after an item has already been
   * yielded would replay/duplicate it.
   */
  private <T> Iterator<T> openStreamWithRetry(RetryableCall<Iterator<T>> openCall) {
    return withRetry(
        () -> {
          Iterator<T> serverStream = openCall.call();
          // Force establishment now, inside the retried unit of work, instead of leaving it to
          // whatever unretried call the caller makes next.
          serverStream.hasNext();
          return serverStream;
        });
  }

  private Stream<Relationship> paginatedRelationshipStream(
      Consistency consistency, Filter filter, int pageSize) {
    Context.CancellableContext cancelCtx = Context.current().withCancellation();
    Iterator<Relationship> iterator =
        new Iterator<>() {
          private Cursor cursor = null;
          private final List<Relationship> buffer = new ArrayList<>();
          private int bufferIndex = 0;
          private boolean done = false;

          @Override
          public boolean hasNext() {
            if (bufferIndex < buffer.size()) return true;
            if (done) return false;
            fetchNextPage();
            return bufferIndex < buffer.size();
          }

          @Override
          public Relationship next() {
            if (!hasNext()) throw new NoSuchElementException();
            return buffer.get(bufferIndex++);
          }

          private void fetchNextPage() {
            buffer.clear();
            bufferIndex = 0;

            var reqBuilder =
                ReadRelationshipsRequest.newBuilder()
                    .setConsistency(consistency.toProto())
                    .setRelationshipFilter(toRelationshipFilter(filter))
                    .setOptionalLimit(pageSize);

            if (cursor != null) {
              reqBuilder.setOptionalCursor(cursor);
            }

            Iterator<ReadRelationshipsResponse> serverStream;
            Context previous = cancelCtx.attach();
            try {
              serverStream =
                  openStreamWithRetry(() -> permissionsStub.readRelationships(reqBuilder.build()));
            } finally {
              cancelCtx.detach(previous);
            }

            while (mapStreamErrors(serverStream::hasNext)) {
              ReadRelationshipsResponse resp = mapStreamErrors(serverStream::next);
              cursor = resp.getAfterResultCursor();
              buffer.add(fromProtoRelationship(resp.getRelationship()));
            }

            if (buffer.size() < pageSize) {
              done = true;
            }
          }
        };

    return cancelOnClose(
        StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false),
        cancelCtx);
  }

  private static CheckBulkPermissionsRequestItem checkItemFromRel(
      Relationship r, String permission, Map<String, Object> callLevelContext) {
    var builder =
        CheckBulkPermissionsRequestItem.newBuilder()
            .setResource(
                ObjectReference.newBuilder()
                    .setObjectType(r.resourceType())
                    .setObjectId(r.resourceID())
                    .build())
            .setPermission(permission)
            .setSubject(
                SubjectReference.newBuilder()
                    .setObject(
                        ObjectReference.newBuilder()
                            .setObjectType(r.subjectType())
                            .setObjectId(r.subjectID())
                            .build())
                    .setOptionalRelation(r.subjectRelation() != null ? r.subjectRelation() : "")
                    .build());

    // CHECK-TIME context only -- r.caveatContext() (write-time) is never read here. See
    // mergeCheckContext for the key-level, item-wins merge rule.
    Map<String, Object> merged = mergeCheckContext(callLevelContext, r.checkContext());
    if (merged != null) {
      builder.setContext(toProtoStruct(merged));
    }
    return builder.build();
  }

  /**
   * Merges call-level and per-item CHECK-TIME caveat context using a key-level merge where the
   * item's keys win: {@code {...callLevel, ...item}}. Call-level keys absent from {@code item} are
   * retained -- this is NOT wholesale replacement, since an item supplying one key must not
   * silently drop every call-level key (the caveat would then fail to evaluate for the dropped
   * keys, landing the caller back in the confusing CONDITIONAL_PERMISSION state this contract
   * exists to make legible). Returns {@code null} (never an empty map) when both inputs are
   * null/empty, so the caller knows to omit the wire {@code context} field entirely rather than
   * sending an empty {@code Struct}.
   */
  private static Map<String, Object> mergeCheckContext(
      Map<String, Object> callLevel, Map<String, Object> item) {
    boolean callEmpty = callLevel == null || callLevel.isEmpty();
    boolean itemEmpty = item == null || item.isEmpty();
    if (callEmpty && itemEmpty) {
      return null;
    }
    var merged = new LinkedHashMap<String, Object>();
    if (callLevel != null) {
      merged.putAll(callLevel);
    }
    if (item != null) {
      merged.putAll(item);
    }
    return merged;
  }

  /** Converts a check-time context map to a proto {@code Struct}, reusing {@link #toProtoValue}. */
  private static com.google.protobuf.Struct toProtoStruct(Map<String, Object> context) {
    var builder = com.google.protobuf.Struct.newBuilder();
    for (var entry : context.entrySet()) {
      builder.putFields(entry.getKey(), toProtoValueForKey(entry.getKey(), entry.getValue()));
    }
    return builder.build();
  }

  private static RelationshipUpdate toRelationshipUpdate(Transaction.Mutation m) {
    RelationshipUpdate.Operation op =
        switch (m.operation()) {
          case CREATE -> RelationshipUpdate.Operation.OPERATION_CREATE;
          case TOUCH -> RelationshipUpdate.Operation.OPERATION_TOUCH;
          case DELETE -> RelationshipUpdate.Operation.OPERATION_DELETE;
        };
    return RelationshipUpdate.newBuilder()
        .setOperation(op)
        .setRelationship(toProtoRelationship(m.relationship()))
        .build();
  }

  private static Precondition toPrecondition(Transaction.Precondition p) {
    Precondition.Operation op =
        switch (p.operation()) {
          case MUST_NOT_MATCH -> Precondition.Operation.OPERATION_MUST_NOT_MATCH;
          case MUST_MATCH -> Precondition.Operation.OPERATION_MUST_MATCH;
        };
    return Precondition.newBuilder()
        .setOperation(op)
        .setFilter(toRelationshipFilter(p.filter()))
        .build();
  }

  static build.buf.gen.authzed.api.v1.Relationship toProtoRelationship(Relationship r) {
    var builder =
        build.buf.gen.authzed.api.v1.Relationship.newBuilder()
            .setResource(
                ObjectReference.newBuilder()
                    .setObjectType(r.resourceType())
                    .setObjectId(r.resourceID())
                    .build())
            .setRelation(r.resourceRelation())
            .setSubject(
                SubjectReference.newBuilder()
                    .setObject(
                        ObjectReference.newBuilder()
                            .setObjectType(r.subjectType())
                            .setObjectId(r.subjectID())
                            .build())
                    .setOptionalRelation(r.subjectRelation() != null ? r.subjectRelation() : "")
                    .build());

    if (r.caveatName() != null && !r.caveatName().isEmpty()) {
      var caveatBuilder = ContextualizedCaveat.newBuilder().setCaveatName(r.caveatName());
      if (r.caveatContext() != null) {
        var structBuilder = com.google.protobuf.Struct.newBuilder();
        for (var entry : r.caveatContext().entrySet()) {
          structBuilder.putFields(
              entry.getKey(), toProtoValueForKey(entry.getKey(), entry.getValue()));
        }
        caveatBuilder.setContext(structBuilder.build());
      }
      builder.setOptionalCaveat(caveatBuilder.build());
    }

    if (r.expiration() != null) {
      builder.setOptionalExpiresAt(
          com.google.protobuf.Timestamp.newBuilder()
              .setSeconds(r.expiration().getEpochSecond())
              .setNanos(r.expiration().getNano())
              .build());
    }

    return builder.build();
  }

  static Relationship fromProtoRelationship(build.buf.gen.authzed.api.v1.Relationship pr) {
    String caveatName = null;
    Map<String, Object> caveatContext = null;
    if (pr.hasOptionalCaveat()) {
      caveatName = pr.getOptionalCaveat().getCaveatName();
      if (pr.getOptionalCaveat().hasContext()) {
        caveatContext = new HashMap<>();
        for (var entry : pr.getOptionalCaveat().getContext().getFieldsMap().entrySet()) {
          caveatContext.put(entry.getKey(), fromProtoValue(entry.getValue()));
        }
      }
    }

    Instant expiration = null;
    if (pr.hasOptionalExpiresAt()) {
      expiration =
          Instant.ofEpochSecond(
              pr.getOptionalExpiresAt().getSeconds(), pr.getOptionalExpiresAt().getNanos());
    }

    return new Relationship(
        pr.getResource().getObjectType(),
        pr.getResource().getObjectId(),
        pr.getRelation(),
        pr.getSubject().getObject().getObjectType(),
        pr.getSubject().getObject().getObjectId(),
        pr.getSubject().getOptionalRelation(),
        caveatName,
        caveatContext,
        expiration,
        // checkContext is CHECK-TIME only and never round-trips through a write/read — a
        // relationship read back from the server never carries it.
        null);
  }

  /**
   * Maps the proto {@code LookupPermissionship} enum to its native equivalent. Unrecognized values
   * map to {@code UNSPECIFIED}.
   */
  private static LookupResult.Permissionship permissionshipFromProto(LookupPermissionship v) {
    return switch (v) {
      case LOOKUP_PERMISSIONSHIP_HAS_PERMISSION -> LookupResult.Permissionship.HAS_PERMISSION;
      case LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION ->
          LookupResult.Permissionship.CONDITIONAL_PERMISSION;
      default -> LookupResult.Permissionship.UNSPECIFIED;
    };
  }

  /** Maps a proto {@code PartialCaveatInfo} to its native equivalent. A null input maps to null. */
  private static LookupResult.PartialCaveatInfo partialCaveatFromProto(
      build.buf.gen.authzed.api.v1.PartialCaveatInfo v) {
    if (v == null) {
      return null;
    }
    return new LookupResult.PartialCaveatInfo(List.copyOf(v.getMissingRequiredContextList()));
  }

  /**
   * Maps the proto {@code CheckPermissionResponse.Permissionship} enum to its native equivalent.
   * Unlike {@code LookupPermissionship} (mapped by {@link #permissionshipFromProto}), this enum has
   * a {@code NO_PERMISSION} value — the check surface answers a yes/no/conditional question about
   * one specific pair, so "no" is itself an answer. Unrecognized values map to {@code UNSPECIFIED}.
   */
  private static LookupResult.Permissionship checkPermissionshipFromProto(
      CheckPermissionResponse.Permissionship v) {
    return switch (v) {
      case PERMISSIONSHIP_HAS_PERMISSION -> LookupResult.Permissionship.HAS_PERMISSION;
      case PERMISSIONSHIP_CONDITIONAL_PERMISSION ->
          LookupResult.Permissionship.CONDITIONAL_PERMISSION;
      case PERMISSIONSHIP_NO_PERMISSION -> LookupResult.Permissionship.NO_PERMISSION;
      default -> LookupResult.Permissionship.UNSPECIFIED;
    };
  }

  /**
   * Wraps a per-item {@code google.rpc.Status} (from a bulk response pair) in a {@link
   * StatusRuntimeException} that keeps the status's own details, so a per-item failure reaches the
   * caller carrying the same structured reason an RPC-level failure does. See root DESIGN.md,
   * "RULE: Error mapping must not lose the server's detail".
   *
   * <p>The status arrives as the BSR-generated {@code build.buf.gen.google.rpc.Status} while gRPC's
   * own {@link StatusProto} works with {@code com.google.rpc.Status} -- two generated classes for
   * the same message. They are bridged by a serialize/parse round-trip rather than a field-by-field
   * copy, so no field can be forgotten as the message evolves. If the round-trip ever fails, the
   * code and message still reach the caller as a typed exception; only the details are lost.
   */
  private static StatusRuntimeException perItemStatusException(
      build.buf.gen.google.rpc.Status errorStatus, String messagePrefix) {
    String message = messagePrefix + errorStatus.getMessage();
    try {
      return StatusProto.toStatusRuntimeException(
          com.google.rpc.Status.parseFrom(errorStatus.toByteArray()).toBuilder()
              .setMessage(message)
              .build());
    } catch (com.google.protobuf.InvalidProtocolBufferException e) {
      return Status.fromCodeValue(errorStatus.getCode())
          .withDescription(message)
          .asRuntimeException();
    }
  }

  /**
   * Maps a proto {@code CheckBulkPermissionsResponseItem} (one pair's successful result from a
   * CheckBulkPermissions call) to a native {@link CheckResult}. {@code
   * CheckBulkPermissionsResponseItem} carries no per-item {@code checked_at} of its own — the token
   * lives once on the enclosing {@code CheckBulkPermissionsResponse} and applies to every pair in
   * it, so callers pass it in as {@code responseCheckedAt} to propagate onto each item.
   */
  private static CheckResult checkResultFromBulkItem(
      CheckBulkPermissionsResponseItem item, String responseCheckedAt) {
    return new CheckResult(
        checkPermissionshipFromProto(item.getPermissionship()),
        item.hasPartialCaveatInfo()
            ? item.getPartialCaveatInfo().getMissingRequiredContextList()
            : List.of(),
        responseCheckedAt);
  }

  /**
   * Maps a proto {@code LookupResourcesResponse} to a native {@link LookupResult.LookupResource}.
   */
  private static LookupResult.LookupResource lookupResourceFromProto(LookupResourcesResponse resp) {
    return new LookupResult.LookupResource(
        resp.getResourceObjectId(),
        permissionshipFromProto(resp.getPermissionship()),
        partialCaveatFromProto(resp.hasPartialCaveatInfo() ? resp.getPartialCaveatInfo() : null),
        resp.getLookedUpAt().getToken());
  }

  /**
   * Maps a proto {@code ResolvedSubject} to its native equivalent. A null input maps to a
   * zero-value {@link LookupResult.ResolvedSubject} (empty {@code subjectId}), which callers use as
   * the trigger for falling back to deprecated response-level fields.
   */
  private static LookupResult.ResolvedSubject resolvedSubjectFromProto(
      build.buf.gen.authzed.api.v1.ResolvedSubject v) {
    if (v == null) {
      return new LookupResult.ResolvedSubject("", LookupResult.Permissionship.UNSPECIFIED, null);
    }
    return new LookupResult.ResolvedSubject(
        v.getSubjectObjectId(),
        permissionshipFromProto(v.getPermissionship()),
        partialCaveatFromProto(v.hasPartialCaveatInfo() ? v.getPartialCaveatInfo() : null));
  }

  /**
   * Maps a proto {@code LookupSubjectsResponse} to a native {@link LookupResult.LookupSubject},
   * falling back to the deprecated {@code subject_object_id}/{@code permissionship}/{@code
   * partial_caveat_info} fields when {@code subject} isn't populated (older servers), and to the
   * deprecated {@code excluded_subject_ids} (IDs only, no permissionship/caveat info) when {@code
   * excluded_subjects} isn't populated. Mirrors {@code spicedb-go}'s {@code lookup.go}.
   */
  @SuppressWarnings("deprecation") // intentional fallback to deprecated fields, see below
  private static LookupResult.LookupSubject lookupSubjectFromProto(LookupSubjectsResponse resp) {
    LookupResult.ResolvedSubject subject =
        resp.hasSubject() ? resolvedSubjectFromProto(resp.getSubject()) : null;
    if (subject == null || subject.subjectId().isEmpty()) {
      // Fall back to the deprecated top-level fields for servers that don't yet populate the
      // non-deprecated `subject` field.
      subject =
          new LookupResult.ResolvedSubject(
              resp.getSubjectObjectId(),
              permissionshipFromProto(resp.getPermissionship()),
              partialCaveatFromProto(
                  resp.hasPartialCaveatInfo() ? resp.getPartialCaveatInfo() : null));
    }

    List<LookupResult.ResolvedSubject> excluded;
    if (!resp.getExcludedSubjectsList().isEmpty()) {
      excluded =
          resp.getExcludedSubjectsList().stream()
              .map(SpiceDBClient::resolvedSubjectFromProto)
              .toList();
    } else if (!resp.getExcludedSubjectIdsList().isEmpty()) {
      // Fall back to the deprecated excluded_subject_ids field, which carries only IDs (no
      // permissionship/caveat info).
      excluded =
          resp.getExcludedSubjectIdsList().stream()
              .map(
                  id ->
                      new LookupResult.ResolvedSubject(
                          id, LookupResult.Permissionship.UNSPECIFIED, null))
              .toList();
    } else {
      excluded = List.of();
    }

    return new LookupResult.LookupSubject(subject, excluded, resp.getLookedUpAt().getToken());
  }

  /**
   * Recursively maps a proto {@code PermissionRelationshipTree} to its native {@link
   * PermissionTree} representation. A null input maps to a zero-value tree.
   */
  static PermissionTree toPermissionTree(PermissionRelationshipTree t) {
    if (t == null) {
      return new PermissionTree(new PermissionTree.ObjectRef("", ""), "", null, null);
    }

    PermissionTree.IntermediateNode intermediate = null;
    if (t.hasIntermediate()) {
      AlgebraicSubjectSet algebraic = t.getIntermediate();
      var children = new ArrayList<PermissionTree>(algebraic.getChildrenCount());
      for (var child : algebraic.getChildrenList()) {
        children.add(toPermissionTree(child));
      }
      intermediate =
          new PermissionTree.IntermediateNode(
              toTreeOperation(algebraic.getOperation()), List.copyOf(children));
    }

    PermissionTree.LeafNode leaf = null;
    if (t.hasLeaf()) {
      DirectSubjectSet direct = t.getLeaf();
      var subjects = new ArrayList<PermissionTree.SubjectRef>(direct.getSubjectsCount());
      for (var subject : direct.getSubjectsList()) {
        subjects.add(
            new PermissionTree.SubjectRef(
                subject.getObject().getObjectType(),
                subject.getObject().getObjectId(),
                subject.getOptionalRelation()));
      }
      leaf = new PermissionTree.LeafNode(List.copyOf(subjects));
    }

    return new PermissionTree(
        new PermissionTree.ObjectRef(
            t.getExpandedObject().getObjectType(), t.getExpandedObject().getObjectId()),
        t.getExpandedRelation(),
        intermediate,
        leaf);
  }

  /** Maps the proto algebraic set operation to its native equivalent. */
  private static PermissionTree.Operation toTreeOperation(AlgebraicSubjectSet.Operation op) {
    return switch (op) {
      case OPERATION_UNION -> PermissionTree.Operation.UNION;
      case OPERATION_INTERSECTION -> PermissionTree.Operation.INTERSECTION;
      case OPERATION_EXCLUSION -> PermissionTree.Operation.EXCLUSION;
      default -> PermissionTree.Operation.UNSPECIFIED;
    };
  }

  /**
   * Converts a {@link Filter} to its proto representation.
   *
   * @throws InvalidArgumentException if {@code subjectID} or {@code subjectRelation} is set without
   *     {@code subjectType}. The wire's {@code SubjectFilter.subject_type} is a required field, so
   *     there is no way to express a subject ID/relation constraint without it, which makes
   *     silently dropping the constraint the one unsafe resolution: a caller who wrote {@code
   *     Filter.of("document").withSubjectID("alice")}, expecting to narrow to alice's
   *     relationships, would instead match every subject on every document -- e.g. {@code
   *     deleteRelationships} would delete every relationship on every document, not just alice's.
   *     See root DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail", clause 1.
   */
  private static RelationshipFilter toRelationshipFilter(Filter f) {
    var builder = RelationshipFilter.newBuilder().setResourceType(f.resourceType());

    if (f.resourceID() != null && !f.resourceID().isEmpty()) {
      builder.setOptionalResourceId(f.resourceID());
    }
    if (f.resourceIDPrefix() != null && !f.resourceIDPrefix().isEmpty()) {
      builder.setOptionalResourceIdPrefix(f.resourceIDPrefix());
    }
    if (f.relation() != null && !f.relation().isEmpty()) {
      builder.setOptionalRelation(f.relation());
    }
    boolean hasSubjectID = f.subjectID() != null && !f.subjectID().isEmpty();
    boolean hasSubjectRelation = f.subjectRelation() != null && !f.subjectRelation().isEmpty();
    if (f.subjectType() != null && !f.subjectType().isEmpty()) {
      var subjectBuilder = SubjectFilter.newBuilder().setSubjectType(f.subjectType());
      if (hasSubjectID) {
        subjectBuilder.setOptionalSubjectId(f.subjectID());
      }
      if (hasSubjectRelation) {
        subjectBuilder.setOptionalRelation(
            SubjectFilter.RelationFilter.newBuilder().setRelation(f.subjectRelation()).build());
      }
      builder.setOptionalSubjectFilter(subjectBuilder.build());
    } else if (hasSubjectID || hasSubjectRelation) {
      String missing = hasSubjectID ? "subjectID" : "subjectRelation";
      throw new InvalidArgumentException(
          "Filter has "
              + missing
              + " set without subjectType. The wire format requires subjectType whenever a"
              + " subject constraint is present -- call withSubjectType(...) before with"
              + Character.toUpperCase(missing.charAt(0))
              + missing.substring(1)
              + "(...).");
    }
    return builder.build();
  }

  private Update updateFromProto(RelationshipUpdate pu) {
    // Server-supplied data: an unrecognized operation (OPERATION_UNSPECIFIED, or a future wire
    // value added after this client shipped) MUST NOT map to a write. Mirrors toTreeOperation and
    // both permissionship mappers in this file, which already map an unrecognized server enum to
    // their safe UNSPECIFIED value rather than raising or guessing. Root DESIGN.md, "RULE: A
    // conversion that cannot preserve meaning must fail", clause 2: server-supplied values the
    // client does not recognise MUST NOT raise, and MUST map to the safe, non-permissive default
    // -- never a grant, and never a write. Mapping to TOUCH here would let a cache or index mirror
    // consuming the watch stream upsert a relationship that may in fact have been deleted.
    UpdateOperation op =
        switch (pu.getOperation()) {
          case OPERATION_CREATE -> UpdateOperation.CREATE;
          case OPERATION_TOUCH -> UpdateOperation.TOUCH;
          case OPERATION_DELETE -> UpdateOperation.DELETE;
          default -> UpdateOperation.UNSPECIFIED;
        };
    return new Update(op, fromProtoRelationship(pu.getRelationship()));
  }

  private SchemaDiff schemaDiffFromProto(ReflectionSchemaDiff d) {
    // Map each diff case to a descriptive kind string
    if (d.hasDefinitionAdded()) {
      return new SchemaDiff("definition_added", d.getDefinitionAdded().getName(), "", "", "");
    } else if (d.hasDefinitionRemoved()) {
      return new SchemaDiff("definition_removed", d.getDefinitionRemoved().getName(), "", "", "");
    } else if (d.hasDefinitionDocCommentChanged()) {
      return new SchemaDiff(
          "definition_doc_comment_changed",
          d.getDefinitionDocCommentChanged().getName(),
          "",
          "",
          "");
    } else if (d.hasRelationAdded()) {
      return new SchemaDiff(
          "relation_added",
          d.getRelationAdded().getParentDefinitionName(),
          d.getRelationAdded().getName(),
          "",
          "");
    } else if (d.hasRelationRemoved()) {
      return new SchemaDiff(
          "relation_removed",
          d.getRelationRemoved().getParentDefinitionName(),
          d.getRelationRemoved().getName(),
          "",
          "");
    } else if (d.hasRelationDocCommentChanged()) {
      return new SchemaDiff(
          "relation_doc_comment_changed",
          d.getRelationDocCommentChanged().getParentDefinitionName(),
          d.getRelationDocCommentChanged().getName(),
          "",
          "");
    } else if (d.hasRelationSubjectTypeAdded()) {
      return new SchemaDiff(
          "relation_subject_type_added",
          d.getRelationSubjectTypeAdded().getRelation().getParentDefinitionName(),
          d.getRelationSubjectTypeAdded().getRelation().getName(),
          "",
          "");
    } else if (d.hasRelationSubjectTypeRemoved()) {
      return new SchemaDiff(
          "relation_subject_type_removed",
          d.getRelationSubjectTypeRemoved().getRelation().getParentDefinitionName(),
          d.getRelationSubjectTypeRemoved().getRelation().getName(),
          "",
          "");
    } else if (d.hasPermissionAdded()) {
      return new SchemaDiff(
          "permission_added",
          d.getPermissionAdded().getParentDefinitionName(),
          "",
          d.getPermissionAdded().getName(),
          "");
    } else if (d.hasPermissionRemoved()) {
      return new SchemaDiff(
          "permission_removed",
          d.getPermissionRemoved().getParentDefinitionName(),
          "",
          d.getPermissionRemoved().getName(),
          "");
    } else if (d.hasPermissionDocCommentChanged()) {
      return new SchemaDiff(
          "permission_doc_comment_changed",
          d.getPermissionDocCommentChanged().getParentDefinitionName(),
          "",
          d.getPermissionDocCommentChanged().getName(),
          "");
    } else if (d.hasPermissionExprChanged()) {
      return new SchemaDiff(
          "permission_expr_changed",
          d.getPermissionExprChanged().getParentDefinitionName(),
          "",
          d.getPermissionExprChanged().getName(),
          "");
    } else if (d.hasCaveatAdded()) {
      return new SchemaDiff("caveat_added", "", "", "", d.getCaveatAdded().getName());
    } else if (d.hasCaveatRemoved()) {
      return new SchemaDiff("caveat_removed", "", "", "", d.getCaveatRemoved().getName());
    } else if (d.hasCaveatDocCommentChanged()) {
      return new SchemaDiff(
          "caveat_doc_comment_changed", "", "", "", d.getCaveatDocCommentChanged().getName());
    } else if (d.hasCaveatExprChanged()) {
      return new SchemaDiff("caveat_expr_changed", "", "", "", d.getCaveatExprChanged().getName());
    } else if (d.hasCaveatParameterAdded()) {
      return new SchemaDiff(
          "caveat_parameter_added", "", "", "", d.getCaveatParameterAdded().getParentCaveatName());
    } else if (d.hasCaveatParameterRemoved()) {
      return new SchemaDiff(
          "caveat_parameter_removed",
          "",
          "",
          "",
          d.getCaveatParameterRemoved().getParentCaveatName());
    } else if (d.hasCaveatParameterTypeChanged()) {
      return new SchemaDiff(
          "caveat_parameter_type_changed",
          "",
          "",
          "",
          d.getCaveatParameterTypeChanged().getParameter().getParentCaveatName());
    }
    return new SchemaDiff("unknown", "", "", "", "");
  }

  /**
   * Converts a native Java value into a protobuf {@code Value} by dispatching on type, recursing
   * into nested {@link Map}/{@link List} values. This is the single converter for caveat context on
   * both surfaces: check-time (merged in {@link #mergeCheckContext}, sent via {@link
   * #toProtoStruct}) and write-time (a relationship's stored {@code caveatContext}, in {@link
   * #toProtoRelationship}). A numeric/boolean/null/nested value lands on its matching {@code kind}
   * oneof case instead of being stringified, so a caveat comparing a typed parameter (e.g. a
   * schema's {@code now < 100} against an {@code int}) evaluates correctly on either surface -- and
   * on the write path, evaluates correctly on every future check against the stored relationship,
   * since a bad write-time context is persisted rather than failing just once.
   *
   * @throws InvalidArgumentException if {@code value}'s type cannot be represented on the wire
   *     (e.g. a custom class instance). Caveat context is caller-supplied, so per root {@code
   *     DESIGN.md} "RULE: A conversion that cannot preserve meaning must fail", clause 1, this
   *     raises a typed error naming the unsupported type instead of silently stringifying it.
   */
  private static com.google.protobuf.Value toProtoValue(Object value) {
    if (value == null) {
      return com.google.protobuf.Value.newBuilder()
          .setNullValue(com.google.protobuf.NullValue.NULL_VALUE)
          .build();
    } else if (value instanceof Boolean b) {
      return com.google.protobuf.Value.newBuilder().setBoolValue(b).build();
    } else if (value instanceof Number n) {
      return com.google.protobuf.Value.newBuilder().setNumberValue(n.doubleValue()).build();
    } else if (value instanceof String s) {
      return com.google.protobuf.Value.newBuilder().setStringValue(s).build();
    } else if (value instanceof Map<?, ?> m) {
      var structBuilder = com.google.protobuf.Struct.newBuilder();
      for (var entry : m.entrySet()) {
        structBuilder.putFields(
            String.valueOf(entry.getKey()),
            toProtoValueForKey(String.valueOf(entry.getKey()), entry.getValue()));
      }
      return com.google.protobuf.Value.newBuilder().setStructValue(structBuilder.build()).build();
    } else if (value instanceof List<?> l) {
      var listBuilder = com.google.protobuf.ListValue.newBuilder();
      for (var item : l) {
        listBuilder.addValues(toProtoValue(item));
      }
      return com.google.protobuf.Value.newBuilder().setListValue(listBuilder.build()).build();
    } else {
      // A value this conversion cannot represent came from the caller, who can see this error and
      // fix their input -- stringifying it instead would silently produce a caveat context value
      // SpiceDB never intended. Shared by both the check path (toProtoStruct) and the write path
      // (toProtoRelationship).
      throw new InvalidArgumentException(
          "unsupported caveat context value type: " + value.getClass().getName());
    }
  }

  /**
   * Calls {@link #toProtoValue} for one caveat-context entry, and -- if it throws {@link
   * InvalidArgumentException} because {@code value}'s type cannot be represented -- re-raises with
   * {@code key} named, so the caller can tell which entry in their context map needs fixing rather
   * than just "some value, somewhere." For a nested {@link Map}, the innermost failure names its
   * own (nested) key first, and each enclosing call adds its key in turn, so the message traces the
   * full path to the offending entry.
   */
  private static com.google.protobuf.Value toProtoValueForKey(String key, Object value) {
    try {
      return toProtoValue(value);
    } catch (InvalidArgumentException e) {
      throw new InvalidArgumentException("caveat context key \"" + key + "\": " + e.getMessage());
    }
  }

  /**
   * Converts a protobuf {@code Value} into a native Java value by dispatching on its {@code kind}
   * oneof -- the read-side inverse of {@link #toProtoValue}, recursing into nested {@code
   * Struct}/{@code ListValue} values so a relationship read back via {@link #fromProtoRelationship}
   * doesn't lose the shape of a caveat context it wrote with {@link #toProtoValue}.
   */
  private static Object fromProtoValue(com.google.protobuf.Value value) {
    return switch (value.getKindCase()) {
      case NULL_VALUE -> null;
      case BOOL_VALUE -> value.getBoolValue();
      case NUMBER_VALUE -> value.getNumberValue();
      case STRING_VALUE -> value.getStringValue();
      case STRUCT_VALUE -> {
        Map<String, Object> m = new LinkedHashMap<>();
        for (var entry : value.getStructValue().getFieldsMap().entrySet()) {
          m.put(entry.getKey(), fromProtoValue(entry.getValue()));
        }
        yield m;
      }
      case LIST_VALUE -> {
        List<Object> l = new ArrayList<>();
        for (var item : value.getListValue().getValuesList()) {
          l.add(fromProtoValue(item));
        }
        yield l;
      }
      default -> null;
    };
  }
}
