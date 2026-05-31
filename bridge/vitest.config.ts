import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: [
      "src/**/*.test.ts",
      "../internal/plugins/*/bridge/**/*.test.ts",
    ],
  },
});
