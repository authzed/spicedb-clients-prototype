# spicedb-typescript

This is the idiomatic TypeScript client for SpiceDB.

## What to do

1. Read `../DESIGN.md` (root) for overall vision and backwards compat rules
2. Read `./DESIGN.md` for TypeScript-specific goals — this takes precedence
3. When updating after a proto change: check the proto client diff
4. NEVER remove or rename public API methods/types — add new methods instead
5. Propagate deprecation using `@deprecated` JSDoc tags
6. Update `examples/` to cover new functionality
7. Never delete an example — mark deprecated ones with a note instead
8. Append to the Changelog section of DESIGN.md after making changes
9. Run `yarn build && yarn test` after making changes
10. No `any` types in public API — use `unknown` if truly needed
