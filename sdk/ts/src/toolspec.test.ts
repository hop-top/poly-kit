import { describe, it, expect } from "vitest";
import * as path from "path";
import {
  loadToolSpec,
  validateToolSpec,
  type ToolSpec,
  type Command,
} from "./toolspec";

const EXAMPLE_PATH = path.resolve(
  __dirname,
  "../../../examples/spaced/spaced.toolspec.yaml",
);

describe("loadToolSpec", () => {
  it("parses the spaced example without errors", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    expect(spec.name).toBe("spaced");
    expect(spec.schemaVersion).toBe("1");
  });

  it("loads stateIntrospection", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    expect(spec.stateIntrospection).toBeDefined();
    expect(spec.stateIntrospection!.configCommands).toEqual([
      "spaced config show",
    ]);
    expect(spec.stateIntrospection!.envVars).toEqual([
      "SPACED_FORMAT",
      "SPACED_LOG_LEVEL",
      "SPACED_LOG_FORMAT",
    ]);
  });

  it("loads commands with nested children", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    expect(spec.commands.length).toBeGreaterThanOrEqual(3);

    const launch = spec.commands.find((c) => c.name === "launch");
    expect(launch).toBeDefined();
    expect(launch!.intent?.domain).toBe("space");
    expect(launch!.intent?.category).toBe("operations");
    expect(launch!.intent?.tags).toEqual(["mission", "launch"]);
  });

  it("loads contract fields", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const launch = spec.commands.find((c) => c.name === "launch")!;
    expect(launch.contract).toBeDefined();
    expect(launch.contract!.idempotent).toBe(false);
    expect(launch.contract!.retryable).toBe(false);
    expect(launch.contract!.sideEffects).toHaveLength(2);
  });

  it("loads safety fields", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const launch = spec.commands.find((c) => c.name === "launch")!;
    expect(launch.safety).toBeDefined();
    expect(launch.safety!.level).toBe("dangerous");
    expect(launch.safety!.requiresConfirmation).toBe(false);
  });

  it("loads previewModes", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const launch = spec.commands.find((c) => c.name === "launch")!;
    expect(launch.previewModes).toEqual(["--dry-run"]);
  });

  it("loads outputSchema", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const launch = spec.commands.find((c) => c.name === "launch")!;
    expect(launch.outputSchema).toBeDefined();
    expect(launch.outputSchema!.format).toBe("text");
    expect(launch.outputSchema!.fields).toContain("mission");
  });

  it("loads flags on commands", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const launch = spec.commands.find((c) => c.name === "launch")!;
    expect(launch.flags).toBeDefined();
    expect(launch.flags!.length).toBeGreaterThan(0);
    const payload = launch.flags!.find((f) => f.name === "--payload");
    expect(payload).toBeDefined();
    expect(payload!.type).toBe("string[]");
  });

  it("loads suggestedNext", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const launch = spec.commands.find((c) => c.name === "launch")!;
    expect(launch.suggestedNext).toEqual(["mission list", "telemetry"]);
  });

  it("loads nested children (mission.list)", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const mission = spec.commands.find((c) => c.name === "mission");
    expect(mission).toBeDefined();
    expect(mission!.children).toBeDefined();
    expect(mission!.children!.length).toBeGreaterThanOrEqual(1);
    const list = mission!.children!.find((c) => c.name === "list");
    expect(list).toBeDefined();
    expect(list!.contract?.idempotent).toBe(true);
    expect(list!.safety?.level).toBe("safe");
  });
});

describe("validateToolSpec", () => {
  it("returns no errors for valid spaced example", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const errors = validateToolSpec(spec);
    expect(errors).toEqual([]);
  });

  it("returns error when name is missing", () => {
    const spec: ToolSpec = { name: "", schemaVersion: "1", commands: [] };
    const errors = validateToolSpec(spec);
    expect(errors).toContain("name is required");
  });

  it("returns error when schemaVersion is missing", () => {
    const spec: ToolSpec = { name: "test", schemaVersion: "", commands: [] };
    const errors = validateToolSpec(spec);
    expect(errors).toContain("schemaVersion is required");
  });

  it("returns error when commands is empty", () => {
    const spec: ToolSpec = { name: "test", schemaVersion: "1", commands: [] };
    const errors = validateToolSpec(spec);
    expect(errors).toContain("at least one command is required");
  });

  it("returns error when command name is missing", () => {
    const spec: ToolSpec = {
      name: "test",
      schemaVersion: "1",
      commands: [{ name: "" }],
    };
    const errors = validateToolSpec(spec);
    expect(errors.some((e) => e.includes("command name"))).toBe(true);
  });

  it("returns error for invalid safety level", () => {
    const spec: ToolSpec = {
      name: "test",
      schemaVersion: "1",
      commands: [
        {
          name: "foo",
          safety: { level: "yolo" as any, requiresConfirmation: false },
        },
      ],
    };
    const errors = validateToolSpec(spec);
    expect(errors.some((e) => e.includes("safety level"))).toBe(true);
  });
});

describe("findCommand", () => {
  it("finds top-level command", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const launch = findCmd(spec, "launch");
    expect(launch).toBeDefined();
    expect(launch!.name).toBe("launch");
  });

  it("finds nested command", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    const list = findCmd(spec, "list");
    expect(list).toBeDefined();
    expect(list!.name).toBe("list");
  });

  it("returns undefined for missing command", () => {
    const spec = loadToolSpec(EXAMPLE_PATH);
    expect(findCmd(spec, "nonexistent")).toBeUndefined();
  });
});

// BFS findCommand mirroring Go's ToolSpec.FindCommand
function findCmd(spec: ToolSpec, name: string): Command | undefined {
  const queue: Command[] = [...spec.commands];
  while (queue.length > 0) {
    const c = queue.shift()!;
    if (c.name === name) return c;
    if (c.children) queue.push(...c.children);
  }
  return undefined;
}
