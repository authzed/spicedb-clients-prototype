package com.authzed.spicedb;

import com.authzed.spicedb.errors.InvalidArgumentException;
import java.nio.file.Path;
import java.time.Duration;

/** Immutable connection configuration. Certificate paths refer to PEM files. */
public final class ClientOptions {
  private final Duration defaultTimeout;
  private final RemotePlaintextCredentials remotePlaintextCredentials;
  private final Path caCertificate, clientCertificate, clientKey;

  private ClientOptions(Builder b) {
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

  public static final class Builder {
    private Duration defaultTimeout = Duration.ofSeconds(30);
    private RemotePlaintextCredentials remotePlaintextCredentials =
        RemotePlaintextCredentials.REJECT;
    private Path caCertificate, clientCertificate, clientKey;

    private Builder() {}

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
