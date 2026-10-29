/**
 * Tests for the exported layer merge (mergeLayers), its policy options, and
 * load()'s parse and merge options.
 */

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { describe, it, expect } from "vitest";
import { load, mergeLayers, type ArrayMergeFn, type MergeOptions } from "./config";

function tmpFile(ext: string, content: string): string {
  const p = path.join(os.tmpdir(), `config-merge-${Math.random().toString(36).slice(2)}${ext}`);
  fs.writeFileSync(p, content, "utf8");
  return p;
}

function cleanup(...paths: string[]): void {
  for (const p of paths) {
    try { fs.unlinkSync(p); } catch { /* ignore */ }
  }
}

// ---------------------------------------------------------------------------
// Defaults: same rules as load()
// ---------------------------------------------------------------------------

describe("mergeLayers defaults", () => {
  it("returns a fresh object and never mutates the layers", () => {
    const a = { db: { host: "x", port: 1 }, tags: ["a"] };
    const b = { db: { port: 2 } };
    const got = mergeLayers([a, b]);
    expect(got).toEqual({ db: { host: "x", port: 2 }, tags: ["a"] });
    expect(a).toEqual({ db: { host: "x", port: 1 }, tags: ["a"] });
    expect(got.db).not.toBe(a.db);
    expect(got.tags).not.toBe(a.tags);
  });

  it("merges recursively, replaces arrays, null clears objects/arrays but not scalars", () => {
    const got = mergeLayers([
      { a: { b: { c: 1, d: 2 } }, tags: ["x"], m: { k: 1 }, name: "n", list: [1] },
      { a: { b: { d: 3 } }, tags: ["y"], m: null, name: null, list: null, fresh: null },
    ]);
    expect(got).toEqual({
      a: { b: { c: 1, d: 3 } },
      tags: ["y"],
      m: null,
      name: "n",
      list: null,
      fresh: null,
    });
  });

  it("skips null, undefined and non-mapping layers", () => {
    expect(mergeLayers([{ a: 1 }, null, undefined, ["x"], "s", 3, { b: 2 }])).toEqual({ a: 1, b: 2 });
    expect(mergeLayers([])).toEqual({});
  });

  it("matches load() on the same layers", () => {
    const bodies = ["db:\n  host: x\n  port: 1\ntags: [a]\nn: 1\n", "db:\n  port: 2\ntags: [b]\nn: ~\nm: ~\n"];
    const files = bodies.map((b) => tmpFile(".yaml", b));
    try {
      const viaLoad = load({}, { systemConfigPath: files[0], userConfigPath: files[1] });
      const viaMerge = mergeLayers([
        { db: { host: "x", port: 1 }, tags: ["a"], n: 1 },
        { db: { port: 2 }, tags: ["b"], n: null, m: null },
      ]);
      expect(viaMerge).toEqual(viaLoad);
    } finally {
      cleanup(...files);
    }
  });

  it("does not let __proto__ keys reach Object.prototype", () => {
    const layer = JSON.parse('{"__proto__": {"polluted": 1}}') as object;
    const got = mergeLayers([layer, layer]);
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
    expect(Object.getPrototypeOf(got)).toBe(Object.prototype);
  });

  it("rejects a cyclic layer instead of recursing forever", () => {
    const cyc: Record<string, unknown> = { a: 1 };
    cyc.self = cyc;
    expect(() => mergeLayers([cyc])).toThrow(/config: layer 0: nesting exceeded maxDepth \(100\)/);
  });
});

// ---------------------------------------------------------------------------
// nullPolicy
// ---------------------------------------------------------------------------

describe("mergeLayers nullPolicy", () => {
  it("keep-lower: null never replaces a lower value of any type", () => {
    const got = mergeLayers(
      [
        { m: { a: 1 }, tags: ["x"], name: "n", deep: { inner: { k: "v" } } },
        { m: null, tags: null, name: null, deep: { inner: null } },
      ],
      { nullPolicy: "keep-lower" },
    );
    expect(got).toEqual({ m: { a: 1 }, tags: ["x"], name: "n", deep: { inner: { k: "v" } } });
  });

  it("keep-lower: null at a key no lower layer set leaves it absent", () => {
    const got = mergeLayers([{ a: 1 }, { fresh: null }], { nullPolicy: "keep-lower" });
    expect(got).toEqual({ a: 1 });
    expect(Object.prototype.hasOwnProperty.call(got, "fresh")).toBe(false);
  });

  it("keep-lower: nulls inside a subtree copied whole are kept", () => {
    const got = mergeLayers([{}, { db: { host: null } }], { nullPolicy: "keep-lower" });
    expect(got).toEqual({ db: { host: null } });
  });

  it("clear is the default", () => {
    const layers = [{ m: { a: 1 } }, { m: null }];
    expect(mergeLayers(layers, { nullPolicy: "clear" })).toEqual(mergeLayers(layers));
  });
});

