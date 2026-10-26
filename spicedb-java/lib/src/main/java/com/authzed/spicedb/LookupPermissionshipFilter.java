package com.authzed.spicedb;

/** Client-side filtering for LookupResources. Conditional results are not unconditional grants. */
public enum LookupPermissionshipFilter {
  ALL,
  HAS_PERMISSION_ONLY
}
