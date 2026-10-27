import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Every test file starts with an empty XDG config home, so no
    // developer's llm.yaml (or other config) reaches a test.
    setupFiles: ["test/setup-env.ts"],
  },
});