// ---------------------------------------------------------------------------
// maxDepth
// ---------------------------------------------------------------------------

describe("mergeLayers maxDepth", () => {
  const lower = { a: { x: 1, n: { p: 1, q: 1 } }, top: 1 };
  const higher = { a: { y: 2, n: { q: 2 } } };

  it("Infinity (default) merges at every depth", () => {
    expect(mergeLayers([lower, higher])).toEqual({ a: { x: 1, y: 2, n: { p: 1, q: 2 } }, top: 1 });
    expect(mergeLayers([lower, higher], { maxDepth: Infinity })).toEqual(mergeLayers([lower, higher]));
  });

  it("1 merges objects one level below the root; deeper objects replace", () => {
    expect(mergeLayers([lower, higher], { maxDepth: 1 })).toEqual({
      a: { x: 1, y: 2, n: { q: 2 } },
      top: 1,
    });
  });

  it("0 merges root keys only; every nested object replaces", () => {
    expect(mergeLayers([lower, higher], { maxDepth: 0 })).toEqual({ a: { y: 2, n: { q: 2 } }, top: 1 });
  });

  it("rejects a negative or non-integer depth", () => {
    expect(() => mergeLayers([], { maxDepth: -1 })).toThrow(/maxDepth/);
    expect(() => mergeLayers([], { maxDepth: 1.5 })).toThrow(/maxDepth/);
    expect(() => mergeLayers([], { maxDepth: NaN })).toThrow(/maxDepth/);
  });
});

// ---------------------------------------------------------------------------
// arrayPolicy
// ---------------------------------------------------------------------------

