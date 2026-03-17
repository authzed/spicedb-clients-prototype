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
- `Client.cs` — SpiceDBProtoClient class, constructor, IDisposable
- `ClientTest.cs` — xUnit tests
- `SpiceDB.Proto.csproj` — .NET 8 project file
