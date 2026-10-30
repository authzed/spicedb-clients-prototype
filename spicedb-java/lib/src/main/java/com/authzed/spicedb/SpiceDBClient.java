package com.authzed.spicedb;

import com.authzed.api.v1.*;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.LocalValidationException;
import com.authzed.spicedb.errors.SpiceDBException;
import com.authzed.spicedb.protoconv.ProtoConv;
import io.grpc.Channel;
import io.grpc.ClientInterceptors;
import io.grpc.Context;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.protobuf.StatusProto;
import io.grpc.stub.MetadataUtils;
import java.net.URI;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.TimeUnit;
import java.util.function.Consumer;
import java.util.stream.Stream;
import java.util.stream.StreamSupport;

public final class SpiceDBClient implements SpiceDBApi {

  private static final int DEFAULT_CHECK_BATCH_SIZE = 1_000;

  public static final Duration DEFAULT_TIMEOUT = Duration.ofSeconds(30);

  /** The channel this client owns and shuts down on close; null for a caller-owned channel. */
  private final ManagedChannel channel;

  private final PermissionsServiceGrpc.PermissionsServiceBlockingStub permissionsStub;
  private final SchemaServiceGrpc.SchemaServiceBlockingStub schemaStub;
  private final WatchServiceGrpc.WatchServiceBlockingStub watchStub;
  private final ExperimentalServiceGrpc.ExperimentalServiceBlockingStub experimentalStub;
  private final PermissionsServiceGrpc.PermissionsServiceStub permissionsAsyncStub;
  private final Duration defaultTimeout;
  private final DefaultDeadline defaultDeadline;
  private final RetryPolicy retryPolicy;
  private final boolean retriesEnabled;
  private final boolean streamRetriesEnabled;
  private final java.util.Map<ClientOperation, RetryPolicy> operationRetryPolicies;

  private final Channel authenticatedChannel;

  /**
   * The channel whose connectivity {@link #awaitReady} watches; null when it cannot be observed.
   */
  private final ManagedChannel readinessChannel;

  /** The endpoint readiness errors name. */
  private final String endpointLabel;

  private SpiceDBClient(ManagedChannel channel, Metadata metadata) {
    this(channel, metadata, DEFAULT_TIMEOUT);
  }

  private SpiceDBClient(ManagedChannel channel, Metadata metadata, Duration defaultTimeout) {
    this(
        channel, channel, metadata, ClientOptions.builder().defaultTimeout(defaultTimeout).build());
  }

  /**
   * @param base the channel calls go through
   * @param owned the channel to shut down on close, or null when {@code base} is caller-owned
   */
  private SpiceDBClient(
      Channel base, ManagedChannel owned, Metadata metadata, ClientOptions options) {
    this(base, owned, metadata, options, base.authority());
  }

  private SpiceDBClient(
      Channel base,
      ManagedChannel owned,
      Metadata metadata,
      ClientOptions options,
      String endpointLabel) {
    ProtobufRuntime.requireSupported();
    GrpcRuntime.requireSupported();
    this.channel = owned;
    this.readinessChannel =
        owned != null ? owned : base instanceof ManagedChannel managed ? managed : null;
    this.endpointLabel = endpointLabel;
    Duration defaultTimeout = options.defaultTimeout();
    if (defaultTimeout == null || defaultTimeout.isZero() || defaultTimeout.isNegative())
      throw new LocalValidationException("default timeout must be positive");
    this.defaultTimeout = defaultTimeout;
    this.defaultDeadline = options.defaultDeadline();
    this.retryPolicy = options.retryPolicy();
    this.retriesEnabled = options.retries() == Retries.ENABLED;
    this.streamRetriesEnabled = options.streamRetries() == StreamRetries.ENABLED;
    this.operationRetryPolicies = options.operationRetryPolicies();
    this.authenticatedChannel =
        metadata.keys().isEmpty()
            ? base
            : ClientInterceptors.intercept(
                base, MetadataUtils.newAttachHeadersInterceptor(metadata));
    this.permissionsStub = PermissionsServiceGrpc.newBlockingStub(authenticatedChannel);
    this.schemaStub = SchemaServiceGrpc.newBlockingStub(authenticatedChannel);
    this.watchStub = WatchServiceGrpc.newBlockingStub(authenticatedChannel);
    this.experimentalStub = ExperimentalServiceGrpc.newBlockingStub(authenticatedChannel);
    this.permissionsAsyncStub = PermissionsServiceGrpc.newStub(authenticatedChannel);
  }

  /**
   * Builds a client over a caller-owned channel: an in-process or buffered-listener channel, a
   * channel from SpiceDB's test server, or one with custom transport setup. The channel already
   * carries its own transport security and credentials, so this sends no preshared key; wrap the
   * channel with an interceptor if the server needs one.
   *
   * <p>{@link #close()} never shuts the channel down; it stays the caller's. Operation behavior
   * from {@code options} (default timeout and deadline mode, retry policy) applies; transport and
   * credential options (TLS material, {@code remotePlaintextCredentials}) do not and are rejected.
   */
  public static SpiceDBClient fromChannel(Channel channel) {
    return fromChannel(channel, ClientOptions.builder().build());
  }

  /** See {@link #fromChannel(Channel)}. */
  public static SpiceDBClient fromChannel(Channel channel, ClientOptions options) {
    ProtobufRuntime.requireSupported();
    GrpcRuntime.requireSupported();
    if (channel == null) throw new LocalValidationException("channel must not be null");
    if (options == null) throw new LocalValidationException("options must not be null");
    if (options.caCertificate() != null
        || options.clientCertificate() != null
        || options.clientKey() != null
        || options.remotePlaintextCredentials() != RemotePlaintextCredentials.REJECT)
      throw new LocalValidationException(
          "fromChannel: the supplied channel already carries its transport and credentials;"
              + " TLS and remotePlaintextCredentials options do not apply");
    return new SpiceDBClient(channel, null, new Metadata(), options);
  }

  public static SpiceDBClient createPlaintext(String endpoint, String presharedKey) {
    return createPlaintext(endpoint, presharedKey, ClientOptions.builder().build());
  }

  /**
   * Creates an authenticated plaintext client. Remote endpoints require the explicit opt-in {@code
   * ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW)}.
   *
   * <p>The endpoint is {@code host:port}, {@code http://host:port} or {@code unix:/path}; any other
   * form (including {@code https://} and a host without a port) throws {@link
   * InvalidArgumentException} naming the problem.
   */
  public static SpiceDBClient createPlaintext(
      String endpoint, String presharedKey, ClientOptions options) {
    return createPlaintext(endpoint, presharedKey, options, NO_CHANNEL_CUSTOMIZATION);
  }

  /**
   * Like {@link #createPlaintext(String, String, ClientOptions)}, then passes the channel builder
   * to {@code channelCustomizer} before the channel is built, for transport settings {@code
   * ClientOptions} does not cover, such as keepalive:
   *
   * <pre>{@code
   * SpiceDBClient.createPlaintext(endpoint, key, options,
   *     b -> b.keepAliveTime(60, TimeUnit.SECONDS).keepAliveTimeout(10, TimeUnit.SECONDS));
   * }</pre>
   *
   * <p>The customizer runs after the endpoint guard and the plaintext decision. It must not change
   * the transport security (for example by calling {@code useTransportSecurity()}); the client's
   * guards cannot see what it does.
   */
  public static SpiceDBClient createPlaintext(
      String endpoint,
      String presharedKey,
      ClientOptions options,
      Consumer<ManagedChannelBuilder<?>> channelCustomizer) {
    ProtobufRuntime.requireSupported();
    GrpcRuntime.requireSupported();
    if (options == null) throw new LocalValidationException("options must not be null");
    if (channelCustomizer == null)
      throw new LocalValidationException("channelCustomizer must not be null");
    if (options.caCertificate() != null
        || options.clientCertificate() != null
        || options.clientKey() != null)
      throw new LocalValidationException("TLS certificates cannot be used with plaintext");
    var parsed = Endpoint.parse(endpoint).forTransport(true, "createSystemTls", "createPlaintext");
    requireInsecureTransportAllowed(
        parsed, true, options.remotePlaintextCredentials() == RemotePlaintextCredentials.ALLOW);
    var metadata = bearerMetadata(presharedKey);
    var builder = channelBuilder(parsed).usePlaintext();
    channelCustomizer.accept(builder);
    return ownedClient(parsed, builder.build(), metadata, options);
  }

  public static SpiceDBClient createSystemTls(String endpoint, String presharedKey) {
    return createSystemTls(endpoint, presharedKey, ClientOptions.builder().build());
  }

  /**
   * Creates an authenticated TLS client using the JVM trust store, or the CA in {@code options}.
   *
   * <p>The endpoint is {@code host[:port]} or {@code https://host[:port]} (443 by default); any
   * other form (including {@code http://}) throws {@link InvalidArgumentException} naming the
   * problem.
   */
  public static SpiceDBClient createSystemTls(
      String endpoint, String presharedKey, ClientOptions options) {
    return createSystemTls(endpoint, presharedKey, options, NO_CHANNEL_CUSTOMIZATION);
  }

  /**
   * Like {@link #createSystemTls(String, String, ClientOptions)}, then passes the channel builder
   * to {@code channelCustomizer} before the channel is built, for transport settings {@code
   * ClientOptions} does not cover, such as keepalive:
   *
   * <pre>{@code
   * SpiceDBClient.createSystemTls(endpoint, key, options,
   *     b -> b.keepAliveTime(60, TimeUnit.SECONDS).keepAliveWithoutCalls(true));
   * }</pre>
   *
   * <p>The customizer runs after the TLS configuration from {@code options} is applied. It must not
   * change the transport security (for example by calling {@code usePlaintext()}, which would send
   * the key in cleartext); the client's guards cannot see what it does.
   */
  public static SpiceDBClient createSystemTls(
      String endpoint,
      String presharedKey,
      ClientOptions options,
      Consumer<ManagedChannelBuilder<?>> channelCustomizer) {
    ProtobufRuntime.requireSupported();
    GrpcRuntime.requireSupported();
    if (options == null) throw new LocalValidationException("options must not be null");
    if (channelCustomizer == null)
      throw new LocalValidationException("channelCustomizer must not be null");
    var parsed = Endpoint.parse(endpoint).forTransport(false, "createSystemTls", "createPlaintext");
    var metadata = bearerMetadata(presharedKey);
    var builder = nettyBuilder(parsed);
    // Each input is parsed on its own so a failure names it, rather than surfacing as an
    // unexplained handshake failure on the first call. See root DESIGN.md, "Reject unusable
    // credentials and trust material at construction".
    var tls = io.grpc.netty.shaded.io.grpc.netty.GrpcSslContexts.forClient();
    if (options.caCertificate() != null) {
      var ca = requireCertificates("caCertificate", options.caCertificate());
      tls.trustManager(ca);
    }
    if (options.clientCertificate() != null) {
      var chain = requireCertificates("clientCertificate", options.clientCertificate());
      try {
        tls.keyManager(options.clientCertificate().toFile(), options.clientKey().toFile());
      } catch (IllegalArgumentException e) {
        throw new LocalValidationException(
            "clientKey "
                + options.clientKey()
                + " is not a PEM-encoded private key for clientCertificate ("
                + e.getMessage()
                + ")",
            e);
      }
      requireKeyMatchesCertificate(options.clientKey(), options.clientCertificate(), chain[0]);
    }
    try {
      builder.sslContext(tls.build());
    } catch (java.io.IOException | IllegalArgumentException e) {
      throw new LocalValidationException("invalid TLS configuration: " + e.getMessage(), e);
    }
    channelCustomizer.accept(builder);
    return ownedClient(parsed, builder.build(), metadata, options);
  }

  /**
   * The channel builder for {@code endpoint}: gRPC's default provider, or for a {@code unix:}
   * socket Netty's native epoll domain-socket transport (see {@link #nettyBuilder}).
   */
  private static ManagedChannelBuilder<?> channelBuilder(Endpoint endpoint) {
    if (endpoint.scheme() == Endpoint.Scheme.UNIX) return nettyBuilder(endpoint);
    return ManagedChannelBuilder.forTarget(endpoint.target());
  }

