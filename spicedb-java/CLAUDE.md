# spicedb-java

This is the idiomatic Java client for SpiceDB.

## What to do

1. Read `../DESIGN.md` (root) for overall vision and backwards compat rules
2. Read `./DESIGN.md` for Java-specific goals — this takes precedence
3. When updating after a proto change: check the proto client diff
4. NEVER remove or rename public API methods/types — add new methods instead
5. Propagate deprecation using `@Deprecated` annotation + `@deprecated` Javadoc tag
6. Update `examples/` to cover new functionality
7. Never delete an example — mark deprecated ones with a note instead
8. Update CHANGELOG.md after making changes
9. After making changes, run **`gradle spotlessApply && mage -d spicedb-java lint test`**
   (equivalently `gradle spotlessApply && gradle spotlessCheck :lib:test`), with
   `JAVA_HOME=/opt/homebrew/opt/openjdk@21` — google-java-format needs a JDK it supports.

   **Formatting is not optional and nothing else catches it.** `spotlessCheck` runs in CI
   and is the `lint` job; there is no pre-commit hook, and there is no `gradlew` wrapper in
   this project (the only one in the repo belongs to `spicedb-gen/testdata/java`), so a
   command improvised around a missing wrapper is how unformatted code has reached the
   branch twice — `gradle :lib:test` alone passes while `spotlessCheck` fails.
