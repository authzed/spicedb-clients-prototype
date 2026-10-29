# spicedb-csharp-proto

This is a buf-generated C# proto client for SpiceDB.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `gen/` — those are produced by buf generate
4. Mark deprecated proto methods with `[Obsolete("...")]` attributes
5. Run `dotnet test tests/SpiceDB.Proto.Tests.csproj` (or `mage test`) after making changes
6. After `buf generate`, run `mage bridge` to regenerate the Authzed.Net bridge forwards

## Prerequisites

Run `buf generate` before building. The project will not compile without the
generated C# files in `gen/`.

## File layout

- `gen/` — buf-generated code (DO NOT MODIFY)
- `Client.cs` — SpiceDBProtoClient class, constructors, the loopback guard, IDisposable
- `tests/` — `SpiceDB.Proto.Tests.csproj`: `ClientTest.cs` (construction, disposal, channel
  ownership), `InsecureHostGuardTest.cs` (insecure-transport credentials guard),
  `AuthzedNetBridgeTest.cs` (legacy bridge, both assemblies in one process),
  `DescriptorInitializationTest.cs` (every generated descriptor initialises, in-process and
  in a fresh process via `tests/DescriptorProbe/`)
- `bridge/` — `generator/` writes `Authzed.Net/TypeForwards.g.cs` (DO NOT EDIT BY HAND);
  `Authzed.Net/` is the legacy-package bridge assembly
- `TransitiveProtoStubs.cs` — metadata-only stub descriptors for transitive proto
  dependencies, each with its real file name
- `SpiceDB.Proto.csproj` — `net8.0;net9.0;net10.0` project file
