namespace SpiceDB.Client;

public sealed record DeleteResult(ZedToken Revision, ulong DeletedCount, bool Completed, string? ResumeCursor);
public sealed record DeleteProgress(ulong AcknowledgedCount, ZedToken? LastToken, string? LastCursor, int CompletedPages, bool OutcomeUnknown);
