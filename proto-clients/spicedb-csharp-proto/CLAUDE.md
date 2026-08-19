# spicedb-csharp-proto

This is a buf-generated C# proto client for SpiceDB.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `gen/` — those are produced by buf generate
4. Mark deprecated proto methods with `[Obsolete("...")]` attributes
5. Run `dotnet test` after making changes

## Prerequisites

Run `buf generate` before building. The project will not compile without the
generated C# files in `gen/`.

## File layout

- `gen/` — buf-generated code (DO NOT MODIFY)
- `Client.cs` — SpiceDBProtoClient class, constructors, the loopback guard, IDisposable
- `ClientTest.cs` — xUnit tests (construction, disposal, channel ownership)
- `InsecureHostGuardTest.cs` — xUnit tests for the insecure-transport credentials guard
- `TransitiveProtoStubs.cs` — metadata-only stubs for transitive proto dependencies
- `SpiceDB.Proto.csproj` — `net10.0` project file