  /**
   * A Netty channel builder for {@code endpoint}. A {@code unix:} socket needs Netty's native epoll
   * transport (Linux); where it is unavailable (macOS, Windows, or a platform without the native
   * library) the endpoint is rejected here, at construction, naming the accepted forms, rather than
   * building a client whose every call fails.
   */
  private static io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder nettyBuilder(
      Endpoint endpoint) {
    var builder =
        io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder.forTarget(endpoint.target());
    if (endpoint.scheme() != Endpoint.Scheme.UNIX) return builder;
    if (!io.grpc.netty.shaded.io.netty.channel.epoll.Epoll.isAvailable())
      throw new LocalValidationException(
          Endpoint.invalid(
                  endpoint.raw(),
                  "Unix domain sockets are not supported by the gRPC transport on this platform"
                      + " (they need Netty's native epoll transport, available on Linux)")
              .getMessage(),
          io.grpc.netty.shaded.io.netty.channel.epoll.Epoll.unavailabilityCause());
    return builder
        .channelType(io.grpc.netty.shaded.io.netty.channel.epoll.EpollDomainSocketChannel.class)
        .eventLoopGroup(DomainSockets.GROUP);
  }

  /** The event loop shared by domain-socket clients: daemon threads, created on first use. */
  private static final class DomainSockets {
    static final io.grpc.netty.shaded.io.netty.channel.EventLoopGroup GROUP =
        new io.grpc.netty.shaded.io.netty.channel.epoll.EpollEventLoopGroup(
            0,
            new io.grpc.netty.shaded.io.netty.util.concurrent.DefaultThreadFactory(
                "spicedb-uds", true));
  }

  /** A client that owns {@code managed}, with connection failures naming {@code endpoint}. */
  private static SpiceDBClient ownedClient(
      Endpoint endpoint, ManagedChannel managed, Metadata metadata, ClientOptions options) {
    return new SpiceDBClient(
        ClientInterceptors.intercept(managed, endpoint.connectionErrorInterceptor()),
        managed,
        metadata,
        options,
        endpoint.raw());
  }

  /**
   * Advanced transport configuration. Custom options can supersede TLS assumptions; callers own
   * their safety.
   */
  public static SpiceDBClient create(
      String endpoint, String presharedKey, ClientOption... options) {
    return create(endpoint, presharedKey, DEFAULT_TIMEOUT, null, options);
  }

  static SpiceDBClient create(
      String endpoint,
      String presharedKey,
      Duration defaultTimeout,
      ManagedChannelBuilder<?> testChannelBuilder,
      ClientOption... options) {
    ProtobufRuntime.requireSupported();
    GrpcRuntime.requireSupported();
    // Detect the two well-known named options by reference -- see root DESIGN.md, "RULE:
    // Credentials over insecure transport require an explicit opt-in", and the public security
    // notes on create(...) and ClientOption#apply above (not just this comment) for what this
    // does and does not catch: a fully custom ClientOption lambda that calls
    // builder.usePlaintext() directly is invisible to this check.
    if (options == null) throw new LocalValidationException("options must not be null");
    for (ClientOption option : options)
      if (option == null) throw new LocalValidationException("options must not contain null");
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
    var parsed =
        Endpoint.parse(endpoint)
            .forTransport(
                insecureRequested,
                "create(...) without withInsecure()",
                "create(..., withInsecure())");
    requireInsecureTransportAllowed(parsed, insecureRequested, allowInsecureRemoteCredentials);
    var metadata = bearerMetadata(presharedKey);

    var builder = testChannelBuilder != null ? testChannelBuilder : channelBuilder(parsed);
    for (ClientOption option : options) {
      option.apply(builder);
    }
    return ownedClient(
        parsed,
        builder.build(),
        metadata,
        ClientOptions.builder().defaultTimeout(defaultTimeout).build());
  }

  static SpiceDBClient forChannel(ManagedChannel channel) {
    return new SpiceDBClient(channel, new Metadata());
  }

  static SpiceDBClient forChannel(ManagedChannel channel, Duration defaultTimeout) {
    return new SpiceDBClient(channel, new Metadata(), defaultTimeout);
  }

  static SpiceDBClient forChannel(ManagedChannel channel, ClientOptions options) {
    return new SpiceDBClient(channel, channel, new Metadata(), options);
  }

  private static final Consumer<ManagedChannelBuilder<?>> NO_CHANNEL_CUSTOMIZATION = builder -> {};

  @FunctionalInterface
  public interface ClientOption {

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

  public static ClientOption withInsecure() {
    return INSECURE_OPTION;
  }

