namespace SpiceDB.Client;

/// <summary>Typed access to progress reported by failed deletion operations.</summary>
public static class SpiceDBExceptionExtensions
{
    /// <summary>Returns acknowledged deletion progress, if present, without changing the exception.</summary>
    public static DeleteProgress? GetDeleteProgress(this Exception error)
    {
        ArgumentNullException.ThrowIfNull(error);
        return error is SpiceDBException typed ? typed.DeleteProgress
            : error is OperationCanceledException ? error.Data["SpiceDB.DeleteProgress"] as DeleteProgress
            : null;
    }
}
