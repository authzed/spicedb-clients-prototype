using System.Text.Json.Serialization;
namespace SpiceDB.Client;

[JsonConverter(typeof(JsonStringEnumConverter<CheckTracing>))]
public enum CheckTracing { Disabled, Enabled }
[JsonConverter(typeof(JsonStringEnumConverter<LookupDebug>))]
public enum LookupDebug { Disabled, Enabled }
[JsonConverter(typeof(JsonStringEnumConverter<RemotePlaintextCredentials>))]
public enum RemotePlaintextCredentials { Reject, Allow }
[Flags]
[JsonConverter(typeof(JsonStringEnumConverter<WatchContent>))]
public enum WatchContent { Relationships = 1, Checkpoints = 2, SchemaUpdates = 4 }

internal static class BehaviorOptions
{
    internal static T Defined<T>(T value, string name) where T : struct, Enum => Enum.IsDefined(value)
        ? value : throw new InvalidArgumentException($"Unknown {name} value: {value}.");

    internal static WatchContent Content(WatchContent value) => value != 0 &&
        (value & ~(WatchContent.Relationships | WatchContent.Checkpoints | WatchContent.SchemaUpdates)) == 0
        ? value : throw new InvalidArgumentException($"Unknown or empty watch content: {value}.");
}
