package com.authzed.spicedb;

/**
 * Whether a watch also requests schema-change events, reported by {@link
 * SpiceDBClient.WatchEvent#schemaUpdated()}. Combinable with checkpoints.
 */
public enum WatchSchemaChanges {
  EXCLUDED,
  INCLUDED
}
