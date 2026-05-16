/**
 * Tests for layered config loader (config.ts).
 * Covers: merge order, missing-file skip, bad YAML error, envOverride, empty opts.
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
