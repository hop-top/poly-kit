/**
 * Runs every agent-skill snippet under skills/<skill>/assets/snippets/<name>/
 * and compares what it prints with <name>.expected. A snippet imports
 * `@hop-top/kit/<sub>` as an adopter would; vitest.config.ts resolves that to
 * src/, so no build is needed.
 */

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { format } from "node:util";
import { describe, expect, it, vi } from "vitest";

const root = fileURLToPath(new URL("../skills", import.meta.url));

const snippets = readdirSync(root, { withFileTypes: true })
  .filter((d) => d.isDirectory())
  .flatMap((skill) => {
    const dir = join(root, skill.name, "assets", "snippets");
    if (!existsSync(dir)) return [];
    return readdirSync(dir, { withFileTypes: true })
      .filter((d) => d.isDirectory())
      .map((d) => ({
        id: `${skill.name}/${d.name}`,
        source: join(dir, d.name, `${d.name}.ts`),
        expected: join(dir, d.name, `${d.name}.expected`),
      }));
  });

/** Imports the snippet and returns what it wrote to stdout and console.log. */
async function run(source: string): Promise<string> {
  const out: string[] = [];
  const spy = vi.spyOn(process.stdout, "write").mockImplementation(((
    chunk: string | Uint8Array,
  ) => {
    out.push(String(chunk));
    return true;
  }) as typeof process.stdout.write);
  const log = vi.spyOn(console, "log").mockImplementation((...args: unknown[]) => {
    out.push(format(...args) + "\n");
  });
  try {
    await import(/* @vite-ignore */ source);
  } finally {
    spy.mockRestore();
    log.mockRestore();
  }
  return out.join("");
}

describe("skill snippets", () => {
  it("finds at least one snippet", () => {
    expect(snippets.length).toBeGreaterThan(0);
  });

  it.each(snippets)("$id matches its .expected", async ({ source, expected }) => {
    expect(existsSync(source)).toBe(true);
    expect(await run(source)).toBe(readFileSync(expected, "utf8"));
  });
});
