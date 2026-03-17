# spicedb-java-proto

This is a buf-generated Java proto client for SpiceDB.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `gen/` — those are produced by buf generate
4. Mark deprecated proto methods with `@Deprecated` annotations
5. Run `gradle test` after making changes

## File layout

- `gen/` — buf-generated code (DO NOT MODIFY)
- `src/main/java/com/authzed/spicedb/proto/SpiceDBProtoClient.java` — Client class
- `src/test/java/com/authzed/spicedb/proto/SpiceDBProtoClientTest.java` — tests
- `build.gradle.kts` — Gradle build config
- `settings.gradle.kts` — Gradle settings
