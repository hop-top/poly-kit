import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

// Resolve the published package name to src/ so code written the way an
// adopter writes it (`import ... from "@hop-top/kit/output"`, as the agent
// skill snippets do) runs without a build. Aliases come from the exports map
// of package.json: "./dist/<x>.js" maps to "./src/<x>.ts". Exact matches
// only, so "@hop-top/kit/upgrade" does not swallow its drivers.
const here = (p: string): string => fileURLToPath(new URL(p, import.meta.url));
const exportsMap: Record<string, { default?: string }> = JSON.parse(
  readFileSync(here("./package.json"), "utf8"),
).exports;

const escape = (s: string): string => s.replace(/[.*+?^${}()|[\]\\/]/g, "\\$&");

const kitAliases = Object.entries(exportsMap).flatMap(([sub, target]) => {
  const dist = target.default;
  if (!dist?.startsWith("./dist/") || !dist.endsWith(".js")) return [];
  return [
    {
      find: new RegExp(`^${escape("@hop-top/kit" + sub.slice(1))}$`),
      replacement: here(dist.replace("./dist/", "./src/").replace(/\.js$/, ".ts")),
    },
  ];
});

export default defineConfig({
  resolve: { alias: kitAliases },
  test: {
    // Every test file starts with an empty XDG config home, so no
    // developer's llm.yaml (or other config) reaches a test.
    setupFiles: ["test/setup-env.ts"],
  },
});
