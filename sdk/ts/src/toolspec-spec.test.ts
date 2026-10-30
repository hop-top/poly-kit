import { describe, it, expect, afterEach } from "vitest";
import * as fs from "fs";
import * as path from "path";
import * as yaml from "js-yaml";
import { Command, CommanderError } from "commander";
import {
  coverageOf,
  findCommand,
  registerSpecCommand,
  toolSpecFromCommand,
} from "./toolspec";
import {
  COVERAGE_REPORT_OUTPUT_SCHEMA,
  MANIFEST_OUTPUT_SCHEMA,
} from "./toolspec-spec-schemas";
import { goFormat0 } from "./toolspec-internal";
import { createCLI } from "./cli";
import { CONTRACTS, FIXTURES, fixtureProgram } from "../test/toolspec/fixture";

interface Run {
  out: string;
  err: string;
  exitCode?: number;
  code?: string;
}

/** Fixture program with a kit-style global --format and spec mounted. */
function specProgram(): Command {
  const { program, tree } = fixtureProgram();
  program.option("--format <fmt>", "output format", "table");
  registerSpecCommand(program, "1.0", { reserved: tree.reserved });
  return program;
}

async function run(program: Command, args: string[]): Promise<Run> {
  const r: Run = { out: "", err: "" };
  const capture = (c: Command): void => {
    c.exitOverride();
    c.configureOutput({
      writeOut: (s) => {
        r.out += s;
      },
      writeErr: (s) => {
        r.err += s;
      },
    });
    for (const sub of c.commands) capture(sub);
  };
  capture(program);
  try {
    await program.parseAsync(["node", program.name(), ...args]);
  } catch (e) {
    if (!(e instanceof CommanderError)) throw e;
    r.exitCode = e.exitCode;
    r.code = e.code;
  }
  return r;
}

function golden(file: string): string {
  return fs.readFileSync(path.join(FIXTURES, file), "utf-8");
}

const savedEnv = process.env.KIT_TOOLSPEC_SCHEMA;
afterEach(() => {
  if (savedEnv === undefined) delete process.env.KIT_TOOLSPEC_SCHEMA;
  else process.env.KIT_TOOLSPEC_SCHEMA = savedEnv;
});

describe("registerSpecCommand cross-language fixture", () => {
  for (const [file, args] of [
    ["toolspec-spec.json", ["spec", "--format", "json"]],
    ["toolspec-spec.default.json", ["spec"]],
    [
      "toolspec-spec.include-deprecated.json",
      ["spec", "--format", "kit-manifest", "--include-deprecated"],
    ],
    ["toolspec-spec.version.json", ["spec", "--version"]],
    ["toolspec-spec.coverage.json", ["spec", "coverage", "--format", "json"]],
  ] as const) {
    it(`matches Go stdout for \`fixture ${args.join(" ")}\``, async () => {
      process.env.KIT_TOOLSPEC_SCHEMA = "1.2";
      const r = await run(specProgram(), [...args]);
      expect(r.exitCode).toBeUndefined();
      expect(r.out).toBe(golden(file));
    });
  }
});

describe("registerSpecCommand formats", () => {
  it("accepts the manifest and kit aliases with the same bytes", async () => {
    process.env.KIT_TOOLSPEC_SCHEMA = "1.2";
    for (const f of ["manifest", "kit"]) {
      const r = await run(specProgram(), ["spec", "--format", f]);
      expect(r.out).toBe(golden("toolspec-spec.json"));
    }
  });

  it("resolves schema_version from the declared floor without a request", async () => {
    delete process.env.KIT_TOOLSPEC_SCHEMA;
    const r = await run(specProgram(), ["spec", "--version"]);
    expect(r.out).toBe('{\n  "schema_version": "1.0"\n}\n');
  });

  it("renders yaml as the snake_case manifest", async () => {
    const r = await run(specProgram(), ["spec", "--format", "yaml"]);
    const doc = yaml.load(r.out) as { schema_version: string; commands: unknown[] };
    expect(doc.schema_version).toBe("1.0");
    expect(doc.commands.length).toBeGreaterThan(0);
    const v = await run(specProgram(), ["spec", "--version", "--format", "yaml"]);
    expect(v.out).toBe('schema_version: "1.0"\n');
  });

  it("prints the bare version for non-structured formats", async () => {
    const r = await run(specProgram(), ["spec", "--version", "--format", "table"]);
    expect(r.out).toBe("1.0\n");
  });

  it("emits nothing for table/csv/text, as Go does", async () => {
    for (const f of ["table", "csv", "text"]) {
      const r = await run(specProgram(), ["spec", "--format", f]);
      expect(r.exitCode).toBeUndefined();
      expect(r.out).toBe("");
    }
  });

  it("rejects an unknown format listing the valid names", async () => {
    const r = await run(specProgram(), ["spec", "--format", "bogus"]);
    expect(r.exitCode).toBe(1);
    expect(r.err).toContain(
      'unknown --format "bogus" (valid: kit-manifest, json, yaml, table, csv, text)',
    );
  });

  it("adds a local --format when the program has none", async () => {
    const { program } = fixtureProgram();
    registerSpecCommand(program, "1.1");
    const r = await run(program, ["spec", "--format", "json"]);
    const doc = JSON.parse(r.out) as { commands: { path: string[]; flags?: { name: string }[] }[] };
    const spec = doc.commands.find((c) => c.path.join(" ") === "fixture spec")!;
    expect(spec.flags!.map((f) => f.name)).toContain("format");
  });
});