  public static ClientOption allowInsecureRemoteCredentials() {
    return ALLOW_INSECURE_REMOTE_CREDENTIALS_OPTION;
  }

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
   * Runs on the parsed endpoint, after {@link Endpoint#parse} has rejected malformed forms, so this
   * message only ever describes a well-formed remote host.
   */
  private static void requireInsecureTransportAllowed(
      Endpoint endpoint, boolean insecure, boolean allowInsecureRemoteCredentials) {
    if (insecure
        && !allowInsecureRemoteCredentials
        && !isLoopbackEndpoint(endpoint.loopbackTarget())) {
      throw new LocalValidationException(remotePlaintextRefusal(endpoint.raw()));
    }
  }

  /**
   * The remote-plaintext refusal. It names every choice that allowing it takes (the plaintext
   * transport and the credentials opt-in), spelled as a caller writes them, since a Docker Compose
   * service name such as {@code spicedb:50051} needs both.
   */
  static String remotePlaintextRefusal(String endpoint) {
    return "spicedb: refusing to send credentials over plaintext to non-loopback endpoint \""
        + endpoint
        + "\". To allow it, choose plaintext and opt in to remote plaintext credentials:"
        + " SpiceDBClient.createPlaintext(endpoint, key, ClientOptions.builder()"
        + ".remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build()),"
        + " or SpiceDBClient.create(endpoint, key, SpiceDBClient.withInsecure(),"
        + " SpiceDBClient.allowInsecureRemoteCredentials()). Otherwise use TLS:"
        + " SpiceDBClient.createSystemTls(endpoint, key)";
  }

  // -----------------------------------------------------------------------
  // Checks — singular and chunked bulk RPCs
  // -----------------------------------------------------------------------

  public CheckResult check(PermissionCheck request) {
    return check(request, CheckOptions.builder().build());
  }

  public CheckResult check(PermissionCheck request, CheckOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    if (request == null) throw new LocalValidationException("request must not be null");
    var deadline = deadlineFor(options.timeout());
    var item = checkItem(request, options.context());
    var builder =
        CheckPermissionRequest.newBuilder()
            .setResource(item.getResource())
            .setPermission(item.getPermission())
            .setSubject(item.getSubject())
            .setConsistency(request.consistency().toProto())
            .setWithTracing(options.tracing() == CheckTracing.ENABLED);
    if (item.hasContext()) builder.setContext(item.getContext());
    var response =
        withRetry(
            deadline,
            retryFor(ClientOperation.CHECK, options.retryPolicy()),
            () ->
                stub(permissionsStub, options.transport())
                    .withDeadline(deadline)
                    .checkPermission(builder.build()));
    return new CheckResult(
        checkPermissionshipFromProto(response.getPermissionship(), "check response"),
        response.getPartialCaveatInfo().getMissingRequiredContextList(),
        (response.hasCheckedAt() ? new ZedToken(response.getCheckedAt().getToken()) : null),
        response.hasDebugTrace() ? DebugInformation.fromProto(response.getDebugTrace()) : null,
        response.hasOptionalExpiresAt()
            ? Instant.ofEpochSecond(
                response.getOptionalExpiresAt().getSeconds(),
                response.getOptionalExpiresAt().getNanos())
            : null);
  }

  public List<CheckResult> checkBulk(Collection<PermissionCheck> requests) {
    return checkBulk(requests, CheckOptions.builder().build());
  }

  public List<CheckResult> checkBulk(Collection<PermissionCheck> requests, CheckOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    var deadline = deadlineFor(options.timeout());
    if (requests == null) throw new LocalValidationException("requests must not be null");
    var snapshot = new ArrayList<>(requests);
    if (snapshot.contains(null))
      throw new LocalValidationException("requests must not contain null");
    if (snapshot.isEmpty()) return List.of();
    var consistency = snapshot.get(0).consistency();
    for (var request : snapshot)
      if (!consistency.equals(request.consistency()))
        throw new LocalValidationException("bulk requests must have equal consistency");
    var results = new ArrayList<CheckResult>();
    for (int offset = 0; offset < snapshot.size(); offset += DEFAULT_CHECK_BATCH_SIZE) {
      var items =
          snapshot
              .subList(offset, Math.min(offset + DEFAULT_CHECK_BATCH_SIZE, snapshot.size()))
              .stream()
              .map(r -> checkItem(r, options.context()))
              .toList();
      var req =
          CheckBulkPermissionsRequest.newBuilder()
              .setConsistency(consistency.toProto())
              .setWithTracing(options.tracing() == CheckTracing.ENABLED)
              .addAllItems(items)
              .build();
      var response =
          withRetry(
              deadline,
              retryFor(ClientOperation.CHECK_BULK, options.retryPolicy()),
              () ->
                  stub(permissionsStub, options.transport())
                      .withDeadline(deadline)
                      .checkBulkPermissions(req));
      if (response.getPairsCount() != items.size())
        throw malformed(
            "checkBulkPermissions returned "
                + response.getPairsCount()
                + " pair(s) for "
                + items.size()
                + " request item(s)");
      for (int i = 0; i < response.getPairsCount(); i++) {
        var pair = response.getPairs(i);
        if (!pair.hasRequest() || !pair.getRequest().equals(items.get(i)))
          throw malformed("check item " + (offset + i) + ": missing or mismatched echoed request");
        if (pair.hasError()) {
          var itemError =
              ErrorMapper.toSpiceDBException(
                  perItemStatusException(pair.getError()),
                  "check item " + (offset + i) + ": " + pair.getError().getMessage());
          if (options.bulkItemErrors() == BulkItemErrors.FAIL_CALL) throw itemError;
          results.add(
              CheckResult.ofError(
                  itemError,
                  response.hasCheckedAt()
                      ? new ZedToken(response.getCheckedAt().getToken())
                      : null));
          continue;
        }
        if (!pair.hasItem())
          throw malformed(
              "check item "
                  + (offset + i)
                  + ": malformed CheckBulkPermissionsPair (neither item nor error set)");
        results.add(
            checkResultFromBulkItem(
                pair.getItem(),
                offset + i,
                response.hasCheckedAt() ? new ZedToken(response.getCheckedAt().getToken()) : null));
      }
    }
    return List.copyOf(results);
  }

  public boolean checkAny(Collection<PermissionCheck> requests) {
    return checkAny(requests, CheckOptions.builder().build());
  }

  public boolean checkAny(Collection<PermissionCheck> requests, CheckOptions options) {
    return checkBulk(requests, options).stream().anyMatch(CheckResult::hasPermission);
  }

  public boolean checkAll(Collection<PermissionCheck> requests) {
    return checkAll(requests, CheckOptions.builder().build());
  }

  public boolean checkAll(Collection<PermissionCheck> requests, CheckOptions options) {
    var results = checkBulk(requests, options);
    return !results.isEmpty() && results.stream().allMatch(CheckResult::hasPermission);
  }

  // -----------------------------------------------------------------------
  // Writes
  // -----------------------------------------------------------------------

  public WriteResult write(Transaction txn) {
    return write(txn, WriteOptions.builder().build());
  }

  public WriteResult write(Transaction txn, WriteOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    var reqBuilder = WriteRelationshipsRequest.newBuilder();
    if (options.metadata() != null)
      reqBuilder.setOptionalTransactionMetadata(toProtoStruct(options.metadata()));

    for (Transaction.Mutation m : List.copyOf(txn.mutations())) {
      reqBuilder.addUpdates(ProtoConv.toProto(m));
    }

    for (Transaction.Precondition p : List.copyOf(txn.preconditions())) {
      reqBuilder.addOptionalPreconditions(ProtoConv.toProto(p));
    }

    // An empty transaction is sent as is: SpiceDB accepts it, evaluates any preconditions, and
    // returns the current revision.
    var rs =
        mutationRetry(ClientOperation.WRITE, options.retryPolicy(), txn.isIdempotent(), "write");
    WriteRelationshipsResponse resp =
        retrying(
            deadline,
            () ->
                stub(permissionsStub, options.transport())
                    .withDeadline(deadline)
                    .writeRelationships(reqBuilder.build()),
            rs);
    return new WriteResult(
        resp.hasWrittenAt() ? new ZedToken(resp.getWrittenAt().getToken()) : null);
  }

  // -----------------------------------------------------------------------
  // Read Relationships — cursor-based auto-pagination (512-item pages)
  // -----------------------------------------------------------------------

  public Stream<ReadRelationship> readRelationships(Consistency consistency, Filter filter) {
    return readRelationships(consistency, filter, ReadOptions.builder().build());
  }

  /**
   * Reads relationships, paging automatically. Each item carries its after-result cursor and its
   * page's read-at revision; {@link ReadOptions.Builder#cursor} resumes after a cursor.
   *
   * <p>Every page repeats the caller's consistency and the same page size: SpiceDB hashes both into
   * the cursor and rejects a cursor sent with different arguments, while the cursor itself pins
   * later pages to the first page's revision. A total {@code limit} is enforced client-side.
   */
  public Stream<ReadRelationship> readRelationships(
      Consistency consistency, Filter filter, ReadOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    var proto = toRelationshipFilter(filter);
    if (consistency == null) throw new LocalValidationException("consistency must not be null");
    boolean single = options.paging() == Paging.SINGLE_REQUEST;
    int requestLimit = pageRequestLimit(single, options.pageSize(), options.limit());
    return pagedStream(
        cursor -> {
          var b =
              ReadRelationshipsRequest.newBuilder()
                  .setConsistency(consistency.toProto())
                  .setRelationshipFilter(proto)
                  .setOptionalLimit(requestLimit);
          if (cursor != null) b.setOptionalCursor(cursor);
          return stub(permissionsStub, options.transport()).readRelationships(b.build());
        },
        ReadRelationshipsResponse::getAfterResultCursor,
        ProtoConv::fromProto,
        "read",
        options.cursor(),
        response -> true,
        requestLimit,
        single,
        options.limit(),
        options.timeout(),
        retryFor(ClientOperation.READ_RELATIONSHIPS, options.retryPolicy()));
  }

  public record DeleteResult(
      ZedToken revision, long deletedCount, boolean completed, String resumeCursor) {}

  /**
   * Deletes every relationship matching {@code filter} in one all-or-nothing request with the
   * server's default limit, exactly what a raw {@code DeleteRelationships} request carrying only a
   * filter does: if more relationships match than the server's limit, nothing is deleted and the
   * call fails with {@code INVALID_ARGUMENT}. Same as {@link #deleteRelationships(Filter,
   * DeleteOptions)} with {@code PartialDeletion.DISALLOWED}. To delete any number of matches in
   * batches, use {@link #deleteAllRelationshipsNonAtomic(Filter)}.
   */
  public DeleteResult deleteRelationships(Filter filter) {
    return deleteRelationships(filter, ALL_OR_NOTHING_DELETE);
  }

  private static final DeleteOptions ALL_OR_NOTHING_DELETE =
      DeleteOptions.builder().partialDeletion(PartialDeletion.DISALLOWED).build();

  public DeleteResult deleteRelationships(Filter filter, int limit) {
    return deleteRelationships(filter, limit, DeleteOptions.builder().build());
  }

  public DeleteResult deleteRelationships(Filter filter, int limit, DeleteOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    mutationRetry(
        ClientOperation.DELETE_RELATIONSHIPS, options.retryPolicy(), false, "delete relationships");
    if (limit <= 0) throw new LocalValidationException("limit must be positive");
    return deleteBounded(filter, limit, options);
  }

  /**
   * All-or-nothing delete with the server's default limit. Requires {@code
   * options.partialDeletion() == PartialDeletion.DISALLOWED}: the server rejects a delete matching
   * more than its limit instead of deleting part of it. In the default partial mode a positive
   * limit is required, so use {@link #deleteRelationships(Filter, int, DeleteOptions)}.
   */
  public DeleteResult deleteRelationships(Filter filter, DeleteOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    mutationRetry(
        ClientOperation.DELETE_RELATIONSHIPS, options.retryPolicy(), false, "delete relationships");
    if (options.partialDeletion() != PartialDeletion.DISALLOWED)
      throw new LocalValidationException(
          "deleteRelationships without a limit requires PartialDeletion.DISALLOWED");
    return deleteBounded(filter, 0, options);
  }

  private DeleteResult deleteBounded(Filter filter, int limit, DeleteOptions options) {
    var deadline = deadlineFor(options.timeout());
    try {
      return deletePage(filter, limit, options, deadline);
    } catch (SpiceDBException e) {
      if (e.deleteProgress() == null)
        e.attachDeleteProgress(new DeleteProgress(0, null, null, 0, true));
      throw e;
    } catch (java.util.concurrent.CancellationException e) {
      throw new DeleteCancellationException(e, new DeleteProgress(0, null, null, 0, true));
    }
  }

  private DeleteResult deletePage(
      Filter filter, int limit, DeleteOptions options, io.grpc.Deadline deadline) {
    var builder =
        DeleteRelationshipsRequest.newBuilder()
            .setRelationshipFilter(toRelationshipFilter(filter))
            .setOptionalAllowPartialDeletions(options.partialDeletion() == PartialDeletion.ALLOWED);
    if (limit > 0) builder.setOptionalLimit(limit);
    for (var f : options.mustMatch())
      builder.addOptionalPreconditions(
          toPrecondition(
              new Transaction.Precondition(Transaction.PreconditionOperation.MUST_MATCH, f)));
    for (var f : options.mustNotMatch())
      builder.addOptionalPreconditions(
          toPrecondition(
              new Transaction.Precondition(Transaction.PreconditionOperation.MUST_NOT_MATCH, f)));
    if (options.resumeCursor() != null && !options.resumeCursor().isEmpty())
      builder.setOptionalCursor(Cursor.newBuilder().setToken(options.resumeCursor()));
    var response =
        callOnce(
            () ->
                stub(permissionsStub, options.transport())
                    .withDeadline(deadline)
                    .deleteRelationships(builder.build()));
    var token = (response.hasDeletedAt() ? new ZedToken(response.getDeletedAt().getToken()) : null);
    var cursor =
        response.hasAfterResultCursor() ? response.getAfterResultCursor().getToken() : null;
    boolean completed =
        switch (response.getDeletionProgress()) {
          case DELETION_PROGRESS_COMPLETE -> true;
          case DELETION_PROGRESS_PARTIAL -> false;
          default -> {
            var error = malformed("invalid deletion progress in server response");
            error.attachDeleteProgress(
                new DeleteProgress(
                    response.getRelationshipsDeletedCount(), token, cursor, 1, false));
            throw error;
          }
        };
    return new DeleteResult(token, response.getRelationshipsDeletedCount(), completed, cursor);
  }

  /**
   * Deletes matches in transactional batches. Completed batches remain committed on later failure
   * or cancellation; the overall operation is non-atomic.
   */
  public DeleteResult deleteAllRelationshipsNonAtomic(Filter filter) {
    return deleteAllRelationshipsNonAtomic(filter, DeleteAllOptions.builder().build());
  }

  /**
   * Deletes matches in transactional batches. Completed batches remain committed on later failure
   * or cancellation; the overall operation is non-atomic.
   */
  public DeleteResult deleteAllRelationshipsNonAtomic(Filter filter, DeleteAllOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    mutationRetry(
        ClientOperation.DELETE_ALL_RELATIONSHIPS_NON_ATOMIC,
        options.retryPolicy(),
        false,
        "delete relationships");
    var deadline = deadlineFor(options.timeout());
    long count = 0;
    int pages = 0;
    ZedToken token = null;
    String cursor = options.resumeCursor();
    while (true) {
      try {
        var result =
            deletePage(
                filter,
                options.pageSize(),
                DeleteOptions.builder()
                    .mustMatch(options.mustMatch())
                    .mustNotMatch(options.mustNotMatch())
                    .resumeCursor(cursor)
                    .transport(options.transport())
                    .build(),
                deadline);
        var sent = cursor;
        count += result.deletedCount();
        pages++;
        token = result.revision();
        cursor = result.resumeCursor();
        if (options.onProgress() != null)
          options.onProgress().accept(new DeleteResult(token, count, result.completed(), cursor));
        if (result.completed()) return new DeleteResult(token, count, true, cursor);
        // A partial page that returns the cursor it was sent would repeat the same request
        // forever. (A partial page without a cursor is valid: the next page re-evaluates the
        // filter against what remains.)
        if (cursor != null && !cursor.isEmpty() && cursor.equals(sent)) {
          var error = malformed("delete page did not advance cursor");
          error.attachDeleteProgress(new DeleteProgress(0, token, cursor, 0, false));
          throw error;
        }
      } catch (SpiceDBException error) {
        var prior = error.deleteProgress();
        error.attachDeleteProgress(
            prior == null
                ? new DeleteProgress(count, token, pages == 0 ? null : cursor, pages, true)
                : new DeleteProgress(
                    count + prior.acknowledgedCount(),
                    prior.lastToken(),
                    prior.lastCursor(),
                    pages + prior.completedPages(),
                    prior.outcomeUnknown()));
        throw error;
      } catch (java.util.concurrent.CancellationException error) {
        throw new DeleteCancellationException(
            error, new DeleteProgress(count, token, pages == 0 ? null : cursor, pages, true));
      }
    }
  }

  // -----------------------------------------------------------------------
  // Lookups — cursor-based auto-pagination (512-item pages)
  // -----------------------------------------------------------------------

  public Stream<LookupResult.LookupResource> lookupResources(
      Consistency consistency, String resourceType, String permission, SubjectRef subject) {
    return lookupResources(
        consistency, resourceType, permission, subject, LookupOptions.builder().build());
  }

  public Stream<LookupResult.LookupResource> lookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      SubjectRef subject,
      LookupOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    if (consistency == null) throw new LocalValidationException("consistency must not be null");
    if (options.subjectRelation() != null)
      throw new LocalValidationException(
          "lookupResources uses the required SubjectRef relation, not subjectRelation options");
    var protoSubject = subjectProto(subject);
    boolean single = options.paging() == Paging.SINGLE_REQUEST;
    int requestLimit = pageRequestLimit(single, options.pageSize(), options.limit());
    return pagedStream(
        cursor -> {
          var b =
              LookupResourcesRequest.newBuilder()
                  .setConsistency(consistency.toProto())
                  .setResourceObjectType(resourceType)
                  .setPermission(permission)
                  .setSubject(protoSubject)
                  .setOptionalLimit(requestLimit)
                  .setWithDebug(options.debug() == LookupDebug.ENABLED);
          if (cursor != null) b.setOptionalCursor(cursor);
          if (options.context() != null) b.setContext(toProtoStruct(options.context()));
          return stub(permissionsStub, options.transport()).lookupResources(b.build());
        },
        LookupResourcesResponse::getAfterResultCursor,
        ProtoConv::fromProto,
        "lookup",
        options.cursor(),
        response -> {
          // Checked before the filter, so an unset permissionship fails the read rather than being
          // skipped as "not HAS_PERMISSION".
          if (response.getPermissionship()
              == LookupPermissionship.LOOKUP_PERMISSIONSHIP_UNSPECIFIED)
            throw malformed("looked-up resource: permissionship is unset");
          return options.permissionshipFilter() == LookupPermissionshipFilter.ALL
              || response.getPermissionship()
                  == LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION;
        },
        requestLimit,
        single,
        options.limit(),
        options.timeout(),
        retryFor(ClientOperation.LOOKUP_RESOURCES, options.retryPolicy()));
  }

