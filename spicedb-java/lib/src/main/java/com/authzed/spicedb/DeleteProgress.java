package com.authzed.spicedb;

public record DeleteProgress(
    long acknowledgedCount,
    ZedToken lastToken,
    String lastCursor,
    int completedPages,
    boolean outcomeUnknown) {}
