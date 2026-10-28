package com.authzed.spicedb;

import com.authzed.spicedb.errors.InvalidArgumentException;
import java.nio.file.Path;
import java.time.Duration;

/** Immutable connection configuration. Certificate paths refer to PEM files. */
public final class ClientOptions {
  private final DefaultDeadline defaultDeadline;
  private final StreamRetries streamRetries;
  private final Retries retries;
  private final RetryPolicy retryPolicy;
  private final Duration defaultTimeout;
  private final RemotePlaintextCredentials remotePlaintextCredentials;
  private final Path caCertificate, clientCertificate, clientKey;

  private ClientOptions(Builder b) {
    this.defaultDeadline = b.defaultDeadline;
    if (defaultDeadline == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "defaultDeadline must not be null");
    this.streamRetries = b.streamRetries;
    if (streamRetries == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "streamRetries must not be null");
    this.retries = b.retries;
    if (retries == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("retries must not be null");
    this.retryPolicy = b.retryPolicy;
    if (retryPolicy == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("retryPolicy must not be null");
    defaultTimeout = b.defaultTimeout;
    remotePlaintextCredentials = b.remotePlaintextCredentials;
    if (remotePlaintextCredentials == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "remotePlaintextCredentials must not be null");
    caCertificate = b.caCertificate;
    clientCertificate = b.clientCertificate;
    clientKey = b.clientKey;
    if (defaultTimeout == null || defaultTimeout.isZero() || defaultTimeout.isNegative())
      throw new InvalidArgumentException("defaultTimeout must be positive");
    if ((clientCertificate == null) != (clientKey == null))
      throw new InvalidArgumentException(
          "clientCertificate and clientKey must be supplied together");
  }

  public Duration defaultTimeout() {
    return defaultTimeout;
  }

  public RemotePlaintextCredentials remotePlaintextCredentials() {
    return remotePlaintextCredentials;
  }

  public Path caCertificate() {
    return caCertificate;
  }

  public Path clientCertificate() {
    return clientCertificate;
  }

  public Path clientKey() {
    return clientKey;
  }

  public static Builder builder() {
    return new Builder();
  }

  /** Retry policy for idempotent reads (default {@link RetryPolicy#defaults()}). */
  public RetryPolicy retryPolicy() {
    return retryPolicy;
  }

  /** Whether reads are retried at all (default {@link Retries#ENABLED}). */
  public Retries retries() {
    return retries;
  }

  /** Whether stream establishment is retried (default {@link StreamRetries#ENABLED}). */
  public StreamRetries streamRetries() {
    return streamRetries;
  }

  /** Whether unary calls get the default deadline (default {@link DefaultDeadline#ENABLED}). */
  public DefaultDeadline defaultDeadline() {
    return defaultDeadline;
  }

  public static final class Builder {
    private Duration defaultTimeout = Duration.ofSeconds(30);
    private RemotePlaintextCredentials remotePlaintextCredentials =
        RemotePlaintextCredentials.REJECT;
    private Path caCertificate, clientCertificate, clientKey;

    private Builder() {}

    private DefaultDeadline defaultDeadline = DefaultDeadline.ENABLED;

    /** Whether unary calls get the default deadline (default {@link DefaultDeadline#ENABLED}). */
    public Builder defaultDeadline(DefaultDeadline value) {
      this.defaultDeadline = value;
      return this;
    }

    private StreamRetries streamRetries = StreamRetries.ENABLED;

    /** Whether stream establishment is retried (default {@link StreamRetries#ENABLED}). */
    public Builder streamRetries(StreamRetries value) {
      this.streamRetries = value;
      return this;
    }

    private Retries retries = Retries.ENABLED;

    /** Whether reads are retried at all (default {@link Retries#ENABLED}). */
    public Builder retries(Retries value) {
      this.retries = value;
      return this;
    }

    private RetryPolicy retryPolicy = RetryPolicy.defaults();

    /** Retry policy for idempotent reads (default {@link RetryPolicy#defaults()}). */
    public Builder retryPolicy(RetryPolicy value) {
      this.retryPolicy = value;
      return this;
    }

    public Builder defaultTimeout(Duration value) {
      defaultTimeout = value;
      return this;
    }

    public Builder remotePlaintextCredentials(RemotePlaintextCredentials value) {
      remotePlaintextCredentials = value;
      return this;
    }

    public Builder caCertificate(Path value) {
      caCertificate = value;
      return this;
    }

    public Builder clientCertificate(Path value) {
      clientCertificate = value;
      return this;
    }

    public Builder clientKey(Path value) {
      clientKey = value;
      return this;
    }

    public ClientOptions build() {
      return new ClientOptions(this);
    }
  }
}
