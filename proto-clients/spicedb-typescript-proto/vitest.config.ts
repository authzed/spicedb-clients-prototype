import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Source tests only. Vitest 4 widened its default `include` to match
    // compiled output as well, so once `dist/` exists every suite was collected
    // twice -- 3 files became 6, 59 tests became 118 -- and the second copy was
    // whatever `dist/` last happened to hold rather than current source. Vitest
    // 3 did not do this, so the doubling arrived with the 4.x bump.
    include: ["src/**/*.test.ts"],
  },
});
