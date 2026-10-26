package com.authzed.spicedb;

/** Result of a relationship write. Revision is null when the server omits written_at. */
public record WriteResult(ZedToken revision) {}
