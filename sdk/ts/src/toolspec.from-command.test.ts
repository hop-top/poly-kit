import { describe, it, expect } from "vitest";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { Command, Option } from "commander";
import { completionCommand } from "./completion";
import {
  buildManifest,
  findCommand,
  loadToolSpec,
  toolSpecFromCommand,
  validateToolSpec,
} from "./toolspec";

function buildProgram(): Command {
  const program = new Command("spaced").version("1.0.0");
  program.option("--format <fmt>", "Output format");

  program
    .command("launch <mission> [vehicle]")
    .alias("go")
    .description("Launch a mission")
    .option("--payload <items...>", "Payload manifest")
    .option("-o, --output <file>", "Write JSON report to file")
    .option("--dry-run", "Simulate launch without executing")
    .addOption(new Option("--orbit <orbit>", "Target orbit").makeOptionMandatory())
    .addOption(new Option("--secret", "internal").hideHelp())
    .action(() => {});

  const mission = program.command("mission").description("Mission catalog");
  mission.command("list").description("List missions").action(() => {});
  mission.command("purge").description("Purge catalog").action(() => {});

  program.command("internal", { hidden: true }).action(() => {});
  return program;
}

describe("toolSpecFromCommand", () => {
  it("projects names, descriptions, aliases and children", () => {
    const spec = toolSpecFromCommand(buildProgram());
    expect(spec.name).toBe("spaced");
    expect(spec.commands.map((c) => c.name)).toEqual(["launch", "mission"]);

    const launch = findCommand(spec, "launch")!;
    expect(launch.short).toBe("Launch a mission");
    expect(launch.aliases).toEqual(["go"]);

    const mission = findCommand(spec, "mission")!;
    expect(mission.children!.map((c) => c.name)).toEqual(["list", "purge"]);
    expect(mission.children![0].short).toBe("List missions");
  });

  it("omits hidden commands and hidden options", () => {
    const spec = toolSpecFromCommand(buildProgram());
    expect(findCommand(spec, "internal")).toBeUndefined();
    const launch = findCommand(spec, "launch")!;
    expect(launch.flags!.some((f) => f.name === "secret")).toBe(false);
  });

  it("projects flags with Go walker naming", () => {
    const spec = toolSpecFromCommand(buildProgram());
    const flags = findCommand(spec, "launch")!.flags!;
    expect(flags).toEqual([
      { name: "payload", type: "stringSlice", description: "Payload manifest" },
      {
        name: "output",
        short: "o",
        type: "string",
        description: "Write JSON report to file",
      },
      {
        name: "dry-run",
        type: "bool",
        description: "Simulate launch without executing",
      },
      {
        name: "orbit",
        type: "string",
        description: "Target orbit",
        required: true,
      },
    ]);
  });

  it("puts root options on spec.flags", () => {
    const spec = toolSpecFromCommand(buildProgram());
    expect(spec.flags!.map((f) => f.name)).toEqual(["version", "format"]);
  });

  it("projects positional arguments in order", () => {
    const spec = toolSpecFromCommand(buildProgram());
    expect(findCommand(spec, "launch")!.args).toEqual([
      { name: "mission", required: true },
      { name: "vehicle" },
    ]);
    expect(findCommand(spec, "list")!.args).toBeUndefined();
  });

  it("includes commands registered after construction", () => {
    const program = buildProgram();
    program.command("plugin-" + "dyn").description("dynamic").action(() => {});
    const spec = toolSpecFromCommand(program);
    expect(findCommand(spec, "plugin-dyn")!.short).toBe("dynamic");
  });

  it("merges per-command annotations keyed by path", () => {
    const spec = toolSpecFromCommand(buildProgram(), {
      annotations: {
        launch: {
          contract: { idempotent: false, retryable: false },
          safety: { level: "dangerous" },
          previewModes: ["--dry-run"],
          short: "Launch (annotated)",
        },
        "mission list": {
          contract: { idempotent: true, retryable: true },
          safety: { level: "safe" },
        },
      },
    });
    const launch = findCommand(spec, "launch")!;
    expect(launch.safety).toEqual({ level: "dangerous" });
    expect(launch.previewModes).toEqual(["--dry-run"]);
    expect(launch.short).toBe("Launch (annotated)");
    expect(launch.flags!.length).toBe(4);
    const list = findCommand(spec, "list")!;
    expect(list.contract).toEqual({ idempotent: true, retryable: true });
    expect(findCommand(spec, "purge")!.safety).toBeUndefined();
  });

  it("produces a spec that validates and defaults schemaVersion", () => {
    const spec = toolSpecFromCommand(buildProgram());
    expect(spec.schemaVersion).toBe("1");
    expect(validateToolSpec(spec)).toEqual([]);
    const custom = toolSpecFromCommand(buildProgram(), { schemaVersion: "2" });
    expect(custom.schemaVersion).toBe("2");
  });

  it("serializes without undefined keys", () => {
    const spec = toolSpecFromCommand(buildProgram());
    const list = findCommand(spec, "list")!;
    expect(Object.keys(list).sort()).toEqual(["name", "short"]);
  });
});