  public Stream<LookupResult.LookupSubject> lookupSubjects(
      Consistency consistency, ObjectRef resource, String permission, String subjectType) {
    return lookupSubjects(
        consistency, resource, permission, subjectType, LookupOptions.builder().build());
  }

  public Stream<LookupResult.LookupSubject> lookupSubjects(
      Consistency consistency,
      ObjectRef resource,
      String permission,
      String subjectType,
      LookupOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    if (options.permissionshipFilter() != LookupPermissionshipFilter.ALL)
      throw new LocalValidationException("lookupSubjects does not support permissionshipFilter");
    if (options.debug() == LookupDebug.ENABLED
        || options.pageSize() != 512
        || options.paging() != Paging.AUTOMATIC)
      throw new LocalValidationException(
          "lookupSubjects does not support debug, pageSize or paging");
    var builder =
        LookupSubjectsRequest.newBuilder()
            .setConsistency(consistency.toProto())
            .setResource(objectProto(resource))
            .setPermission(permission)
            .setSubjectObjectType(subjectType);
    if (options.subjectRelation() != null)
      builder.setOptionalSubjectRelation(options.subjectRelation());
    if (options.context() != null) builder.setContext(toProtoStruct(options.context()));
    return lazyStream(
        () -> stub(permissionsStub, options.transport()).lookupSubjects(builder.build()),
        SpiceDBClient::lookupSubjectFromProto,
        "lookup subjects",
        options.timeout(),
        options.limit(),
        retryFor(ClientOperation.LOOKUP_SUBJECTS, options.retryPolicy()));
  }

  // -----------------------------------------------------------------------
  // Schema
  // -----------------------------------------------------------------------

  public record SchemaResult(String schema, ZedToken revision) {}

  public SchemaResult readSchema() {
    return readSchema(CallOptions.builder().build());
  }

  public SchemaResult readSchema(CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    ReadSchemaResponse resp =
        withRetry(
            deadline,
            retryFor(ClientOperation.READ_SCHEMA, options.retryPolicy()),
            () ->
                stub(schemaStub, options.transport())
                    .withDeadline(deadline)
                    .readSchema(ReadSchemaRequest.getDefaultInstance()));
    return new SchemaResult(
        resp.getSchemaText(),
        (resp.hasReadAt() ? new ZedToken(resp.getReadAt().getToken()) : null));
  }

  public ZedToken writeSchema(String schema) {
    return writeSchema(schema, CallOptions.builder().build());
  }

  public ZedToken writeSchema(String schema, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    var rs =
        mutationRetry(ClientOperation.WRITE_SCHEMA, options.retryPolicy(), true, "write schema");
    WriteSchemaResponse resp =
        retrying(
            deadline,
            () ->
                stub(schemaStub, options.transport())
                    .withDeadline(deadline)
                    .writeSchema(WriteSchemaRequest.newBuilder().setSchema(schema).build()),
            rs);
    return (resp.hasWrittenAt() ? new ZedToken(resp.getWrittenAt().getToken()) : null);
  }

  public record SchemaDefinition(
      String name,
      String comment,
      List<SchemaRelation> relations,
      List<SchemaPermission> permissions) {
    public SchemaDefinition {
      relations = List.copyOf(relations);
      permissions = List.copyOf(permissions);
    }
  }

  public record SchemaRelation(String name, String comment, String parentDefinitionName) {}

  public record SchemaPermission(String name, String comment, String parentDefinitionName) {}

  public record SchemaCaveat(
      String name, String comment, String expression, List<SchemaCaveatParameter> parameters) {
    public SchemaCaveat {
      parameters = List.copyOf(parameters);
    }
  }

  public record SchemaCaveatParameter(String name, String type, String parentCaveatName) {}

  public record ReflectSchemaResult(
      List<SchemaDefinition> definitions, List<SchemaCaveat> caveats, ZedToken revision) {
    public ReflectSchemaResult {
      definitions = List.copyOf(definitions);
      caveats = List.copyOf(caveats);
    }
  }

  public ReflectSchemaResult reflectSchema(Consistency consistency) {
    return reflectSchema(consistency, CallOptions.builder().build());
  }

  public ReflectSchemaResult reflectSchema(Consistency consistency, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    ReflectSchemaResponse resp =
        withRetry(
            deadline,
            retryFor(ClientOperation.REFLECT_SCHEMA, options.retryPolicy()),
            () ->
                stub(schemaStub, options.transport())
                    .withDeadline(deadline)
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
        List.copyOf(definitions),
        List.copyOf(caveats),
        (resp.hasReadAt() ? new ZedToken(resp.getReadAt().getToken()) : null));
  }

  public record RelationReference(
      String definitionName, String relationName, boolean isPermission) {}

  public record ComputablePermissionsResult(
      List<RelationReference> permissions, ZedToken revision) {
    public ComputablePermissionsResult {
      permissions = List.copyOf(permissions);
    }
  }

  public ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName) {
    return computablePermissions(
        consistency, definitionName, relationName, CallOptions.builder().build());
  }

  public ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    ComputablePermissionsResponse resp =
        withRetry(
            deadline,
            retryFor(ClientOperation.COMPUTABLE_PERMISSIONS, options.retryPolicy()),
            () ->
                stub(schemaStub, options.transport())
                    .withDeadline(deadline)
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
    return new ComputablePermissionsResult(
        List.copyOf(refs), (resp.hasReadAt() ? new ZedToken(resp.getReadAt().getToken()) : null));
  }

  public record DependentRelationsResult(List<RelationReference> relations, ZedToken revision) {
    public DependentRelationsResult {
      relations = List.copyOf(relations);
    }
  }

