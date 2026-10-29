/**
 * Tests for layered config loader (config.ts).
 * Covers: merge order, missing-file skip, bad YAML error, envOverride, empty opts,
 * and the per-layer merge rules (objects recursive, arrays/scalars replace,
 * null, type conflicts, non-plain objects).
 */

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { describe, it, expect } from "vitest";
import { load } from "./config";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function tmpYaml(content: string): string {
  const p = path.join(os.tmpdir(), `config-test-${Math.random().toString(36).slice(2)}.yaml`);
  fs.writeFileSync(p, content, "utf8");
  return p;
}

function cleanup(...paths: string[]): void {
  for (const p of paths) {
    try { fs.unlinkSync(p); } catch { /* ignore */ }
  }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("load", () => {
  it("returns dst unchanged when opts is empty", () => {
    const dst = { host: "localhost", port: 3000 };
    const result = load(dst);
    expect(result).toBe(dst);
    expect(result).toEqual({ host: "localhost", port: 3000 });
  });

  it("merges system → user → project in order (later wins)", () => {
    const sys = tmpYaml("host: sys\nport: 1\ndebug: false\n");
    const usr = tmpYaml("host: user\nport: 2\n");
    const proj = tmpYaml("host: project\n");

    const dst = { host: "", port: 0, debug: false };
    try {
      load(dst, { systemConfigPath: sys, userConfigPath: usr, projectConfigPath: proj });
      // project wins for host; user wins for port; system sets debug
      expect(dst.host).toBe("project");
      expect(dst.port).toBe(2);
      expect(dst.debug).toBe(false);
    } finally {
      cleanup(sys, usr, proj);
    }
  });

  it("silently skips missing files", () => {
    const missing = "/tmp/does-not-exist-config-loader-test.yaml";
    const dst = { host: "default" };
    expect(() => load(dst, { systemConfigPath: missing })).not.toThrow();
    expect(dst.host).toBe("default");
  });

  it("throws on bad YAML", () => {
    const bad = tmpYaml("key: [unclosed");
    try {
      const dst = {};
      expect(() => load(dst, { systemConfigPath: bad })).toThrow();
    } finally {
      cleanup(bad);
    }
  });

  it("applies envOverride last, after all file layers", () => {
    const sys = tmpYaml("host: from-file\nport: 80\n");
    const dst = { host: "", port: 0, token: "" };
    try {
      load(dst, {
        systemConfigPath: sys,
        envOverride(cfg) {
          cfg.host = "from-env";
          cfg.token = "secret";
        },
      });
      expect(dst.host).toBe("from-env");   // env wins over file
      expect(dst.port).toBe(80);            // file value preserved
      expect(dst.token).toBe("secret");     // env-only value set
    } finally {
      cleanup(sys);
    }
  });

  it("partial opts: only systemConfigPath provided", () => {
    const sys = tmpYaml("port: 9090\n");
    const dst = { port: 0, host: "default" };
    try {
      load(dst, { systemConfigPath: sys });
      expect(dst.port).toBe(9090);
      expect(dst.host).toBe("default"); // not in yaml — unchanged
    } finally {
      cleanup(sys);
    }
  });

  it("returns dst (same reference)", () => {
    const dst = { x: 1 };
    const result = load(dst, {});
    expect(result).toBe(dst);
  });
});

// ---------------------------------------------------------------------------
// Merge rules
// ---------------------------------------------------------------------------

/** Writes each YAML body to a temp file, loads them in order, cleans up. */
function loadLayers<T extends object>(dst: T, ...bodies: string[]): T {
  const files = bodies.map(tmpYaml);
  try {
    const [systemConfigPath, userConfigPath, projectConfigPath] = files;
    return load(dst, { systemConfigPath, userConfigPath, projectConfigPath });
  } finally {
    cleanup(...files);
  }
}

describe("load merge rules", () => {
  it("merges nested maps across layers instead of replacing them", () => {
    const got = loadLayers<Record<string, unknown>>(
      {},
      "",
      "db:\n  host: x\n  port: 1\n",
      "db:\n  port: 2\n",
    );
    expect(got).toEqual({ db: { host: "x", port: 2 } });
  });

  it("merges at every depth and keeps dst defaults", () => {
    const dst = { a: { b: { c: 1, d: 2 }, keep: true } };
    loadLayers(dst, "a:\n  b:\n    d: 3\n    e: 4\n");
    expect(dst).toEqual({ a: { b: { c: 1, d: 3, e: 4 }, keep: true } });
  });

  it("an empty mapping leaves the object as is", () => {
    const dst = { m: { a: 1 } };
    loadLayers(dst, "m: {}\n");
    expect(dst).toEqual({ m: { a: 1 } });
  });

  it("replaces arrays wholesale, including with an empty one", () => {
    const dst = { tags: ["d"] };
    loadLayers(dst, "tags: [a, b]\n", "tags: [c]\n");
    expect(dst.tags).toEqual(["c"]);
    loadLayers(dst, "tags: []\n");
    expect(dst.tags).toEqual([]);
  });

  it("replaces scalars", () => {
    const dst = { name: "d", port: 1, debug: false };
    loadLayers(dst, "name: a\nport: 2\n", "name: b\ndebug: true\n");
    expect(dst).toEqual({ name: "b", port: 2, debug: true });
  });

  it("null leaves a scalar untouched", () => {
    const dst = { name: "d", port: 1 };
    loadLayers(dst, "name: a\n", "name: ~\nport: null\n");
    expect(dst).toEqual({ name: "a", port: 1 });
  });

  it("null clears objects and arrays", () => {
    const dst = { m: { a: 1 }, tags: ["x"], deep: { inner: { k: "v" } } };
    loadLayers(dst, "m: ~\ntags: ~\ndeep:\n  inner: ~\n");
    expect(dst).toEqual({ m: null, tags: null, deep: { inner: null } });
  });

  it("null sets a key that is absent or already null", () => {
    const dst: Record<string, unknown> = { n: null };
    loadLayers(dst, "n: ~\nfresh: ~\n");
    expect(dst).toEqual({ n: null, fresh: null });
  });

  it("type conflicts: the later value replaces the earlier one", () => {
    const dst: Record<string, unknown> = {
      objToScalar: { a: 1 },
      scalarToObj: 5,
      objToArray: { a: 1 },
      arrayToObj: [1],
    };
    loadLayers(
      dst,
      "objToScalar: 7\nscalarToObj: {b: 2}\nobjToArray: [x]\narrayToObj: {c: 3}\n",
    );
    expect(dst).toEqual({
      objToScalar: 7,
      scalarToObj: { b: 2 },
      objToArray: ["x"],
      arrayToObj: { c: 3 },
    });
  });

  it("does not merge into non-plain objects; replaces them", () => {
    class Endpoint {
      host = "h";
      port = 1;
    }
    const dst: Record<string, unknown> = {
      when: new Date(0),
      ep: new Endpoint(),
      map: new Map([["k", "v"]]),
    };
    loadLayers(dst, "when: {y: 1}\nep: {port: 2}\nmap: {k: w}\n");
    expect(dst).toEqual({ when: { y: 1 }, ep: { port: 2 }, map: { k: "w" } });
    expect(Object.getPrototypeOf(dst.ep)).toBe(Object.prototype);
  });

  it("a YAML timestamp (Date) replaces a plain object", () => {
    const dst: Record<string, unknown> = { when: { y: 1 } };
    loadLayers(dst, "when: 2026-01-02\n");
    expect(dst.when).toBeInstanceOf(Date);
  });

  it("aliased mappings are copied, so later merges do not leak across keys", () => {
    const dst: Record<string, unknown> = {};
    loadLayers(dst, "a: &x {k: v}\nb: *x\n", "a:\n  k: changed\n");
    expect(dst).toEqual({ a: { k: "changed" }, b: { k: "v" } });
  });

  it("does not let __proto__ keys reach Object.prototype", () => {
    const dst: Record<string, unknown> = {};
    loadLayers(dst, "__proto__:\n  polluted: yes\n", "__proto__:\n  polluted2: yes\n");
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
    expect(({} as Record<string, unknown>).polluted2).toBeUndefined();
    expect(Object.getPrototypeOf(dst)).toBe(Object.prototype);
  });

  it("ignores a document whose root is not a mapping", () => {
    const dst = { a: 1 };
    loadLayers(dst, "- x\n- y\n", "just a string\n");
    expect(dst).toEqual({ a: 1 });
  });
});

// ---------------------------------------------------------------------------
// Default parsing (pinned: no parse option means YAML for every file)
// ---------------------------------------------------------------------------

function tmpFile(ext: string, content: string): string {
  const p = path.join(os.tmpdir(), `config-test-${Math.random().toString(36).slice(2)}${ext}`);
  fs.writeFileSync(p, content, "utf8");
  return p;
}

describe("load default parsing", () => {
  it("throws on a duplicate key", () => {
    expect(() => loadLayers({}, "a: 1\na: 2\n")).toThrow(/failed to parse .*duplicated mapping key/);
  });

  it("accepts a trailing comma in a flow collection", () => {
    const got = loadLayers<Record<string, unknown>>({}, "{a: 1, tags: [x, y,],}\n");
    expect(got).toEqual({ a: 1, tags: ["x", "y"] });
  });

  it("parses a .json file as YAML", () => {
    const f = tmpFile(".json", '{"a": {"b": 1}, "tags": ["x"]}');
    try {
      expect(load({}, { projectConfigPath: f })).toEqual({ a: { b: 1 }, tags: ["x"] });
    } finally {
      cleanup(f);
    }
  });

  it("reads a .js file as YAML text and never executes it", () => {
    const scalar = tmpFile(".js", "'use strict'\n");
    const junk = tmpFile(".js", "module.exports = { a: 1 };\n");
    try {
      expect(load({ keep: true }, { projectConfigPath: scalar })).toEqual({ keep: true });
      expect(load({}, { projectConfigPath: junk })).toEqual({ "module.exports = { a": "1 };" });
    } finally {
      cleanup(scalar, junk);
    }
  });
});

// ---------------------------------------------------------------------------
// Resource bounds
// ---------------------------------------------------------------------------

/** `levels` anchors, each a list of `width` aliases of the previous one. */
function aliasBomb(levels: number, width = 9): string {
  const row = (ref: string): string => `[${Array(width).fill(ref).join(", ")}]`;
  let out = `a0: &a0 ${row("lol")}\n`;
  for (let i = 1; i < levels; i++) out += `a${i}: &a${i} ${row(`*a${i - 1}`)}\n`;
  return out;
}

describe("load resource bounds", () => {
  it("fails fast on an exponential alias bomb", () => {
    const started = Date.now();
    expect(() => loadLayers({}, aliasBomb(10))).toThrow(
      /failed to parse .*: document contains excessive aliasing/,
    );
    expect(Date.now() - started).toBeLessThan(2000);
  });

  it("fails on the shared config-v1 excessive aliasing vector", () => {
    const corpus = JSON.parse(
      fs.readFileSync(path.join(__dirname, "../../../contracts/config-v1/load.json"), "utf8"),
    ) as { cases: { name: string; files: string[] }[] };
    const c = corpus.cases.find((x) => x.name === "excessive aliasing");
    expect(c).toBeDefined();
    expect(() => loadLayers({}, ...(c?.files ?? []))).toThrow(/excessive aliasing/);
  });

  it("allows ordinary alias use", () => {
    const got = loadLayers<Record<string, unknown>>({}, aliasBomb(2) + "x: *a1\n");
    expect((got.x as unknown[]).length).toBe(9);
  });

  it("fails on nesting deeper than the limit", () => {
    const deep = "x: " + "[".repeat(150) + "]".repeat(150) + "\n";
    expect(() => loadLayers({}, deep)).toThrow(/failed to parse .*: nesting exceeded maxDepth \(100\)/);
  });

  it("fails on alias chains that nest deeper than the limit", () => {
    let doc = "a0: &a0 [x]\n";
    for (let i = 1; i < 150; i++) doc += `a${i}: &a${i} [*a${i - 1}]\n`;
    expect(() => loadLayers({}, doc)).toThrow(/failed to parse .*: nesting exceeded maxDepth \(100\)/);
  });

  it("accepts nesting at the limit", () => {
    const ok = "x: " + "[".repeat(98) + "]".repeat(98) + "\n";
    expect(() => loadLayers({}, ok)).not.toThrow();
  });
});

