import { describe, it, expect } from "vitest";
import * as fs from "fs";
import * as path from "path";
import { Command, Option } from "commander";
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
      const manifest = buildManifest(program, "1.2", {
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
    ["1.0", "1.2", "1.2"],
    ["1.0", "2.0", "1.2"],
    ["1.0", "1.9", "1.2"],
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
    ["1.1", "1.2", "1.2"],
    ["1.1", "2.0", "1.2"],
    ["1.1", "junk", "1.1"],
    ["1.2", "", "1.2"],
    ["1.2", "1.0", "1.2"],
    ["1.2", "1.1", "1.2"],
    ["1.2", "2.0", "1.2"],
    ["1.2", "junk", "1.2"],
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
    expect(LATEST_SCHEMA_VERSION).toBe("1.2");
    expect(SCHEMA_VERSION_ENV).toBe("KIT_TOOLSPEC_SCHEMA");
  });
});

describe("buildManifest schema 1.2 fields", () => {
  it("projects required, choices and variadic as Go does", () => {
    const program = new Command("t");
    const attach = program.command("attach").action(() => {});
    attach.addOption(
      new Option("--mode <mode>", "Attach mode").choices(["fast", "safe"]).default("fast").makeOptionMandatory(),
    );
    attach.option("--label <text>", "Free-form label");
    (attach as Command & { annotations?: Record<string, string> }).annotations = {
      "kit/args": "name, note?, files...?",
    };
    program.command("cat <files...>").action(() => {});

    const [a, c] = buildManifest(program, "1.2").commands;
    expect(a.args).toEqual([
      { name: "name", required: true },
      { name: "note" },
      { name: "files", variadic: true },
    ]);
    expect(a.flags).toEqual([
      { name: "label", type: "string", description: "Free-form label" },
      {
        name: "mode",
        type: "string",
        description: "Attach mode",
        default: "fast",
        required: true,
        choices: ["fast", "safe"],
      },
    ]);
    // Keys land in Go struct order so the encoder matches Go's bytes.
    expect(Object.keys(a.flags![1])).toEqual([
      "name", "type", "description", "default", "required", "choices",
    ]);
    expect(Object.keys(a.args![2])).toEqual(["name", "variadic"]);
    // commander's own <files...> marks variadic when kit/args is absent.
    expect(c.args).toEqual([{ name: "files", required: true, variadic: true }]);
  });

  it("reads the kit/args grammar exactly as Go: dots before the ?", () => {
    const program = new Command("t");
    const run = program.command("run").action(() => {});
    (run as Command & { annotations?: Record<string, string> }).annotations = {
      "kit/args": " ids... ,, odd?... , plain.. ",
    };
    expect(buildManifest(program, "1.2").commands[0].args).toEqual([
      { name: "ids", required: true, variadic: true },
      { name: "odd?", required: true, variadic: true },
      { name: "plain..", required: true },
    ]);
  });
});

describe("buildManifest negatable flags", () => {
  it("types --no-x bool with default false, as pflag does", () => {
    const program = new Command("t");
    program
      .command("run")
      .option("--no-color", "c")
      .option("--hints", "on")
      .option("--no-hints", "off")
      .action(() => {});
    expect(buildManifest(program, "1.1").commands[0].flags).toEqual([
      { name: "hints", type: "bool", description: "on", default: "false" },
      { name: "no-color", type: "bool", description: "c", default: "false" },
      { name: "no-hints", type: "bool", description: "off", default: "false" },
    ]);
  });

  it("keeps a negated flag's default false whatever commander's default", () => {
    const program = new Command("t");
    program.command("run").option("--no-cache", "nc", true).action(() => {});
    expect(buildManifest(program, "1.1").commands[0].flags).toEqual([
      { name: "no-cache", type: "bool", description: "nc", default: "false" },
    ]);
  });
});
