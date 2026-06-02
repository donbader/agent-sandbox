import { defineConfig } from "vitest/config";
import path from "path";

export default defineConfig({
  test: {
    include: [
      "src/**/*.test.ts",
      "../internal/plugins/*/channel/**/*.test.ts",
    ],
  },
  resolve: {
    alias: {
      // Plugin channel files import from "../" expecting channel-manager/src/ context.
      // At build time, the Go generator copies them into src/channel/ where paths resolve.
      // For testing, we map these imports to their actual locations.
      "../startup-buffer.js": path.resolve(__dirname, "src/startup-buffer.ts"),
      "../safe-prompt.js": path.resolve(__dirname, "src/safe-prompt.ts"),
    },
  },
});