describe("toolSpecFromCommand exit codes", () => {
  it("reads kit/exit-codes off the command, annotation map wins", () => {
    const program = new Command("t");
    const get = program.command("get").action(() => {});
    (get as Command & { annotations?: Record<string, string> }).annotations = {
      "kit/exit-codes": "OK, NOT_FOUND,TRANSIENT",
    };
    const put = program.command("put").action(() => {});
    (put as Command & { annotations?: Record<string, string> }).annotations = {
      "kit/exit-codes": "OK",
    };
    program.command("plain").action(() => {});

    const spec = toolSpecFromCommand(program, {
      annotations: { put: { exitCodes: ["OK", "CONFLICT"] } },
    });
    expect(findCommand(spec, "get")!.exitCodes).toEqual([
      "OK",
      "NOT_FOUND",
      "TRANSIENT",
    ]);
    expect(findCommand(spec, "put")!.exitCodes).toEqual(["OK", "CONFLICT"]);
    expect(findCommand(spec, "plain")!.exitCodes).toBeUndefined();
  });

  it("validateToolSpec rejects an empty exit-code symbol", () => {
    const errors = validateToolSpec({
      name: "t",
      schemaVersion: "1",
      commands: [{ name: "get", exitCodes: ["OK", ""] }],
    });
    expect(errors.some((e) => e.includes("exit code"))).toBe(true);
  });
});

describe("loadToolSpec args/short/required", () => {
  it("maps args, undeclared_args, short and flag required from YAML", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "toolspec-"));
    const file = path.join(dir, "t.toolspec.yaml");
    fs.writeFileSync(
      file,
      [
        "name: t",
        'schema_version: "1"',
        "commands:",
        "  - name: get",
        "    short: Get a thing",
        "    undeclared_args: true",
        "    exit_codes: [OK, NOT_FOUND]",
        "    args:",
        "      - name: id",
        "        required: true",
        "      - name: field",
        "    flags:",
        "      - name: out",
        "        required: true",
      ].join("\n"),
    );
    try {
      const get = loadToolSpec(file).commands[0];
      expect(get.short).toBe("Get a thing");
      expect(get.undeclaredArgs).toBe(true);
      expect(get.exitCodes).toEqual(["OK", "NOT_FOUND"]);
      expect(get.args).toEqual([
        { name: "id", required: true },
        { name: "field", required: undefined },
      ]);
      expect(get.flags![0].required).toBe(true);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
});

describe("toolSpecFromCommand negatable flags", () => {
  it("types --no-x as a bool flag named no-x, as pflag does", () => {
    const program = new Command("t");
    program.command("run").option("--no-color", "c").action(() => {});
    expect(findCommand(toolSpecFromCommand(program), "run")!.flags).toEqual([
      { name: "no-color", type: "bool", description: "c" },
    ]);
  });

  it("types both halves of a --x/--no-x pair bool", () => {
    const program = new Command("t");
    program.option("--hints", "on").option("--no-hints", "off");
    expect(toolSpecFromCommand(program).flags).toEqual([
      { name: "hints", type: "bool", description: "on" },
      { name: "no-hints", type: "bool", description: "off" },
    ]);
  });
});

describe("toolSpecFromCommand builtins and hidden (Go WalkCobra)", () => {
  function tree(): Command {
    const program = new Command("t");
    program.command("real").action(() => {});
    program.command("man").action(() => {});
    program.command("__complete").action(() => {});
    program.addCommand(completionCommand(program));
    const grp = program.command("grp", { hidden: true });
    grp.command("leaf").action(() => {});
    return program;
  }

  it("skips framework builtins: help, completion and its shells, man, __complete", () => {
    const spec = toolSpecFromCommand(tree());
    expect(spec.commands.map((c) => c.name)).toEqual(["real"]);
    expect(findCommand(spec, "bash")).toBeUndefined();
  });

  it("includeHidden keeps hidden commands, never builtins", () => {
    const spec = toolSpecFromCommand(tree(), { includeHidden: true });
    expect(spec.commands.map((c) => c.name)).toEqual(["real", "grp"]);
    expect(findCommand(spec, "grp")!.children!.map((c) => c.name)).toEqual(["leaf"]);
  });

  it("buildManifest skips the same builtins", () => {
    const paths = buildManifest(tree(), "1.1").commands.map((c) => c.path.join(" "));
    expect(paths).toEqual(["t grp leaf", "t real"]);
  });
});