describe("mergeLayers arrayPolicy", () => {
  it("concat appends the higher array to the lower one", () => {
    const got = mergeLayers([{ t: ["a", "b"] }, { t: ["b", "c"] }, { t: ["d"] }], { arrayPolicy: "concat" });
    expect(got).toEqual({ t: ["a", "b", "b", "c", "d"] });
  });

  it("union keeps the first occurrence of each item, lower items first", () => {
    const got = mergeLayers([{ t: ["a", "b", "a"] }, { t: ["c", "b"] }], { arrayPolicy: "union" });
    expect(got).toEqual({ t: ["a", "b", "c"] });
  });

  it("union compares objects and arrays structurally, key order ignored", () => {
    const got = mergeLayers(
      [
        { t: [{ k: 1, j: 2 }, [1, 2], NaN] },
        { t: [{ j: 2, k: 1 }, [1, 2], [2, 1], { k: 1 }, NaN, "1", 1] },
      ],
      { arrayPolicy: "union" },
    );
    expect(got).toEqual({ t: [{ k: 1, j: 2 }, [1, 2], NaN, [2, 1], { k: 1 }, "1", 1] });
  });

  it("concat and union act only when both sides are arrays; otherwise the higher value replaces", () => {
    const opts: MergeOptions = { arrayPolicy: "union" };
    expect(mergeLayers([{}, { t: ["a", "a"] }], opts)).toEqual({ t: ["a", "a"] });
    expect(mergeLayers([{ t: "s" }, { t: ["a"] }], opts)).toEqual({ t: ["a"] });
    expect(mergeLayers([{ t: ["a"] }, { t: "s" }], opts)).toEqual({ t: "s" });
  });

  it("applies at every merged depth, not inside objects that replace", () => {
    const layers = [{ a: { t: [1], n: { t: [1] } } }, { a: { t: [2], n: { t: [2] } } }];
    expect(mergeLayers(layers, { arrayPolicy: "concat", maxDepth: 1 })).toEqual({
      a: { t: [1, 2], n: { t: [2] } },
    });
  });

  it("does not share items with the layers", () => {
    const item = { k: 1 };
    const got = mergeLayers([{ t: [] }, { t: [item] }], { arrayPolicy: "concat" });
    expect((got.t as unknown[])[0]).toEqual(item);
    expect((got.t as unknown[])[0]).not.toBe(item);
  });

  it("per-path override by dotted path wins over the default", () => {
    const got = mergeLayers(
      [
        { hints: ["a"], other: ["a"], scope: { hints: ["a"] } },
        { hints: ["b", "a"], other: ["b"], scope: { hints: ["b"] } },
      ],
      { arrayPolicyByPath: { hints: "union", "scope.hints": "concat" } },
    );
    expect(got).toEqual({ hints: ["a", "b"], other: ["b"], scope: { hints: ["a", "b"] } });
  });

  it("per-path custom function gets lower, higher and the path", () => {
    const calls: [unknown[], unknown[], string][] = [];
    const got = mergeLayers(
      [{ a: { t: [1, 2] } }, { a: { t: [3] } }],
      {
        arrayPolicy: "concat",
        arrayPolicyByPath: {
          "a.t": (lower, higher, p) => {
            calls.push([lower, higher, p]);
            return [...higher, ...lower];
          },
        },
      },
    );
    expect(got).toEqual({ a: { t: [3, 1, 2] } });
    expect(calls).toEqual([[[1, 2], [3], "a.t"]]);
  });

  it("a per-path entry only matches the exact path", () => {
    const got = mergeLayers([{ a: { t: [1] }, t: [1] }, { a: { t: [2] }, t: [2] }], {
      arrayPolicyByPath: { t: "concat" },
    });
    expect(got).toEqual({ a: { t: [2] }, t: [1, 2] });
  });

  it("a * segment matches exactly one key at that position", () => {
    const got = mergeLayers(
      [
        { scopes: { a: { hints: ["x"] }, b: { hints: ["x"], deep: { hints: ["x"] } } }, hints: ["x"] },
        { scopes: { a: { hints: ["y"] }, b: { hints: ["y"], deep: { hints: ["y"] } } }, hints: ["y"] },
      ],
      { arrayPolicyByPath: { "scopes.*.hints": "concat" } },
    );
    expect(got).toEqual({
      scopes: { a: { hints: ["x", "y"] }, b: { hints: ["x", "y"], deep: { hints: ["y"] } } },
      hints: ["y"],
    });
  });

  it("a * segment also matches a literal * key; a partial * is literal", () => {
    const got = mergeLayers(
      [{ m: { "*": [1], ab: [1] }, n: { "a*": [1], ab: [1] } }, { m: { "*": [2], ab: [2] }, n: { "a*": [2], ab: [2] } }],
      { arrayPolicyByPath: { "m.*": "concat", "n.a*": "concat" } },
    );
    expect(got).toEqual({ m: { "*": [1, 2], ab: [1, 2] }, n: { "a*": [1, 2], ab: [2] } });
  });

  it("the function form gets the concrete path, not the pattern", () => {
    const paths: string[] = [];
    mergeLayers([{ s: { a: { t: [1] } } }, { s: { a: { t: [2] } } }], {
      arrayPolicyByPath: { "s.*.t": (l, h, p) => (paths.push(p), [...l, ...h]) },
    });
    expect(paths).toEqual(["s.a.t"]);
  });

  it("precedence: exact path, then fewest *, then lexically smallest pattern", () => {
    const layers = [{ a: { b: { c: [1, 1] } } }, { a: { b: { c: [2, 1] } } }];
    const tag = (name: string) => (): unknown => name;
    const pick = (byPath: Record<string, ArrayMergeFn>): unknown =>
      mergeLayers(layers, { arrayPolicyByPath: byPath }).a;
    // exact beats any wildcard, whatever the definition order
    expect(pick({ "*.*.*": tag("3"), "a.*.c": tag("1"), "a.b.c": tag("exact") })).toEqual({ b: { c: "exact" } });
    // fewer * beats more *, whatever the definition order
    expect(pick({ "*.*.*": tag("3"), "*.b.*": tag("2"), "a.*.c": tag("1") })).toEqual({ b: { c: "1" } });
    expect(pick({ "a.*.c": tag("1"), "*.*.*": tag("3") })).toEqual({ b: { c: "1" } });
    // same number of *: lexically smallest pattern ("*" sorts before letters)
    expect(pick({ "a.*.c": tag("a.*.c"), "*.b.c": tag("*.b.c") })).toEqual({ b: { c: "*.b.c" } });
    expect(pick({ "*.b.c": tag("*.b.c"), "a.*.c": tag("a.*.c") })).toEqual({ b: { c: "*.b.c" } });
    expect(pick({ "a.b.*": tag("a.b.*"), "a.*.c": tag("a.*.c") })).toEqual({ b: { c: "a.*.c" } });
  });

  it("a pattern never matches a path with a different number of segments", () => {
    const got = mergeLayers([{ a: { t: [1] }, t: [1] }, { a: { t: [2] }, t: [2] }], {
      arrayPolicyByPath: { "*": "concat", "*.*.*": "concat" },
    });
    expect(got).toEqual({ a: { t: [2] }, t: [1, 2] });
  });

  it("rejects a ** segment", () => {
    expect(() => mergeLayers([], { arrayPolicyByPath: { "a.**.t": "concat" } })).toThrow(
      /arrayPolicyByPath\["a\.\*\*\.t"\]: \*\* is not supported/,
    );
  });

  it("rejects an unknown policy", () => {
    expect(() => mergeLayers([], { arrayPolicy: "merge" as never })).toThrow(/arrayPolicy/);
    expect(() => mergeLayers([], { arrayPolicyByPath: { t: "nope" as never } })).toThrow(/arrayPolicyByPath/);
    expect(() => mergeLayers([], { nullPolicy: "drop" as never })).toThrow(/nullPolicy/);
  });
});

