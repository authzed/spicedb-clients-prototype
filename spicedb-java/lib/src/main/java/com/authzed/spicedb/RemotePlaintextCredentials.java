package com.authzed.spicedb;

/**
 * Policy for sending authentication credentials to remote plaintext endpoints. ALLOW explicitly
 * permits unencrypted credential transmission.
 */
public enum RemotePlaintextCredentials {
  REJECT,
  ALLOW
}
