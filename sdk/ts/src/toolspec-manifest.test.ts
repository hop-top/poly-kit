import { describe, it, expect } from "vitest";
import * as fs from "fs";
import * as path from "path";
import { Command } from "commander";
import { FIXTURES, fixtureProgram } from "../test/toolspec/fixture";
import {
  buildManifest,
  LATEST_SCHEMA_VERSION,
  negotiateSchemaVersion,
  renderManifestJSON,
  SCHEMA_VERSION_ENV,
} from "./toolspec";

describe("buildManifest cross-language fixture", () => {
  for (const [file, includeDeprecated] of [
    ["toolspec-manifest.json", false],
    ["toolspec-manifest.include-deprecated.json", true],
  ] as const) {
    it(`matches the Go bytes in ${file}`, () => {
      const { program, tree } = fixtureProgram();
      const manifest = buildManifest(program, "1.1", {
        includeDeprecated,
        reserved: tree.reserved,
      });
      const want = fs.readFileSync(path.join(FIXTURES, file), "utf-8");
      expect(renderManifestJSON(manifest)).toBe(want);
    });
  }

  it("throws on a malformed output schema, as Go's encoder does", () => {
    const program = new Command("t");
    const c = program.command("x").action(() => {});
    (c as Command & { annotations?: Record<string, string> }).annotations = {
      "kit/output-schema": "{not json",
    };
    expect(() => renderManifestJSON(buildManifest(program, "1.1"))).toThrow();
  });

  it("emits an empty commands array for a bare program", () => {
    const out = renderManifestJSON(buildManifest(new Command("t"), "1.0"));
    expect(out).toBe(
      '{\n  "tool": "t",\n  "version": "",\n  "schema_version": "1.0",\n  "commands": []\n}\n',
    );
  });
});

describe("negotiateSchemaVersion", () => {
  const cases: Array<[string, string, string]> = [
    ["1.0", "", "1.0"],
    ["1.0", "1.0", "1.0"],
    ["1.0", "1.1", "1.1"],
    ["1.0", "2.0", "1.1"],
    ["1.0", "1.9", "1.1"],
    ["1.0", "0.9", "1.0"],
    ["1.0", "garbage", "1.0"],
    ["1.0", "1", "1.0"],
    ["1.0", "1.1x", "1.0"],
    ["1.0", "+1.1", "1.0"],
    ["1.0", "1.x", "1.0"],
    ["1.0", "1.1.0", "1.0"],
    ["1.0", " 1.1 ", "1.1"],
    ["1.1", "", "1.1"],
    ["1.1", "1.0", "1.1"],
    ["1.1", "1.1", "1.1"],
    ["1.1", "2.0", "1.1"],
    ["1.1", "junk", "1.1"],
    ["2.0", "1.1", "2.0"],
    ["2.0", "3.0", "2.0"],
    ["v1", "1.1", "v1"],
    ["", "1.1", ""],
  ];
  for (const [declared, requested, want] of cases) {
    it(`${JSON.stringify(declared)} + ${JSON.stringify(requested)} -> ${want}`, () => {
      expect(negotiateSchemaVersion(declared, requested)).toBe(want);
    });
  }

  it("pins the env name and latest version", () => {
    expect(LATEST_SCHEMA_VERSION).toBe("1.1");
    expect(SCHEMA_VERSION_ENV).toBe("KIT_TOOLSPEC_SCHEMA");
  });
});