  public DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName) {
    return dependentRelations(
        consistency, definitionName, permissionName, CallOptions.builder().build());
  }

  public DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    DependentRelationsResponse resp =
        withRetry(
            deadline,
            retryFor(ClientOperation.DEPENDENT_RELATIONS, options.retryPolicy()),
            () ->
                stub(schemaStub, options.transport())
                    .withDeadline(deadline)
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
    return new DependentRelationsResult(
        List.copyOf(refs), (resp.hasReadAt() ? new ZedToken(resp.getReadAt().getToken()) : null));
  }

  public record SchemaDiff(
      String kind,
      String definitionName,
      String relationName,
      String permissionName,
      String caveatName) {}

  public record DiffSchemaResult(List<SchemaDiff> diffs, ZedToken revision) {
    public DiffSchemaResult {
      diffs = List.copyOf(diffs);
    }
  }

  public DiffSchemaResult diffSchema(Consistency consistency, String comparisonSchema) {
    return diffSchema(consistency, comparisonSchema, CallOptions.builder().build());
  }

  public DiffSchemaResult diffSchema(
      Consistency consistency, String comparisonSchema, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    DiffSchemaResponse resp =
        withRetry(
            deadline,
            retryFor(ClientOperation.DIFF_SCHEMA, options.retryPolicy()),
            () ->
                stub(schemaStub, options.transport())
                    .withDeadline(deadline)
                    .diffSchema(
                        DiffSchemaRequest.newBuilder()
                            .setConsistency(consistency.toProto())
                            .setComparisonSchema(comparisonSchema)
                            .build()));

    var diffs = new ArrayList<SchemaDiff>();
    for (var d : resp.getDiffsList()) {
      diffs.add(schemaDiffFromProto(d));
    }
    return new DiffSchemaResult(
        List.copyOf(diffs), (resp.hasReadAt() ? new ZedToken(resp.getReadAt().getToken()) : null));
  }

  // -----------------------------------------------------------------------
  // Expand
  // -----------------------------------------------------------------------

  public record ExpandResult(PermissionTree tree, ZedToken revision) {}

  public ExpandResult expandPermissionTree(
      Consistency consistency, ObjectRef resource, String permission) {
    return expandPermissionTree(consistency, resource, permission, CallOptions.builder().build());
  }

  public ExpandResult expandPermissionTree(
      Consistency consistency, ObjectRef resource, String permission, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    ExpandPermissionTreeResponse resp =
        withRetry(
            deadline,
            retryFor(ClientOperation.EXPAND_PERMISSION_TREE, options.retryPolicy()),
            () ->
                stub(permissionsStub, options.transport())
                    .withDeadline(deadline)
                    .expandPermissionTree(
                        ExpandPermissionTreeRequest.newBuilder()
                            .setConsistency(consistency.toProto())
                            .setResource(objectProto(resource))
                            .setPermission(permission)
                            .build()));
    if (!resp.hasTreeRoot()) throw malformed("expand response has no tree");
    return new ExpandResult(
        toPermissionTree(resp.getTreeRoot()),
        (resp.hasExpandedAt() ? new ZedToken(resp.getExpandedAt().getToken()) : null));
  }

  // -----------------------------------------------------------------------
  // Bulk Import / Export
  // -----------------------------------------------------------------------

  public long importRelationships(Iterable<Relationship> relationships) {
    return importRelationships(relationships, ImportOptions.builder().build());
  }

  public long importRelationships(Iterable<Relationship> relationships, ImportOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    mutationRetry(
        ClientOperation.IMPORT_RELATIONSHIPS, options.retryPolicy(), false, "import relationships");
    var scope = new StreamScope(options.timeout());
    var ctx = scope.context();
    var previous = ctx.attach();
    var lock = new Object();
    var done = new java.util.concurrent.atomic.AtomicBoolean();
    var failure = new java.util.concurrent.atomic.AtomicReference<Throwable>();
    var loaded = new java.util.concurrent.atomic.AtomicLong();
    var call =
        new java.util.concurrent.atomic.AtomicReference<
            io.grpc.stub.ClientCallStreamObserver<ImportBulkRelationshipsRequest>>();
    Iterator<Relationship> input = null;
    try {
      stub(permissionsAsyncStub, options.transport())
          .importBulkRelationships(
              new io.grpc.stub.ClientResponseObserver<
                  ImportBulkRelationshipsRequest, ImportBulkRelationshipsResponse>() {
                public void beforeStart(
                    io.grpc.stub.ClientCallStreamObserver<ImportBulkRelationshipsRequest> stream) {
                  call.set(stream);
                  stream.setOnReadyHandler(
                      () -> {
                        synchronized (lock) {
                          lock.notifyAll();
                        }
                      });
                }

                public void onNext(ImportBulkRelationshipsResponse response) {
                  loaded.set(response.getNumLoaded());
                }

                public void onError(Throwable error) {
                  failure.set(error);
                  done.set(true);
                  synchronized (lock) {
                    lock.notifyAll();
                  }
                }

                public void onCompleted() {
                  done.set(true);
                  synchronized (lock) {
                    lock.notifyAll();
                  }
                }
              });
      input = relationships.iterator();
      var stream = call.get();
      while (true) {
        synchronized (lock) {
          while (!stream.isReady() && !done.get() && !ctx.isCancelled()) lock.wait(50);
        }
        if (ctx.isCancelled()) {
          if (scope.ended || ctx.getDeadline() == null || !ctx.getDeadline().isExpired())
            throw cancelled("import cancelled");
          throw Status.DEADLINE_EXCEEDED.asRuntimeException();
        }
        if (done.get()) {
          if (failure.get() != null) throw importFailure(failure.get());
          break;
        }
        var batch = new ArrayList<com.authzed.api.v1.Relationship>(options.batchSize());
        while (batch.size() < options.batchSize()
            && !done.get()
            && !ctx.isCancelled()
            && input.hasNext()) {
          if (Thread.currentThread().isInterrupted()) throw new InterruptedException();
          batch.add(toProtoRelationship(input.next()));
        }
        if (!batch.isEmpty())
          stream.onNext(
              ImportBulkRelationshipsRequest.newBuilder().addAllRelationships(batch).build());
        if (batch.size() < options.batchSize()) {
          stream.onCompleted();
          break;
        }
      }
      synchronized (lock) {
        while (!done.get() && !ctx.isCancelled()) lock.wait(50);
      }
      if (failure.get() != null) throw importFailure(failure.get());
      if (ctx.isCancelled()) {
        if (scope.ended) throw cancelled("import cancelled");
        throw Status.DEADLINE_EXCEEDED.asRuntimeException();
      }
      return loaded.get();
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      failure.compareAndSet(null, e);
      throw cancelled("import interrupted");
    } catch (StatusRuntimeException e) {
      failure.compareAndSet(null, e);
      if (Thread.currentThread().isInterrupted() || scope.ended)
        throw cancelled("import cancelled");
      throw ErrorMapper.toSpiceDBException(e);
    } catch (RuntimeException e) {
      failure.compareAndSet(null, e);
      throw e;
    } finally {
      scope.close();
      ctx.detach(previous);
      if (input instanceof AutoCloseable closeable)
        try {
          closeable.close();
        } catch (Exception e) {
          if (failure.get() == null) throw new SpiceDBException("closing import source failed", e);
          failure.get().addSuppressed(e);
        }
    }
  }

  private static RuntimeException importFailure(Throwable error) {
    if (error instanceof StatusRuntimeException status)
      return ErrorMapper.toSpiceDBException(status);
    if (error instanceof io.grpc.StatusException status)
      return ErrorMapper.toSpiceDBException(
          status.getStatus().withCause(error).asRuntimeException(status.getTrailers()));
    return new SpiceDBException("import failed", error);
  }

  public Stream<ExportedRelationship> exportRelationships(Consistency consistency, Filter filter) {
    return exportRelationships(consistency, filter, ExportOptions.builder().build());
  }

  /**
   * Exports relationships as one server stream. Each item carries its batch's after-result cursor
   * (exports have no read-at revision); {@link ExportOptions.Builder#cursor} resumes after one,
   * repeating the original filter, consistency and page size.
   */
  public Stream<ExportedRelationship> exportRelationships(
      Consistency consistency, Filter filter, ExportOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    var b =
        ExportBulkRelationshipsRequest.newBuilder()
            .setConsistency(consistency.toProto())
            .setOptionalLimit(options.pageSize());
    if (filter != null) b.setOptionalRelationshipFilter(toRelationshipFilter(filter));
    if (options.cursor() != null && !options.cursor().isEmpty())
      b.setOptionalCursor(Cursor.newBuilder().setToken(options.cursor()));
    return managedStream(
        () -> {
          // One request whose responses are the server's pages: each wait for the next one is
          // bounded by the client default, so a server that stalls mid-export fails the export.
          var pages = new PageTimer("export");
          var responses =
              pages.open(
                  () ->
                      openStreamWithRetry(
                          retryFor(ClientOperation.EXPORT_RELATIONSHIPS, options.retryPolicy()),
                          () ->
                              stub(permissionsStub, options.transport())
                                  .exportBulkRelationships(b.build())));
          return new Iterator<ExportedRelationship>() {
            Iterator<ExportedRelationship> batch = Collections.emptyIterator();

            public boolean hasNext() {
              while (!batch.hasNext()) {
                if (!pages.await(responses::hasNext)) return false;
                batch = ProtoConv.fromProto(responses.next()).iterator();
              }
              return true;
            }

            public ExportedRelationship next() {
              if (!hasNext()) throw new NoSuchElementException();
              return batch.next();
            }
          };
        },
        options.timeout(),
        options.limit());
  }

  // -----------------------------------------------------------------------
  // Watch
  // -----------------------------------------------------------------------

  public record Update(UpdateOperation operation, Relationship relationship) {}

  public enum UpdateOperation {
    UNSPECIFIED,
    CREATE,
    TOUCH,
    DELETE
  }

  /**
   * One watch response.
   *
   * @param schemaUpdated the schema changed at {@code changesThrough}; only reported when the watch
   *     requested {@link WatchSchemaChanges#INCLUDED}
   */
  public record WatchEvent(
      List<Update> updates, ZedToken changesThrough, boolean isCheckpoint, boolean schemaUpdated) {
    public WatchEvent {
      updates = updates == null ? List.of() : List.copyOf(updates);
    }

    /** An event without a schema change. */
    public WatchEvent(List<Update> updates, ZedToken changesThrough, boolean isCheckpoint) {
      this(updates, changesThrough, isCheckpoint, false);
    }
  }

  /**
   * Watches relationship changes; a null {@code startRevision} starts at head, and an empty or
   * whitespace-only one throws {@link LocalValidationException} before any request. The client-wide
   * retry policy and stream-retry setting never reopen a watch: a failure before the first event
   * (UNAVAILABLE included) is thrown, since a reopened watch without a start revision would begin
   * at a later revision and skip the changes between. Only {@link WatchOptions.Builder#retryPolicy}
   * or a {@link ClientOperation#WATCH} operation policy opts in to reopening before the first
   * event; a watch that delivered an event is never reopened.
   */
  public Stream<WatchEvent> updates(List<String> objectTypes, ZedToken startRevision) {
    return updates(objectTypes, startRevision, WatchOptions.builder().build());
  }

  /** Watches relationship changes with options; see {@link #updates(List, ZedToken)}. */
  public Stream<WatchEvent> updates(
      List<String> objectTypes, ZedToken startRevision, WatchOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    requireOptionalRevision("startRevision", startRevision);
    if (objectTypes != null && !objectTypes.isEmpty() && !options.relationshipFilters().isEmpty())
      throw new LocalValidationException(
          "objectTypes and relationshipFilters are mutually exclusive");
    var b = WatchRequest.newBuilder();
    for (var filter : options.relationshipFilters())
      b.addOptionalRelationshipFilters(toRelationshipFilter(filter));
    if (objectTypes != null) b.addAllOptionalObjectTypes(List.copyOf(objectTypes));
    if (startRevision != null)
      b.setOptionalStartCursor(
          com.authzed.api.v1.ZedToken.newBuilder().setToken(startRevision.value()));
    boolean checkpoints = options.content() == WatchContent.RELATIONSHIPS_AND_CHECKPOINTS;
    boolean schema = options.schemaChanges() == WatchSchemaChanges.INCLUDED;
    if (checkpoints || schema) {
      b.addOptionalUpdateKinds(WatchKind.WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES);
      if (checkpoints) b.addOptionalUpdateKinds(WatchKind.WATCH_KIND_INCLUDE_CHECKPOINTS);
      if (schema) b.addOptionalUpdateKinds(WatchKind.WATCH_KIND_INCLUDE_SCHEMA_UPDATES);
    }
    return lazyStream(
        () -> stub(watchStub, options.transport()).watch(b.build()),
        r ->
            new WatchEvent(
                r.getUpdatesList().stream().map(this::updateFromProto).toList(),
                (r.hasChangesThrough() ? new ZedToken(r.getChangesThrough().getToken()) : null),
                r.getIsCheckpoint(),
                r.getSchemaUpdated()),
        null,
        options.timeout(),
        0,
        watchRetry(ClientOperation.WATCH, options.retryPolicy()));
  }

  // -----------------------------------------------------------------------
  // Experimental — these APIs may change without following the backwards
  // compatibility mandate
  // -----------------------------------------------------------------------

  public void experimentalRegisterRelationshipCounter(String name, Filter filter) {
    experimentalRegisterRelationshipCounter(name, filter, CallOptions.builder().build());
  }

  public void experimentalRegisterRelationshipCounter(
      String name, Filter filter, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    mutationRetry(
        ClientOperation.REGISTER_RELATIONSHIP_COUNTER,
        options.retryPolicy(),
        false,
        "register relationship counter");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    callOnce(
        () ->
            stub(experimentalStub, options.transport())
                .withDeadline(deadline)
                .experimentalRegisterRelationshipCounter(
                    ExperimentalRegisterRelationshipCounterRequest.newBuilder()
                        .setName(name)
                        .setRelationshipFilter(toRelationshipFilter(filter))
                        .build()));
  }

  public record CountResult(long relationshipCount, ZedToken revision, boolean stillCalculating) {}

  public CountResult experimentalCountRelationships(String name) {
    return experimentalCountRelationships(name, CallOptions.builder().build());
  }

  public CountResult experimentalCountRelationships(String name, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    ExperimentalCountRelationshipsResponse resp =
        withRetry(
            deadline,
            retryFor(ClientOperation.COUNT_RELATIONSHIPS, options.retryPolicy()),
            () ->
                stub(experimentalStub, options.transport())
                    .withDeadline(deadline)
                    .experimentalCountRelationships(
                        ExperimentalCountRelationshipsRequest.newBuilder().setName(name).build()));

    if (resp.getCounterStillCalculating()) {
      return new CountResult(0, null, true);
    }

    if (!resp.hasReadCounterValue()) throw malformed("counter response has no value");
    var cv = resp.getReadCounterValue();
    return new CountResult(
        cv.getRelationshipCount(),
        (cv.hasReadAt() ? new ZedToken(cv.getReadAt().getToken()) : null),
        false);
  }

  public void experimentalUnregisterRelationshipCounter(String name) {
    experimentalUnregisterRelationshipCounter(name, CallOptions.builder().build());
  }

  public void experimentalUnregisterRelationshipCounter(String name, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    mutationRetry(
        ClientOperation.UNREGISTER_RELATIONSHIP_COUNTER,
        options.retryPolicy(),
        false,
        "unregister relationship counter");
    Duration timeout = options.timeout();
    var deadline = deadlineFor(timeout);
    callOnce(
        () ->
            stub(experimentalStub, options.transport())
                .withDeadline(deadline)
                .experimentalUnregisterRelationshipCounter(
                    ExperimentalUnregisterRelationshipCounterRequest.newBuilder()
                        .setName(name)
                        .build()));
  }

  // -----------------------------------------------------------------------
  // Escape hatch
  // -----------------------------------------------------------------------

  /** Experimental bitmap lookup; this RPC does not support context, debug or pagination. */
  public Materialize.RoaringLookupResourcesResult experimentalRoaringLookupResources(
      Consistency consistency, String resourceType, String permission, SubjectRef subject) {
    return experimentalRoaringLookupResources(
        consistency, resourceType, permission, subject, LookupOptions.builder().build());
  }

  public Materialize.RoaringLookupResourcesResult experimentalRoaringLookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      SubjectRef subject,
      LookupOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    if (options.subjectRelation() != null
        || options.context() != null
        || options.debug() == LookupDebug.ENABLED
        || options.limit() != 0
        || options.pageSize() != 512
        || options.paging() != Paging.AUTOMATIC)
      throw new LocalValidationException(
          "roaring lookup does not support context, debug, limit, pageSize or paging");
    var deadline = deadlineFor(options.timeout());
    var request =
        com.authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest.newBuilder()
            .setConsistency(consistency.toProto())
            .setResourceObjectType(resourceType)
            .setPermission(permission)
            .setSubject(subjectProto(subject))
            .build();
    var response =
        withRetry(
            deadline,
            retryFor(ClientOperation.ROARING_LOOKUP_RESOURCES, options.retryPolicy()),
            () ->
                stub(
                        com.authzed.api.materialize.v0.RoaringLookupResourcesServiceGrpc
                            .newBlockingStub(authenticatedChannel),
                        options.transport())
                    .withDeadline(deadline)
                    .experimentalRoaringLookupResources(request));
    return new Materialize.RoaringLookupResourcesResult(
        response.getBitmap().toByteArray(),
        response.getCardinality(),
        (response.hasAtRevision() ? new ZedToken(response.getAtRevision().getToken()) : null));
  }

  /** Experimental direct filter count. */
  public CountResult experimentalCountRelationshipsByFilter(Filter filter) {
    return experimentalCountRelationshipsByFilter(filter, CallOptions.builder().build());
  }

  public CountResult experimentalCountRelationshipsByFilter(Filter filter, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    var deadline = deadlineFor(options.timeout());
    var request =
        com.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest.newBuilder()
            .setRelationshipFilter(toRelationshipFilter(filter))
            .build();
    var response =
        withRetry(
            deadline,
            retryFor(ClientOperation.COUNT_RELATIONSHIPS_BY_FILTER, options.retryPolicy()),
            () ->
                stub(
                        com.authzed.api.materialize.v0.RelationshipsServiceGrpc.newBlockingStub(
                            authenticatedChannel),
                        options.transport())
                    .withDeadline(deadline)
                    .experimentalCountRelationshipsByFilter(request));
    return new CountResult(
        response.getRelationshipCount(),
        (response.hasReadAt() ? new ZedToken(response.getReadAt().getToken()) : null),
        false);
  }

  /**
   * Experimental closeable permission-set watch; null starts at head (an empty or whitespace-only
   * token throws {@link LocalValidationException}). Like {@link #updates(List, ZedToken)}, it is
   * reopened before its first event only under a per-call or {@link
   * ClientOperation#WATCH_PERMISSION_SETS} retry policy, never the client-wide one.
   */
  public Stream<Materialize.PermissionSetEvent> experimentalWatchPermissionSets(
      ZedToken startRevision) {
    return experimentalWatchPermissionSets(startRevision, CallOptions.builder().build());
  }

  public Stream<Materialize.PermissionSetEvent> experimentalWatchPermissionSets(
      ZedToken startRevision, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    requireOptionalRevision("startRevision", startRevision);
    var b = com.authzed.api.materialize.v0.WatchPermissionSetsRequest.newBuilder();
    if (startRevision != null)
      b.setOptionalStartingAfter(
          com.authzed.api.v1.ZedToken.newBuilder().setToken(startRevision.value()));
    return lazyStream(
        () ->
            stub(
                    com.authzed.api.materialize.v0.WatchPermissionSetsServiceGrpc.newBlockingStub(
                        authenticatedChannel),
                    options.transport())
                .watchPermissionSets(b.build()),
        Materialize::event,
        null,
        options.timeout(),
        0,
        watchRetry(ClientOperation.WATCH_PERMISSION_SETS, options.retryPolicy()));
  }

  /** Experimental snapshot lookup with explicit opaque continuation. */
  public Stream<Materialize.LookupPermissionSet> experimentalLookupPermissionSets(
      ZedToken atRevision) {
    return experimentalLookupPermissionSets(atRevision, PermissionSetOptions.builder().build());
  }

  public Stream<Materialize.LookupPermissionSet> experimentalLookupPermissionSets(
      ZedToken atRevision, PermissionSetOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    requireOptionalRevision("atRevision", atRevision);
    var b =
        com.authzed.api.materialize.v0.LookupPermissionSetsRequest.newBuilder()
            .setLimit(options.limit());
    if (atRevision != null)
      b.setOptionalAtRevision(
          com.authzed.api.v1.ZedToken.newBuilder().setToken(atRevision.value()));
    if (options.resumeCursor() != null)
      try {
        b.setOptionalStartingAfterCursor(
            com.authzed.api.materialize.v0.Cursor.parseFrom(
                Base64.getDecoder().decode(options.resumeCursor())));
      } catch (Exception e) {
        throw new LocalValidationException("invalid permission-set cursor", e);
      }
    return lazyStream(
        () ->
            stub(
                    com.authzed.api.materialize.v0.WatchPermissionSetsServiceGrpc.newBlockingStub(
                        authenticatedChannel),
                    options.transport())
                .lookupPermissionSets(b.build()),
        r -> {
          if (!r.hasChange()) throw malformed("permission-set lookup response has no change");
          return new Materialize.LookupPermissionSet(
              Materialize.change(r.getChange()),
              r.hasCursor()
                  ? Base64.getEncoder().encodeToString(r.getCursor().toByteArray())
                  : null);
        },
        "permission-set lookup",
        options.timeout(),
        options.limit(),
        retryFor(ClientOperation.LOOKUP_PERMISSION_SETS, options.retryPolicy()));
  }

  /**
   * Experimental file manifest download. URLs are returned without initiating network transfers.
   */
  public Materialize.DownloadPermissionSetsResult experimentalDownloadPermissionSets(
      ZedToken atRevision) {
    return experimentalDownloadPermissionSets(atRevision, CallOptions.builder().build());
  }

  public Materialize.DownloadPermissionSetsResult experimentalDownloadPermissionSets(
      ZedToken atRevision, CallOptions options) {
    if (options == null) throw new LocalValidationException("options must not be null");
    requireOptionalRevision("atRevision", atRevision);
    var deadline = deadlineFor(options.timeout());
    var b = com.authzed.api.materialize.v0.DownloadPermissionSetsRequest.newBuilder();
    if (atRevision != null)
      b.setOptionalAtRevision(
          com.authzed.api.v1.ZedToken.newBuilder().setToken(atRevision.value()));
    var r =
        withRetry(
            deadline,
            retryFor(ClientOperation.DOWNLOAD_PERMISSION_SETS, options.retryPolicy()),
            () ->
                stub(
                        com.authzed.api.materialize.v0.WatchPermissionSetsServiceGrpc
                            .newBlockingStub(authenticatedChannel),
                        options.transport())
                    .withDeadline(deadline)
                    .downloadPermissionSets(b.build()));
    return new Materialize.DownloadPermissionSetsResult(
        r.getFilesList().stream()
            .map(file -> new Materialize.DownloadFile(file.getName(), file.getUrl()))
            .toList(),
        r.hasTimestamp()
            ? Instant.ofEpochSecond(r.getTimestamp().getSeconds(), r.getTimestamp().getNanos())
            : null,
        r.hasAtRevision() ? new ZedToken(r.getAtRevision().getToken()) : null);
  }

  // -----------------------------------------------------------------------
  // Startup readiness
  // -----------------------------------------------------------------------

  /**
   * Connects now and waits until the connection is ready, so a wrong or unreachable endpoint fails
   * at startup rather than on the first call. The client otherwise connects lazily, on its first
   * call. Throws {@link com.authzed.spicedb.errors.UnavailableException} when the connection failed
   * within {@code timeout} (unreachable or refusing connections) and {@link
   * com.authzed.spicedb.errors.DeadlineExceededException} when it was still connecting; both name
   * the endpoint. Over a caller-owned {@link Channel} that is not a {@link ManagedChannel}, whose
   * connectivity cannot be observed, it waits for a schema read to reach the server instead: any
   * answer from the server, an error included, counts as ready.
   *
   * @param timeout how long to wait; must be positive
   */
  public void awaitReady(Duration timeout) {
    if (timeout == null || timeout.isZero() || timeout.isNegative())
      throw new LocalValidationException("timeout must be positive");
    if (closed) throw cancelled("client closed");
    long until = System.nanoTime() + timeout.toNanos();
    var managed = readinessChannel;
    if (managed == null) {
      probeReady(timeout);
      return;
    }
    boolean failed = false;
    var state = managed.getState(true);
    while (state != io.grpc.ConnectivityState.READY) {
      if (state == io.grpc.ConnectivityState.TRANSIENT_FAILURE) failed = true;
      if (state == io.grpc.ConnectivityState.SHUTDOWN)
        throw ErrorMapper.newError(
            Status.Code.UNAVAILABLE,
            "spicedb: the connection to \"" + endpointLabel + "\" is closed");
      long remaining = until - System.nanoTime();
      if (remaining <= 0) throw notReady(timeout, failed);
      var changed = new java.util.concurrent.CountDownLatch(1);
      managed.notifyWhenStateChanged(state, changed::countDown);
      try {
        changed.await(remaining, TimeUnit.NANOSECONDS);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        throw cancelled("interrupted while waiting for the connection");
      }
      state = managed.getState(true);
    }
  }

  private SpiceDBException notReady(Duration timeout, boolean failed) {
    if (failed)
      return ErrorMapper.newError(
          Status.Code.UNAVAILABLE,
          "spicedb: could not connect to \""
              + endpointLabel
              + "\" within "
              + timeout
              + " (unreachable or refusing connections)");
    return ErrorMapper.newError(
        Status.Code.DEADLINE_EXCEEDED,
        "spicedb: the connection to \"" + endpointLabel + "\" was not ready within " + timeout);
  }

  /** Readiness over an unobservable channel: a wait-for-ready schema read reaches the server. */
  private void probeReady(Duration timeout) {
    try {
      schemaStub
          .withWaitForReady()
          .withDeadline(io.grpc.Deadline.after(timeout.toNanos(), TimeUnit.NANOSECONDS))
          .readSchema(ReadSchemaRequest.getDefaultInstance());
    } catch (StatusRuntimeException e) {
      if (Thread.currentThread().isInterrupted())
        throw cancelled("interrupted while waiting for the connection");
      var code = e.getStatus().getCode();
      if (code == Status.Code.DEADLINE_EXCEEDED) throw notReady(timeout, false);
      if (code == Status.Code.UNAVAILABLE) throw ErrorMapper.toSpiceDBException(e);
      // Any other answer came from the server, which is therefore reachable.
    }
  }

  /** Already-authenticated transport escape hatch. Closing it is not ownership transfer. */
  public Channel rawChannel() {
    return authenticatedChannel;
  }

  // -----------------------------------------------------------------------
  // AutoCloseable
  // -----------------------------------------------------------------------

  @Override
  public void close() {
    closed = true;
    for (var scope : List.copyOf(activeStreams)) scope.close();
    // A channel supplied to fromChannel stays the caller's: cancel this client's streams only.
    if (channel == null) return;
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
   * The operation budget: the operation's own timeout, else the client default unless {@link
   * DefaultDeadline#DISABLED}, in which case {@code null} (unbounded apart from the caller's gRPC
   * {@code Context} deadline).
   */
  private io.grpc.Deadline deadlineFor(Duration timeout) {
    if (timeout != null) return io.grpc.Deadline.after(timeout.toNanos(), TimeUnit.NANOSECONDS);
    if (defaultDeadline == DefaultDeadline.DISABLED) return null;
    return io.grpc.Deadline.after(defaultTimeout.toNanos(), TimeUnit.NANOSECONDS);
  }

  /**
   * The per-page budget of a finite read served as one server stream (an export, whose responses
   * are the server's pages; a subject or permission-set lookup; a single-request read or lookup):
   * each wait for the server's next message, opening included, is bounded by the client default
   * unless {@link DefaultDeadline#DISABLED}. Time the caller spends between items never counts.
   * Expiry cancels the call with {@code DEADLINE_EXCEEDED}. The read's total budget, inherited from
   * the stream's context, still applies.
   */
  private final class PageTimer {
    final String what;
    final Duration timeout = defaultDeadline == DefaultDeadline.DISABLED ? null : defaultTimeout;
    final Context.CancellableContext context = Context.current().withCancellation();

    PageTimer(String what) {
      this.what = what;
    }

    /** Opens the call inside this timer's context, so expiry cancels it. */
    <T> T open(java.util.function.Supplier<T> open) {
      var previous = context.attach();
      try {
        return await(open);
      } finally {
        context.detach(previous);
      }
    }

    <T> T await(java.util.function.Supplier<T> wait) {
      if (timeout == null) return wait.get();
      var expiry =
          DEADLINES.schedule(
              () ->
                  context.cancel(
                      Status.DEADLINE_EXCEEDED
                          .withDescription(
                              what
                                  + ": no page from the server within the client default deadline"
                                  + " ("
                                  + timeout
                                  + ")")
                          .asRuntimeException()),
              timeout.toNanos(),
              TimeUnit.NANOSECONDS);
      try {
        return wait.get();
      } finally {
        expiry.cancel(false);
      }
    }
  }

  private static Metadata bearerMetadata(String presharedKey) {
    if (presharedKey == null) throw new LocalValidationException("presharedKey must not be null");
    // A blank key (usually an unset environment variable) would otherwise be sent as a bare
    // "Bearer" header the server rejects on the first call.
    if (presharedKey.isBlank())
      throw new LocalValidationException(
          "presharedKey must not be empty or whitespace (check that the variable holding it is"
              + " set)");
    Metadata metadata = new Metadata();
    metadata.put(
        Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER),
        "Bearer " + presharedKey);
    return metadata;
  }

  /**
   * An optional caller-supplied revision is null (omitted) or a usable token: an empty or
   * whitespace-only one is a local validation error before any request, never sent to the server.
   */
  private static void requireOptionalRevision(String input, ZedToken revision) {
    if (revision != null && revision.value().isBlank())
      throw new LocalValidationException(
          input + " must not be empty or whitespace-only; pass null to omit it");
  }

  /**
   * Reads and parses {@code path} as PEM X.509 certificates, throwing {@link
   * LocalValidationException} naming {@code input} when it cannot be read or holds none.
   */
  private static java.security.cert.X509Certificate[] requireCertificates(
      String input, java.nio.file.Path path) {
    byte[] bytes;
    try {
      bytes = java.nio.file.Files.readAllBytes(path);
    } catch (java.io.IOException e) {
      throw new LocalValidationException(input + " " + path + " cannot be read: " + e, e);
    }
    String text = new String(bytes, java.nio.charset.StandardCharsets.US_ASCII);
    if (!text.contains("-----BEGIN CERTIFICATE-----"))
      throw new LocalValidationException(
          input
              + " "
              + path
              + " is not a PEM-encoded X.509 certificate (no PEM CERTIFICATE block)");
    try {
      var certificates =
          java.security.cert.CertificateFactory.getInstance("X.509")
              .generateCertificates(new java.io.ByteArrayInputStream(bytes));
      if (certificates.isEmpty())
        throw new java.security.cert.CertificateException("no certificates found");
      return certificates.toArray(new java.security.cert.X509Certificate[0]);
    } catch (java.security.cert.CertificateException e) {
      throw new LocalValidationException(
          input + " " + path + " is not a PEM-encoded X.509 certificate (" + e.getMessage() + ")",
          e);
    }
  }

  /**
   * Throws {@link LocalValidationException} naming both inputs when the PKCS#8 private key in
   * {@code keyPath} is not the private half of {@code certificate}'s public key, which otherwise
   * surfaces only as a handshake failure on the first call. Runs after Netty has accepted the key;
   * a key form this check cannot read itself is left to Netty.
   */
  private static void requireKeyMatchesCertificate(
      java.nio.file.Path keyPath,
      java.nio.file.Path certificatePath,
      java.security.cert.X509Certificate certificate) {
    byte[] der;
    try {
      var text =
          java.nio.file.Files.readString(keyPath, java.nio.charset.StandardCharsets.US_ASCII);
      var begin = "-----BEGIN PRIVATE KEY-----";
      int start = text.indexOf(begin);
      int end = text.indexOf("-----END PRIVATE KEY-----");
      if (start < 0 || end < start) return;
      der = java.util.Base64.getMimeDecoder().decode(text.substring(start + begin.length(), end));
    } catch (java.io.IOException | IllegalArgumentException e) {
      return;
    }
    var mismatch =
        new LocalValidationException(
            "clientKey "
                + keyPath
                + " does not match clientCertificate "
                + certificatePath
                + " (the key is not the private key of the certificate's public key)");
    var publicKey = certificate.getPublicKey();
    java.security.PrivateKey privateKey = null;
    for (var algorithm : java.util.List.of(publicKey.getAlgorithm(), "RSA", "EC", "Ed25519")) {
      try {
        privateKey =
            java.security.KeyFactory.getInstance(algorithm)
                .generatePrivate(new java.security.spec.PKCS8EncodedKeySpec(der));
        break;
      } catch (java.security.GeneralSecurityException e) {
        // Try the next algorithm.
      }
    }
    if (privateKey == null) return;
    if (!privateKey.getAlgorithm().equals(publicKey.getAlgorithm())) throw mismatch;
    String signatureAlgorithm =
        switch (privateKey.getAlgorithm()) {
          case "RSA" -> "SHA256withRSA";
          case "EC" -> "SHA256withECDSA";
          case "Ed25519", "EdDSA" -> "Ed25519";
          default -> null;
        };
    if (signatureAlgorithm == null) return;
    try {
      var message = "spicedb client key check".getBytes(java.nio.charset.StandardCharsets.UTF_8);
      var signer = java.security.Signature.getInstance(signatureAlgorithm);
      signer.initSign(privateKey);
      signer.update(message);
      var signature = signer.sign();
      var verifier = java.security.Signature.getInstance(signatureAlgorithm);
      verifier.initVerify(publicKey);
      verifier.update(message);
      if (!verifier.verify(signature)) throw mismatch;
    } catch (java.security.GeneralSecurityException e) {
      // An EC key on a different curve fails here rather than verifying false.
      throw new LocalValidationException(mismatch.getMessage(), e);
    }
  }

  @FunctionalInterface
  private interface RetryableCall<T> {
    T call();
  }

  private static <T> T mapStreamErrors(java.util.function.Supplier<T> op) {
    try {
      return op.get();
    } catch (StatusRuntimeException e) {
      if (Thread.currentThread().isInterrupted()) throw cancelled("operation interrupted");
      throw ErrorMapper.toSpiceDBException(e);
    }
  }

  private static <T> Stream<T> cancelOnClose(
      Stream<T> stream, Context.CancellableContext cancelCtx) {
    return stream.onClose(
        () ->
            cancelCtx.cancel(
                Status.CANCELLED.withDescription("stream closed by caller").asRuntimeException()));
  }

  private static long jitteredBackoffMs(long cap) {
    return (long) (ThreadLocalRandom.current().nextDouble() * cap);
  }

  /**
   * The retry behavior for one operation: a per-call or per-operation policy wins over the client's
   * (including its disabled modes) and also governs stream establishment for that call.
   */
  private record RetrySettings(RetryPolicy policy, int maxRetries, boolean streams) {}

  private static final RetrySettings NO_RETRIES =
      new RetrySettings(RetryPolicy.defaults(), 0, false);

  /**
   * The most specific policy for one call of {@code operation}: the call's own, then the client's
   * per-operation policy, or {@code null} for the client-wide behavior.
   */
  private RetryPolicy retryFor(ClientOperation operation, RetryPolicy perCall) {
    return perCall != null ? perCall : operationRetryPolicies.get(operation);
  }

  private static final RetryPolicy NEVER_REOPEN = RetryPolicy.builder().maxRetries(0).build();

  /**
   * A watch's retry policy: only a per-call or per-operation policy may reopen it. The client-wide
   * policy (the default included) and the stream-retry setting never do, since a reopened watch
   * without a start revision begins at a later revision and silently skips the changes between.
   */
  private RetryPolicy watchRetry(ClientOperation operation, RetryPolicy perCall) {
    var policy = retryFor(operation, perCall);
    return policy != null ? policy : NEVER_REOPEN;
  }

  private RetrySettings retrySettings(RetryPolicy override) {
    if (override != null) return new RetrySettings(override, override.maxRetries(), true);
    return new RetrySettings(
        retryPolicy,
        retriesEnabled ? retryPolicy.maxRetries() : 0,
        retriesEnabled && streamRetriesEnabled);
  }

  /**
   * The retry settings for a mutation: none unless the caller opted in, which only idempotent
   * mutations (touch-only writes without preconditions, schema writes) may do. A per-call opt-in on
   * a non-idempotent mutation is an error; a per-operation policy applies only to the calls that
   * are idempotent (never-idempotent operations reject a retrying one when the client is built).
   */
  private RetrySettings mutationRetry(
      ClientOperation op, RetryPolicy perCall, boolean idempotent, String operation) {
    if (perCall != null) {
      if (perCall.maxRetries() == 0) return NO_RETRIES;
      if (!idempotent)
        throw new LocalValidationException(
            operation
                + " cannot be retried: only touch-only writes without preconditions and schema"
                + " writes are idempotent on replay");
      return new RetrySettings(perCall, perCall.maxRetries(), false);
    }
    var perOperation = operationRetryPolicies.get(op);
    if (perOperation != null && perOperation.maxRetries() > 0 && idempotent)
      return new RetrySettings(perOperation, perOperation.maxRetries(), false);
    return NO_RETRIES;
  }

  /** Retries an idempotent read under the call's or the client's {@link RetryPolicy}. */
  private <T> T withRetry(io.grpc.Deadline deadline, RetryPolicy perCall, RetryableCall<T> call) {
    return retrying(deadline, call, retrySettings(perCall));
  }

  private <T> T retrying(io.grpc.Deadline deadline, RetryableCall<T> call, RetrySettings rs) {
    var retryPolicy = rs.policy();
    int maxRetries = rs.maxRetries();
    long backoff = retryPolicy.initialBackoff().toMillis();
    long maxBackoff = retryPolicy.maxBackoff().toMillis();
    for (int attempt = 0; ; attempt++) {
      if (deadline != null && deadline.isExpired())
        throw ErrorMapper.toSpiceDBException(Status.DEADLINE_EXCEEDED.asRuntimeException());
      if (closed) throw cancelled("client closed");
      try {
        if (Thread.currentThread().isInterrupted()) throw cancelled("operation interrupted");
        return call.call();
      } catch (StatusRuntimeException e) {
        if (Thread.currentThread().isInterrupted()) throw cancelled("operation interrupted");
        if (attempt >= maxRetries
            || !retryPolicy.retryableCodes().contains(e.getStatus().getCode())) {
          throw ErrorMapper.toSpiceDBException(e);
        }
        try {
          long pause =
              deadline == null
                  ? jitteredBackoffMs(backoff)
                  : Math.max(
                      0,
                      Math.min(
                          jitteredBackoffMs(backoff),
                          deadline.timeRemaining(TimeUnit.MILLISECONDS)));
          if (retryPolicy.onRetry() != null)
            retryPolicy
                .onRetry()
                .accept(
                    new RetryPolicy.RetryAttempt(
                        attempt + 1, e.getStatus().getCode(), Duration.ofMillis(pause), e));
          long until = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(pause);
          while (System.nanoTime() < until) {
            if (closed
                || (Context.current().isCancelled() && (deadline == null || !deadline.isExpired())))
              throw cancelled("operation cancelled during backoff");
            Thread.sleep(
                Math.max(
                    1, Math.min(10, TimeUnit.NANOSECONDS.toMillis(until - System.nanoTime()))));
          }
        } catch (InterruptedException ie) {
          Thread.currentThread().interrupt();
          throw cancelled("interrupted during retry");
        }
        backoff = Math.min(maxBackoff, backoff * 2);
      }
    }
  }

  /**
   * A response this client cannot interpret: {@code INTERNAL}, which no retry policy retries by
   * default. Root DESIGN.md: never skip such a response or fill its fields with empty values. The
   * message says the client detected it, so it is not mistaken for a status the server sent.
   */
  static SpiceDBException malformed(String detail) {
    return ErrorMapper.newError(
        Status.Code.INTERNAL, "client detected malformed response from server: " + detail);
  }

  /**
   * This client's native cancellation: a {@link java.util.concurrent.CancellationException} whose
   * cause is a {@code CANCELLED} status, so {@code Status.fromThrowable} and {@link
   * ErrorMapper#codeOf} classify local cancellation as {@code CANCELLED}.
   */
  static java.util.concurrent.CancellationException cancelled(String message) {
    var e = new java.util.concurrent.CancellationException(message);
    e.initCause(Status.CANCELLED.withDescription(message).asRuntimeException());
    return e;
  }

  /** Applies the operation's transport options (call options, metadata capture) to a stub. */
  private static <S extends io.grpc.stub.AbstractStub<S>> S stub(S base, TransportOptions t) {
    var interceptor = t.interceptor();
    return interceptor == null ? base : base.withInterceptors(interceptor);
  }

  private <T> T callOnce(RetryableCall<T> call) {
    try {
      if (Thread.currentThread().isInterrupted()) throw cancelled("operation interrupted");
      return call.call();
    } catch (StatusRuntimeException e) {
      if (Thread.currentThread().isInterrupted()) throw cancelled("operation interrupted");
      throw ErrorMapper.toSpiceDBException(e);
    }
  }

  /**
   * Opens a server stream, retrying establishment (before any item is exposed) unless {@link
   * StreamRetries#DISABLED} or {@link Retries#DISABLED}.
   */
  private <T> Iterator<T> openStreamWithRetry(
      RetryPolicy perCall, RetryableCall<Iterator<T>> openCall) {
    var rs = retrySettings(perCall);
    return retrying(
        Context.current().getDeadline(),
        () -> {
          Iterator<T> serverStream = openCall.call();
          // Force establishment now, inside the retried unit of work, instead of leaving it to
          // whatever unretried call the caller makes next.
          serverStream.hasNext();
          return serverStream;
        },
        rs.streams() ? rs : new RetrySettings(rs.policy(), 0, false));
  }

  private final Set<StreamScope> activeStreams = java.util.concurrent.ConcurrentHashMap.newKeySet();
  private volatile boolean closed;
  private static final java.util.concurrent.ScheduledExecutorService DEADLINES =
      java.util.concurrent.Executors.newSingleThreadScheduledExecutor(
          r -> {
            var t = new Thread(r, "spicedb-stream-deadlines");
            t.setDaemon(true);
            return t;
          });

  private final class StreamScope implements AutoCloseable {
    final Duration timeout;
    final Context parent = Context.current();
    Context.CancellableContext context;
    volatile boolean ended;

    StreamScope(Duration timeout) {
      this.timeout = timeout;
      activeStreams.add(this);
      if (closed) {
        close();
        throw cancelled("client closed");
      }
    }

    synchronized Context.CancellableContext context() {
      if (ended) throw cancelled("stream closed");
      if (context == null)
        context =
            timeout == null
                ? parent.withCancellation()
                : parent.withDeadlineAfter(timeout.toNanos(), TimeUnit.NANOSECONDS, DEADLINES);
      return context;
    }

    public synchronized void close() {
      ended = true;
      if (context != null) context.cancel(null);
      activeStreams.remove(this);
    }
  }

  private <T> Stream<T> managedStream(
      java.util.function.Supplier<Iterator<T>> create, Duration timeout, long limit) {
    var scope = new StreamScope(timeout);
    var iterator =
        new Iterator<T>() {
          Iterator<T> delegate;
          long emitted;
          boolean done;

          public boolean hasNext() {
            if (done) return false;
            if (limit > 0 && emitted >= limit) {
              done = true;
              scope.close();
              return false;
            }
            var ctx = scope.context();
            var previous = ctx.attach();
            try {
              if (Thread.currentThread().isInterrupted()) throw cancelled("stream interrupted");
              if (delegate == null) delegate = create.get();
              boolean more = mapStreamErrors(delegate::hasNext);
              if (!more) {
                done = true;
                scope.close();
              }
              return more;
            } catch (RuntimeException e) {
              boolean local = scope.ended;
              scope.close();
              if (local && e instanceof com.authzed.spicedb.errors.CancelledException)
                throw cancelled("stream cancelled");
              throw e;
            } finally {
              ctx.detach(previous);
            }
          }

          public T next() {
            if (!hasNext()) throw new NoSuchElementException();
            var ctx = scope.context();
            var previous = ctx.attach();
            try {
              T value = mapStreamErrors(delegate::next);
              emitted++;
              return value;
            } catch (RuntimeException e) {
              boolean local = scope.ended;
              scope.close();
              if (local && e instanceof com.authzed.spicedb.errors.CancelledException)
                throw cancelled("stream cancelled");
              throw e;
            } finally {
              ctx.detach(previous);
            }
          }
        };
    return StreamSupport.stream(
            Spliterators.spliteratorUnknownSize(iterator, Spliterator.ORDERED), false)
        .onClose(scope::close);
  }

  /**
   * A single server stream. {@code timed} names a finite read whose waits for the server get the
   * per-page default ({@link PageTimer}); {@code null} for a long-lived stream (watch).
   */
  private <P, T> Stream<T> lazyStream(
      RetryableCall<Iterator<P>> open,
      java.util.function.Function<P, T> convert,
      String timed,
      Duration timeout,
      long limit,
      RetryPolicy perCall) {
    return managedStream(
        () -> {
          var timer = timed == null ? null : new PageTimer(timed);
          var source =
              timer == null
                  ? openStreamWithRetry(perCall, open)
                  : timer.open(() -> openStreamWithRetry(perCall, open));
          return new Iterator<T>() {
            public boolean hasNext() {
              return timer == null ? source.hasNext() : timer.await(source::hasNext);
            }

            public T next() {
              return convert.apply(source.next());
            }
          };
        },
        timeout,
        limit);
  }

  private <P, T> Stream<T> pagedStream(
      java.util.function.Function<Cursor, Iterator<P>> open,
      java.util.function.Function<P, Cursor> cursorOf,
      java.util.function.Function<P, T> convert,
      String what,
      String startCursor,
      java.util.function.Predicate<P> include,
      int pageSize,
      boolean singleRequest,
      long limit,
      Duration timeout,
      RetryPolicy perCall) {
    return managedStream(
        () ->
            new Iterator<T>() {
              P pending;
              Iterator<P> page = Collections.emptyIterator();
              Cursor cursor =
                  startCursor == null || startCursor.isEmpty()
                      ? null
                      : Cursor.newBuilder().setToken(startCursor).build();
              // The cursor sent with the open page and the last cursor that page returned: a full
              // page whose last cursor is missing or unchanged would re-request itself forever.
              String sentToken = startCursor == null ? "" : startCursor;
              String lastToken = "";
              int count;
              boolean opened, done;

              public boolean hasNext() {
                if (pending != null) return true;
                while (!done) {
                  while (page.hasNext()) {
                    var response = page.next();
                    count++;
                    var next = cursorOf.apply(response);
                    lastToken = next == null ? "" : next.getToken();
                    if (!lastToken.isEmpty()) cursor = next;
                    if (include.test(response)) {
                      pending = response;
                      return true;
                    }
                  }
                  if (opened && (singleRequest || count < pageSize)) {
                    done = true;
                    return false;
                  }
                  if (opened && (lastToken.isEmpty() || lastToken.equals(sentToken))) {
                    done = true;
                    throw malformed(what + " page did not advance cursor");
                  }
                  sentToken = cursor == null ? "" : cursor.getToken();
                  var sent = cursor;
                  page =
                      singleRequest
                          ? timedPage(what, perCall, () -> open.apply(sent))
                          : bufferedPage(perCall, () -> open.apply(sent));
                  opened = true;
                  count = 0;
                }
                return false;
              }

              public T next() {
                if (!hasNext()) throw new NoSuchElementException();
                var response = pending;
                pending = null;
                return convert.apply(response);
              }
            },
        timeout,
        limit);
  }

  /**
   * One page of an automatically paged read, received whole under the client default deadline
   * (unless {@link DefaultDeadline#DISABLED}) before any of it is yielded, so the deadline bounds
   * waiting on the server and never the time the caller spends on items. A page holds at most the
   * page size. A failure partway through yields the items received first, then the failure.
   */
  private <P> Iterator<P> bufferedPage(RetryPolicy perCall, RetryableCall<Iterator<P>> open) {
    var page =
        defaultDeadline == DefaultDeadline.DISABLED
            ? Context.current().withCancellation()
            : Context.current()
                .withDeadlineAfter(defaultTimeout.toNanos(), TimeUnit.NANOSECONDS, DEADLINES);
    var items = new ArrayList<P>();
    StatusRuntimeException failure = null;
    var previous = page.attach();
    try {
      var source = openStreamWithRetry(perCall, open);
      try {
        while (source.hasNext()) items.add(source.next());
      } catch (StatusRuntimeException e) {
        failure = e;
      }
    } finally {
      page.detach(previous);
      page.cancel(null);
    }
    if (failure == null) return items.iterator();
    var rest = items.iterator();
    var error = failure;
    return new Iterator<P>() {
      public boolean hasNext() {
        if (rest.hasNext()) return true;
        throw error;
      }

      public P next() {
        if (!hasNext()) throw new NoSuchElementException();
        return rest.next();
      }
    };
  }

  /**
   * The one request of a single-request read or lookup, whose size the caller's limit alone bounds:
   * each wait for the server gets the per-page default ({@link PageTimer}) instead of buffering it.
   */
  private <P> Iterator<P> timedPage(
      String what, RetryPolicy perCall, RetryableCall<Iterator<P>> open) {
    var timer = new PageTimer(what);
    var source = timer.open(() -> openStreamWithRetry(perCall, open));
    return new Iterator<P>() {
      public boolean hasNext() {
        return timer.await(source::hasNext);
      }

      public P next() {
        if (!hasNext()) throw new NoSuchElementException();
        return source.next();
      }
    };
  }

  /**
   * The limit every request of a paged read sends: the page size (fixed for the whole call, since
   * SpiceDB rejects a cursor sent with a different limit), or in single-request mode the total
   * limit alone (0 meaning unbounded).
   */
  private static int pageRequestLimit(boolean singleRequest, int pageSize, long limit) {
    if (singleRequest) return (int) Math.min(limit, 0xFFFFFFFFL);
    return pageSize;
  }

  private static CheckBulkPermissionsRequestItem checkItem(
      PermissionCheck r, Map<String, Object> defaults) {
    return ProtoConv.toProto(r, defaults);
  }

  /**
   * The item's status exactly as the server sent it (details intact); the display message adds the
   * item index at the call site, and {@link SpiceDBException#serverMessage()} keeps this message.
   */
  private static StatusRuntimeException perItemStatusException(com.google.rpc.Status errorStatus) {
    return StatusProto.toStatusRuntimeException(errorStatus);
  }

  private static CheckResult checkResultFromBulkItem(
      CheckBulkPermissionsResponseItem item, int index, ZedToken responseCheckedAt) {
    return new CheckResult(
        checkPermissionshipFromProto(item.getPermissionship(), "check item " + index),
        item.hasPartialCaveatInfo()
            ? item.getPartialCaveatInfo().getMissingRequiredContextList()
            : List.of(),
        responseCheckedAt,
        item.hasDebugTrace() ? DebugInformation.fromProto(item.getDebugTrace()) : null,
        null);
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

  // Conversions live in ProtoConv (the exported secondary namespace); these delegates keep the
  // client's call sites and package-private test hooks short.

  private static ObjectReference objectProto(ObjectRef object) {
    return ProtoConv.toProto(object);
  }

  private static SubjectReference subjectProto(SubjectRef subject) {
    return ProtoConv.toProto(subject);
  }

  private static com.google.protobuf.Struct toProtoStruct(Map<String, Object> context) {
    return ProtoConv.toStruct(context);
  }

  private static Precondition toPrecondition(Transaction.Precondition p) {
    return ProtoConv.toProto(p);
  }

  static com.authzed.api.v1.Relationship toProtoRelationship(Relationship r) {
    return ProtoConv.toProto(r);
  }

  static Relationship fromProtoRelationship(com.authzed.api.v1.Relationship pr) {
    return ProtoConv.fromProto(pr);
  }

  /**
   * The server always sets a check's permissionship, so an unset one is a malformed response, never
   * a result the caller has to handle. A value newer than this client (UNRECOGNIZED) is not
   * malformed: it maps to UNSPECIFIED, which never grants, so upgrading SpiceDB cannot make the
   * client fail.
   */
  private static Permissionship checkPermissionshipFromProto(
      CheckPermissionResponse.Permissionship v, String where) {
    if (v == CheckPermissionResponse.Permissionship.PERMISSIONSHIP_UNSPECIFIED)
      throw malformed(where + ": permissionship is unset");
    return ProtoConv.fromProto(v);
  }

  private static LookupResult.LookupSubject lookupSubjectFromProto(LookupSubjectsResponse resp) {
    return ProtoConv.fromProto(resp);
  }

  static PermissionTree toPermissionTree(PermissionRelationshipTree t) {
    return ProtoConv.fromProto(t);
  }

  private static RelationshipFilter toRelationshipFilter(Filter f) {
    return ProtoConv.toProto(f);
  }

  private Update updateFromProto(RelationshipUpdate pu) {
    return ProtoConv.fromProto(pu);
  }

  static Object fromProtoValue(com.google.protobuf.Value value) {
    return ProtoConv.fromValue(value);
  }
}
