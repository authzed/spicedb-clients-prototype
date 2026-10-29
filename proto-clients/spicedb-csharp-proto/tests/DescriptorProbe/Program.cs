using System.Reflection;
using Google.Protobuf;
using Google.Protobuf.Reflection;

// Fresh-process descriptor probe. The first thing it does is format a message as JSON, the
// path a failing descriptor initialiser breaks; then it walks every generated file directly
// and once more through the legacy Authzed.Net type names.
try
{
    Console.WriteLine(JsonFormatter.Default.Format(new Authzed.Api.V1.CheckPermissionRequest()));
    Console.WriteLine(Authzed.Api.V1.CheckPermissionResponse.Descriptor.FullName);

    var proto = typeof(Authzed.Api.V1.Relationship).Assembly;
    var files = 0;
    var messages = 0;
    foreach (var holder in proto.GetExportedTypes().Where(t =>
                 t.Namespace is "Authzed.Api.V1" or "Authzed.Api.Materialize.V0" && t.Name.EndsWith("Reflection", StringComparison.Ordinal)))
    {
        if (holder.GetProperty("Descriptor", BindingFlags.Public | BindingFlags.Static)?.GetValue(null) is not FileDescriptor file)
        {
            continue;
        }
        files++;
        foreach (var message in file.MessageTypes.Where(m => m.ClrType is not null))
        {
            var instance = (IMessage)Activator.CreateInstance(message.ClrType)!;
            _ = instance.ToString();
            _ = JsonFormatter.Default.Format(instance);
            messages++;
        }
    }
    Console.WriteLine($"direct: ok ({files} files, {messages} messages)");

    var bridged = 0;
    var bridge = Assembly.Load("Authzed.Net");
    foreach (var type in bridge.GetForwardedTypes().Where(t => typeof(IMessage).IsAssignableFrom(t) && !t.IsAbstract))
    {
        var legacy = Type.GetType($"{type.FullName}, Authzed.Net", throwOnError: true)!;
        var instance = (IMessage)Activator.CreateInstance(legacy)!;
        _ = instance.Descriptor;
        _ = instance.ToString();
        _ = JsonFormatter.Default.Format(instance);
        bridged++;
    }
    Console.WriteLine($"bridge: ok ({bridged} messages)");
    return 0;
}
catch (Exception e)
{
    Console.Error.WriteLine(e);
    return 1;
}
