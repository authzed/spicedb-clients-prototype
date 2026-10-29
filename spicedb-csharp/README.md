# SpiceDB.Client

The idiomatic C# client for [SpiceDB](https://github.com/authzed/spicedb): native
immutable values, explicit consistency, typed exceptions, bounded deadlines and retries,
and an in-memory test double. It is built on `SpiceDB.Proto`, the generated protocol
binding in [`../proto-clients/spicedb-csharp-proto`](../proto-clients/spicedb-csharp-proto).

- [DESIGN.md](DESIGN.md): the API's rules and guarantees.
- [MIGRATION.md](MIGRATION.md): moving from `Authzed.Net`, with requirements and a
  member-by-member map.
- [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md): how to integrate the client into an
  application (written for AI coding agents, useful for anyone).
- [examples/](examples/): runnable examples that double as integration tests.

## Requirements

`net8.0`, `net9.0` or `net10.0`, and SpiceDB 1.30 or later. The dependency floors are listed
in [MIGRATION.md](MIGRATION.md#requirements).

## Installation

The packages are not published to NuGet yet. Until they are, consume them from source in
either of two ways.

**Project reference** (a checkout of this repository next to your application):

```xml
<ItemGroup>
  <ProjectReference Include="../spicedb-clients/spicedb-csharp/SpiceDB.Client/SpiceDB.Client.csproj" />
</ItemGroup>
```

**Local package feed** (closest to the published packages):

```bash
# from this repository's root
dotnet pack proto-clients/spicedb-csharp-proto/SpiceDB.Proto.csproj -o ~/spicedb-feed
dotnet pack spicedb-csharp/SpiceDB.Client/SpiceDB.Client.csproj -o ~/spicedb-feed
# only if legacy Authzed.Net code shares the process (see MIGRATION.md)
dotnet pack proto-clients/spicedb-csharp-proto/bridge/Authzed.Net/Authzed.Net.csproj -o ~/spicedb-feed

# in your application
dotnet nuget add source ~/spicedb-feed --name spicedb-local
dotnet add package SpiceDB.Client --version 1.0.0
```

## A first call

```csharp
using SpiceDB.Client;

await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

var txn = new Transaction();
txn.Touch(Relationship.FromTuple("document:readme#viewer@user:alice"));
var written = await client.WriteAsync(txn);

var result = await client.CheckAsync(new PermissionCheck(
    new ObjectRef("document", "readme"), "viewer",
    new ObjectRef("user", "alice"),               // an object converts to a direct subject
    Consistency.AtLeast(written.Revision)));

Console.WriteLine(result.HasPermission);           // true only for an unconditional grant
```

Use `SpiceDBClient.CreateSystemTls("spicedb.example.com:443", token)` for TLS. Plaintext
credentials to a non-loopback host are refused unless
`ClientOptions.RemotePlaintextCredentials` is `Allow`.

Endpoints take one of three forms:

| Form | `CreatePlaintext` | `CreateSystemTls` |
| --- | --- | --- |
| `host:port` | yes | yes |
| `host` (no port) | rejected | yes (port 443) |
| `http://host:port` | yes (the port is required) | rejected |
| `https://host[:port]` | rejected | yes (port defaults to 443) |

A Docker Compose service name such as `spicedb:50051` is a remote host, so plaintext to it
needs both the plaintext factory and the explicit opt-in:

```csharp
// From another service in the same docker-compose.yml
await using var client = SpiceDBClient.CreatePlaintext("spicedb:50051", presharedKey,
    new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow });
```

Without the opt-in, construction fails with a message naming both settings:
`spicedb: refusing to send the preshared key over plaintext to non-loopback endpoint "spicedb:50051". Use SpiceDBClient.CreateSystemTls for TLS, or, to send it in cleartext to a remote host on purpose, call SpiceDBClient.CreatePlaintext(endpoint, presharedKey, new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow })`.

A plaintext endpoint never silently dials port 80: `CreatePlaintext("localhost", key)` fails
with "a plaintext endpoint needs an explicit port, e.g. localhost:50051". Anything else (another scheme, a path, query, fragment, `user@`, a non-numeric port, or a
scheme that disagrees with the factory) throws `InvalidArgumentException` at construction,
naming the problem and these forms. The remote-plaintext check runs on the parsed host.
A failed connection reports the endpoint and the socket error, for example
`Error connecting to subchannel. (endpoint "127.0.0.1:50051"; SocketException: Connection refused)`.

## Testing without a server

`SpiceDB.Client.Testing.InMemorySpiceDBClient` implements `ISpiceDBClient`, the full client
surface. Write code against the interface and pass the double in tests:

```csharp
var store = new InMemorySpiceDBClient();
store.GrantAllChecks();   // or SetCheckResolver(check => ...) / SetCheckResult(...)
```

It stores relationships, answers checks and serves lookups from them, records every call,
and accepts fault injection. See [DESIGN.md](DESIGN.md) for its exact semantics.

## Building and testing

```bash
dotnet test SpiceDB.Client.sln                    # unit tests, both tiers
dotnet build SpiceDB.Client.Examples.sln          # examples
mage integrationTest                              # examples against a SpiceDB container
```
