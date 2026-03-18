package com.authzed.spicedb.proto;

import io.grpc.CallCredentials;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;

import java.util.concurrent.Executor;
import java.util.concurrent.TimeUnit;

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
        ManagedChannelBuilder<?> builder = ManagedChannelBuilder.forTarget(endpoint);
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
     * CallCredentials implementation that injects a bearer token into every RPC.
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