// ---------------------------------------------------------------------------
// load(): parse and merge options
// ---------------------------------------------------------------------------

describe("load parse option", () => {
  it("a parser function handles every file and receives text and path", () => {
    const f = tmpFile(".rc", '{"a": 1, "a": 2}');
    const seen: string[] = [];
    try {
      const got = load<Record<string, unknown>>({}, {
        projectConfigPath: f,
        parse: (text, p) => {
          seen.push(p);
          return JSON.parse(text) as unknown;
        },
      });
      expect(got).toEqual({ a: 2 }); // JSON.parse: duplicate keys, last wins
      expect(seen).toEqual([f]);
    } finally {
      cleanup(f);
    }
  });

  it("a parser map picks by extension and falls back to YAML", () => {
    const sys = tmpFile(".yaml", "a: 1\nlist: [x]\n");
    const usr = tmpFile(".JSON", '{"b": 2, "list": ["y"]}');
    const proj = tmpFile(".js", "module.exports = { c: 3 };\n");
    try {
      const got = load<Record<string, unknown>>({}, {
        systemConfigPath: sys,
        userConfigPath: usr,
        projectConfigPath: proj,
        parse: {
          ".json": (text) => JSON.parse(text) as unknown,
          ".js": (_text, p) => ({ c: p.endsWith(".js") ? 3 : 0 }),
        },
        merge: { arrayPolicy: "union" },
      });
      expect(got).toEqual({ a: 1, b: 2, c: 3, list: ["x", "y"] });
    } finally {
      cleanup(sys, usr, proj);
    }
  });

  it("an extensionless dotfile matches the empty-string key", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "config-merge-"));
    const f = path.join(dir, ".toolrc");
    fs.writeFileSync(f, '{"a": 1, "a": 2}', "utf8");
    try {
      expect(load({}, { projectConfigPath: f, parse: { "": (t) => JSON.parse(t) as unknown } })).toEqual({ a: 2 });
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  it("wraps parser errors with the file path", () => {
    const f = tmpFile(".json", '{"a": 1,}');
    try {
      expect(() => load({}, { projectConfigPath: f, parse: { ".json": (t) => JSON.parse(t) as unknown } })).toThrow(
        new RegExp(`config: failed to parse ${f.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}: `),
      );
    } finally {
      cleanup(f);
    }
  });

  it("still skips missing files and ignores a non-mapping result", () => {
    const f = tmpFile(".json", "a: [unclosed");
    try {
      const got = load({ keep: 1 }, {
        systemConfigPath: "/nonexistent/config-merge-test.json",
        projectConfigPath: f,
        parse: () => [1, 2],
      });
      expect(got).toEqual({ keep: 1 });
    } finally {
      cleanup(f);
    }
  });

  it("applies the bounds check to parser output", () => {
    const f = tmpFile(".json", "{}");
    const cyc: Record<string, unknown> = {};
    cyc.self = cyc;
    try {
      expect(() => load({}, { projectConfigPath: f, parse: () => cyc })).toThrow(/failed to parse .*nesting exceeded/);
    } finally {
      cleanup(f);
    }
  });
});

describe("load merge option", () => {
  it("applies merge policies to file layers, then envOverride assigns plainly", () => {
    const sys = tmpFile(".yaml", "db:\n  host: h\n  opts: {a: 1}\ntags: [a]\nname: n\n");
    const usr = tmpFile(".yaml", "db:\n  opts: {b: 2}\ntags: [b]\nname: ~\n");
    try {
      const got = load<Record<string, unknown>>({}, {
        systemConfigPath: sys,
        userConfigPath: usr,
        merge: { nullPolicy: "keep-lower", maxDepth: 1, arrayPolicy: "union" },
        envOverride(c) {
          c.tags = ["env"];
          c.name = null;
        },
      });
      expect(got).toEqual({ db: { host: "h", opts: { b: 2 } }, tags: ["env"], name: null });
    } finally {
      cleanup(sys, usr);
    }
  });

  it("merges file layers into dst, which acts as the lowest layer", () => {
    const f = tmpFile(".yaml", "tags: [b]\nm: ~\n");
    try {
      const got = load({ tags: ["a"], m: { k: 1 } }, {
        projectConfigPath: f,
        merge: { arrayPolicy: "concat", nullPolicy: "keep-lower" },
      });
      expect(got).toEqual({ tags: ["a", "b"], m: { k: 1 } });
    } finally {
      cleanup(f);
    }
  });
});