describe("spec --version vs the program's --version", () => {
  it("answers spec --version under createCLI's -v, --version", async () => {
    delete process.env.KIT_TOOLSPEC_SCHEMA;
    const { program } = createCLI({ name: "mytool", version: "1.2.3", description: "d" });
    registerSpecCommand(program, "1.0");
    const r = await run(program, ["spec", "--version"]);
    expect(r.code).toBeUndefined();
    expect(r.out).toBe('{\n  "schema_version": "1.0"\n}\n');
  });

  it("forwards wherever the flag sits and with program options before spec", async () => {
    process.env.KIT_TOOLSPEC_SCHEMA = "1.2";
    for (const args of [
      ["--version", "spec"],
      ["--format", "json", "spec", "--version"],
      ["spec", "--format", "yaml", "--version"],
    ]) {
      const r = await run(specProgram(), args);
      expect(r.code, args.join(" ")).toBeUndefined();
      expect(r.out, args.join(" ")).toMatch(/schema_version/);
    }
  });

  it("forwards on each parse of the same program", async () => {
    const program = specProgram();
    expect((await run(program, ["spec", "--version"])).out).toMatch(/schema_version/);
    expect((await run(program, ["spec", "--format", "json"])).out).toMatch(/"commands"/);
  });

  it("keeps the tool version everywhere else", async () => {
    for (const args of [
      ["--version"],
      ["-v"],
      ["launch", "--version"],
      ["spec", "-v"],
      ["spec", "coverage", "--version"],
    ]) {
      const r = await run(specProgram(), args);
      expect(r.code, args.join(" ")).toBe("commander.version");
      expect(r.out, args.join(" ")).toBe("1.2.3\n");
    }
  });

  it("forwards when .version() is called after registerSpecCommand", async () => {
    const { program, tree } = fixtureProgram();
    const late = new Command(tree.tool);
    for (const c of [...program.commands]) late.addCommand(c);
    registerSpecCommand(late, "1.0");
    late.version("9.9.9", "-v, --version");
    const r = await run(late, ["spec", "--version"]);
    expect(r.out).toMatch(/schema_version/);
    expect((await run(late, ["--version"])).out).toBe("9.9.9\n");
  });
});

describe("spec coverage", () => {
  it("prints the report then exits CONFLICT below --min", async () => {
    const r = await run(specProgram(), ["spec", "coverage", "--format", "json", "--min", "90"]);
    expect(r.out).toBe(golden("toolspec-spec.coverage.json"));
    expect(r.exitCode).toBe(4);
    expect(r.err).toContain(
      "annotation coverage 80% (4/5) is below --min 90%; unannotated: fixture mission purge",
    );
  });

  it("passes at or below the measured percentage", async () => {
    const r = await run(specProgram(), ["spec", "coverage", "--min", "80"]);
    expect(r.exitCode).toBeUndefined();
    expect(r.out).toBe(golden("toolspec-spec.coverage.json"));
  });

  it("reports an unmeasurable tree and fails any positive gate", async () => {
    const program = new Command("empty");
    registerSpecCommand(program, "1.0");
    const rep = coverageOf(program);
    expect(rep.measurable).toBe(false);
    expect(rep.total).toBe(0);
    const r = await run(program, ["spec", "coverage", "--min", "1"]);
    expect(r.exitCode).toBe(4);
    expect(r.err).toContain(
      'annotation coverage unmeasurable: no reflectable commands in "empty" (--min 1% cannot be evaluated)',
    );
  });

  it("counts malformed side-effect values against coverage", () => {
    const program = new Command("t");
    const c = program.command("x").action(() => {});
    (c as Command & { annotations?: Record<string, string> }).annotations = {
      "kit/side-effect": "wrte",
    };
    const rep = coverageOf(program);
    expect(rep).toMatchObject({
      total: 1,
      annotated: 0,
      unannotated: 1,
      malformed: 1,
      malformed_paths: ['t x="wrte"'],
      unannotated_paths: ["t x"],
    });
  });
});

describe("goFormat0", () => {
  it("rounds half to even like Go's %.0f", () => {
    expect([0.5, 1.5, 2.5, 3.5, 66.666, 80, 0].map(goFormat0)).toEqual([
      "0", "2", "2", "4", "67", "80", "0",
    ]);
  });
});

describe("spec output schemas", () => {
  it("embed the Go-reflected contract bytes", () => {
    const dir = path.join(CONTRACTS, "toolspec-spec-v1");
    expect(MANIFEST_OUTPUT_SCHEMA).toBe(
      fs.readFileSync(path.join(dir, "manifest.schema.json"), "utf-8"),
    );
    expect(COVERAGE_REPORT_OUTPUT_SCHEMA).toBe(
      fs.readFileSync(path.join(dir, "coverage-report.schema.json"), "utf-8"),
    );
  });
});

describe("toolSpecFromCommand and spec", () => {
  it("leaves the spec command out of the ToolSpec, as WalkCobra does", () => {
    const spec = toolSpecFromCommand(specProgram());
    expect(findCommand(spec, "spec")).toBeUndefined();
    expect(findCommand(spec, "coverage")).toBeUndefined();
  });
});
