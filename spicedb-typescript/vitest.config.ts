import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

/**
 * Point `@spicedb/proto` at the proto package's SOURCE for tests.
 *
 * `@spicedb/proto`'s package.json declares `"main": "dist/index.js"`, and the
 * workspace link in node_modules resolves to that built output. So `npm test`
 * here exercised whatever `dist/` happened to have been built last, not the
 * current source — and `Magefile.go`'s `Test()` builds only the package under
 * test, so nothing rebuilt it. That is not theoretical: it hid a guard fix in
 * the proto tier and made these tests pass against a version of
 * `isLoopbackEndpoint` that still had the bypass in it.
 *
 * (CI was never exposed — `.github/workflows/typescript.yaml` runs
 * `pnpm --filter @spicedb/proto build` in all four jobs. This closes the local
 * gap, which is the one that actually misled someone.)
 *
 * An alias rather than an `exports` "source" condition, because a condition
 * only helps consumers that opt into requesting it — every runner would need
 * its own configuration, and the next one added would silently get stale
 * `dist/` again. The alias makes it true by default for this package's tests.
 * Production builds are unaffected: `tsc` still resolves the published entry
 * point.
 */
export default defineConfig({
  test: {
    // Source tests only. Vitest 4 widened its default `include` to match
    // compiled output as well, so once `dist/` exists every suite was collected
    // twice -- 17 files became 34, 218 tests became 436 -- and the second copy
    // was whatever `dist/` last happened to hold rather than current source.
    // Vitest 3 did not do this, so the doubling arrived with the 4.x bump.
    include: ["src/**/*.test.ts"],
  },
  resolve: {
    alias: [
      {
        // Anchored, not a bare string key. A string alias matches the key OR
        // the key followed by "/", so `@spicedb/proto` would also capture a
        // future deep import like `@spicedb/proto/gen/...` and rewrite it to
        // `<abs>/src/index.ts/gen/...`, which fails confusingly. No deep
        // import exists today; this makes sure one cannot be broken by this
        // file later.
        find: /^@spicedb\/proto$/,
        replacement: fileURLToPath(
          new URL("../proto-clients/spicedb-typescript-proto/src/index.ts", import.meta.url),
        ),
      },
    ],
  },
});
