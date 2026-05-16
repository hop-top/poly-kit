/**
 * compliance.ts — 12-factor AI CLI compliance checker.
 *
 * Static checks analyse a toolspec YAML. Runtime checks execute
 * the binary. Port of Go hop.top/kit/compliance.
 */

import * as fs from "fs";
import * as yaml from "js-yaml";
import { execFileSync } from "child_process";

// --- Types ---

export enum Factor {
  SelfDescribing = 1,
  StructuredIO = 2,
  StreamDiscipline = 3,
  ContractsErrors = 4,
  Preview = 5,
  Idempotency = 6,
  StateTransparency = 7,
  SafeDelegation = 8,
  ObservableOps = 9,
  Provenance = 10,
  Evolution = 11,
  AuthLifecycle = 12,
}

const factorNames: Record<number, string> = {
  [Factor.SelfDescribing]: "Self-Describing",
  [Factor.StructuredIO]: "Structured I/O",
  [Factor.StreamDiscipline]: "Stream Discipline",
  [Factor.ContractsErrors]: "Contracts & Errors",
  [Factor.Preview]: "Preview",
  [Factor.Idempotency]: "Idempotency",
  [Factor.StateTransparency]: "State Transparency",
  [Factor.SafeDelegation]: "Safe Delegation",
  [Factor.ObservableOps]: "Observable Ops",
  [Factor.Provenance]: "Provenance",
  [Factor.Evolution]: "Evolution",
  [Factor.AuthLifecycle]: "Auth Lifecycle",
};

export function factorName(f: Factor): string {
  return factorNames[f] ?? `Factor(${f})`;
}

export interface CheckResult {
  factor: Factor;
  name: string;
  status: "pass" | "fail" | "skip" | "warn";
  details?: string;
  suggestion?: string;
}

export interface Report {
  binary: string;
  toolspec: string;
  results: CheckResult[];
  score: number;
  total: number;
}

// --- Internal YAML types ---

interface SpecYAML {
  name?: string;
  schema_version?: string;
  commands?: CmdYAML[];
  state_introspection?: {
    config_commands?: string[];
    auth_commands?: string[];
  };
}

interface CmdYAML {
  name?: string;
  children?: CmdYAML[];
  contract?: {
    idempotent?: boolean;
    side_effects?: string[];
  };
  safety?: {
    level?: string;
    requires_confirmation?: boolean;
  };
  preview_modes?: string[];
  output_schema?: { format?: string };
}

// --- Helpers ---

function loadSpec(path: string): SpecYAML {
  const raw = fs.readFileSync(path, "utf-8");
  return yaml.load(raw) as SpecYAML;
}

function allCommands(cmds: CmdYAML[]): CmdYAML[] {
  const out: CmdYAML[] = [];
  for (const c of cmds) {
    out.push(c);
    if (c.children) out.push(...allCommands(c.children));
  }
  return out;
}

function mutatingCommands(cmds: CmdYAML[]): CmdYAML[] {
  return allCommands(cmds).filter(
    (c) =>
      c.contract?.side_effects &&
      c.contract.side_effects.length > 0,
  );
}

function dangerousCommands(cmds: CmdYAML[]): CmdYAML[] {
  return allCommands(cmds).filter(
    (c) => c.safety?.level === "dangerous",
  );
}

function pass(
  f: Factor,
  details: string,
): CheckResult {
  return {
    factor: f,
    name: factorName(f),
    status: "pass",
    details,
  };
}

function fail(
  f: Factor,
  details: string,
  suggestion: string,
): CheckResult {
  return {
    factor: f,
    name: factorName(f),
    status: "fail",
    details,
    suggestion,
  };
}

function skip(
  f: Factor,
  details: string,
): CheckResult {
  return {
    factor: f,
    name: factorName(f),
    status: "skip",
    details,
  };
}

// --- Static Checks ---

function checkSelfDescribing(spec: SpecYAML): CheckResult {
  const f = Factor.SelfDescribing;
  if (!spec.commands || spec.commands.length === 0) {
    return fail(f, "no commands defined",
      "Add a commands array with at least one named command");
  }
  for (const c of spec.commands) {
    if (!c.name) {
      return fail(f, "command missing name",
        "Every command must have a name field");
    }
  }
  return pass(f, "commands array non-empty, all named");
}

function checkStructuredIO(spec: SpecYAML): CheckResult {
  const f = Factor.StructuredIO;
  for (const c of allCommands(spec.commands ?? [])) {
    if (c.output_schema) {
      return pass(f, `output_schema found on ${c.name}`);
    }
  }
  return fail(f, "no command has output_schema",
    "Add output_schema to at least one command");
}

function checkContractsErrors(spec: SpecYAML): CheckResult {
  const f = Factor.ContractsErrors;
  const mut = mutatingCommands(spec.commands ?? []);
  if (mut.length === 0) {
    for (const c of allCommands(spec.commands ?? [])) {
      if (c.contract) return pass(f, "contracts found");
    }
    return fail(f, "no contracts declared",
      "Add contract fields to commands");
  }
  for (const c of mut) {
    if (!c.contract) {
      return fail(f, `${c.name} has side_effects but no contract`,
        "Add contract fields to mutating commands");
    }
  }
  return pass(f, "all mutating commands have contracts");
}

function checkPreview(spec: SpecYAML): CheckResult {
  const f = Factor.Preview;
  const mut = mutatingCommands(spec.commands ?? []);
  if (mut.length === 0) {
    return pass(f, "no mutating commands to preview");
  }
  let withPreview = 0;
  for (const c of mut) {
    if (c.preview_modes && c.preview_modes.length > 0) {
      withPreview++;
    }
  }
  if (withPreview === 0) {
    return fail(f, "no mutating command has preview_modes",
      "Add preview_modes (e.g. --dry-run) to mutating commands");
  }
  return pass(f,
    `${withPreview}/${mut.length} mutating commands have preview_modes`);
}

function checkIdempotency(spec: SpecYAML): CheckResult {
  const f = Factor.Idempotency;
  const all = allCommands(spec.commands ?? []);
  if (all.length === 0) {
    return fail(f, "no commands", "Add commands");
  }
  let declared = 0;
  for (const c of all) {
    if (c.contract && c.contract.idempotent !== undefined) {
      declared++;
    }
  }
  if (declared === 0) {
    return fail(f, "no command declares idempotent",
      "Add contract.idempotent to each command");
  }
  return pass(f, "idempotency declared on commands");
}

function checkStateTransparency(spec: SpecYAML): CheckResult {
  const f = Factor.StateTransparency;
  const si = spec.state_introspection;
  if (!si?.config_commands || si.config_commands.length === 0) {
    return fail(f, "no config_commands in state_introspection",
      "Add state_introspection.config_commands");
  }
  return pass(f, "config_commands present");
}

function checkSafeDelegation(spec: SpecYAML): CheckResult {
  const f = Factor.SafeDelegation;
  const dangerous = dangerousCommands(spec.commands ?? []);
  if (dangerous.length === 0) {
    return pass(f, "no dangerous commands");
  }
  for (const c of dangerous) {
    if (!c.safety) {
      return fail(f,
        `${c.name} is dangerous but has no safety block`,
        "Add safety with requires_confirmation");
    }
  }
  return pass(f, "all dangerous commands have safety metadata");
}

function checkEvolution(spec: SpecYAML): CheckResult {
  const f = Factor.Evolution;
  if (!spec.schema_version) {
    return fail(f, "schema_version not set",
      "Set schema_version in the toolspec");
  }
  return pass(f, `schema_version: ${spec.schema_version}`);
}

function checkAuthLifecycle(spec: SpecYAML): CheckResult {
  const f = Factor.AuthLifecycle;
  const si = spec.state_introspection;
  if (!si?.auth_commands || si.auth_commands.length === 0) {
    return skip(f,
      "no auth_commands — skipped (tool may not need auth)");
  }
  return pass(f, "auth_commands present");
}

function runStaticChecks(spec: SpecYAML): CheckResult[] {
  return [
    checkSelfDescribing(spec),
    checkStructuredIO(spec),
    skip(Factor.StreamDiscipline, "runtime check only"),
    checkContractsErrors(spec),
    checkPreview(spec),
    checkIdempotency(spec),
    checkStateTransparency(spec),
    checkSafeDelegation(spec),
    skip(Factor.ObservableOps, "runtime check only"),
    skip(Factor.Provenance, "runtime check only"),
    checkEvolution(spec),
    checkAuthLifecycle(spec),
  ];
}

// --- Runtime Checks ---

function execBin(
  bin: string,
  args: string[],
): { stdout: string; stderr: string; code: number } {
  try {
    const stdout = execFileSync(bin, args, {
      timeout: 10000,
      stdio: ["pipe", "pipe", "pipe"],
    }).toString();
    return { stdout, stderr: "", code: 0 };
  } catch (e: any) {
    return {
      stdout: e.stdout?.toString() ?? "",
      stderr: e.stderr?.toString() ?? "",
      code: e.status ?? 1,
    };
  }
}

function findReadCommand(spec: SpecYAML): string | undefined {
  for (const c of allCommands(spec.commands ?? [])) {
    if (c.output_schema && c.contract?.idempotent === true) {
      return c.name;
    }
  }
  return undefined;
}

function isValidJSON(s: string): boolean {
  try {
    JSON.parse(s.trim());
    return true;
  } catch {
    return false;
  }
}

function runRuntimeChecks(
  bin: string,
  spec: SpecYAML,
): CheckResult[] {
  const results: CheckResult[] = [];

  // F1: --help exits 0
  {
    const f = Factor.SelfDescribing;
    const r = execBin(bin, ["--help"]);
    if (r.code !== 0) {
      results.push(fail(f, `--help exited ${r.code}`,
        "Ensure --help exits 0"));
    } else {
      const upper = r.stdout.toUpperCase();
      if (!upper.includes("COMMANDS") && !upper.includes("USAGE")) {
        results.push(fail(f, "--help lacks COMMANDS/USAGE",
          "Help should list available commands"));
      } else {
        results.push(pass(f,
          "--help exits 0, contains command listing"));
      }
    }
  }

  // F2: read cmd --format json
  {
    const f = Factor.StructuredIO;
    const readCmd = findReadCommand(spec);
    if (!readCmd) {
      results.push(skip(f, "no read command found"));
    } else {
      const r = execBin(bin, [readCmd, "--format", "json"]);
      if (r.code !== 0) {
        results.push(
          fail(f, `${readCmd} --format json exited ${r.code}`,
            "Read commands should support --format json"));
      } else if (!isValidJSON(r.stdout)) {
        results.push(fail(f, "output is not valid JSON",
          "--format json should produce valid JSON"));
      } else {
        results.push(
          pass(f, `${readCmd} --format json returns valid JSON`));
      }
    }
  }

  // F3: stream discipline
  {
    const f = Factor.StreamDiscipline;
    const readCmd = findReadCommand(spec);
    if (!readCmd) {
      results.push(skip(f, "no read command found"));
    } else {
      const r = execBin(bin, [readCmd, "--format", "json"]);
      if (!r.stdout.trim()) {
        results.push(fail(f, "stdout is empty",
          "Data should go to stdout"));
      } else if (
        isValidJSON(r.stderr) && r.stderr.trim().length > 2
      ) {
        results.push(fail(f,
          "stderr contains JSON",
          "Keep structured data on stdout, logs on stderr"));
      } else {
        results.push(pass(f, "stdout has data, stderr clean"));
      }
    }
  }

  // F4: bogus arg
  {
    const f = Factor.ContractsErrors;
    const r = execBin(bin, ["--bogus-arg-xyzzy"]);
    if (r.code === 0) {
      results.push(fail(f, "bogus arg didn't cause error exit",
        "Unknown flags should cause non-zero exit"));
    } else {
      results.push({
        factor: f,
        name: factorName(f),
        status: "warn",
        details: "error output is not structured JSON",
        suggestion:
          "Return JSON errors with a 'code' field on stderr",
      });
    }
  }

  // F5: preview
  {
    const f = Factor.Preview;
    const mut = mutatingCommands(spec.commands ?? []);
    if (mut.length === 0) {
      results.push(skip(f, "no mutating commands"));
    } else {
      let found = false;
      for (const c of mut) {
        for (const mode of c.preview_modes ?? []) {
          const r = execBin(bin, [c.name!, mode]);
          if (r.code === 0) {
            results.push(
              pass(f, `${c.name} ${mode} exits 0`));
            found = true;
            break;
          }
        }
        if (found) break;
      }
      if (!found) {
        results.push(fail(f,
          "no mutating command succeeds with preview mode",
          "Ensure --dry-run exits 0"));
      }
    }
  }

  // F7: config command
  {
    const f = Factor.StateTransparency;
    let r = execBin(bin, ["config", "show"]);
    if (r.code === 0) {
      results.push(pass(f, "config show exits 0"));
    } else {
      r = execBin(bin, ["config"]);
      if (r.code === 0) {
        results.push(pass(f, "config exits 0"));
      } else {
        results.push(fail(f, "config command failed",
          "Add a config/config show command"));
      }
    }
  }

  // F8: safe delegation
  {
    const f = Factor.SafeDelegation;
    const dangerous = dangerousCommands(spec.commands ?? []);
    if (dangerous.length === 0) {
      results.push(skip(f, "no dangerous commands"));
    } else {
      results.push(pass(f,
        "dangerous commands have safety metadata"));
    }
  }

  // F10: provenance
  {
    const f = Factor.Provenance;
    const readCmd = findReadCommand(spec);
    if (!readCmd) {
      results.push(skip(f, "no read command found"));
    } else {
      const r = execBin(bin, [readCmd, "--format", "json"]);
      if (r.code !== 0) {
        results.push(skip(f, `${readCmd} failed`));
      } else {
        try {
          const obj = JSON.parse(r.stdout.trim());
          if (obj._meta) {
            results.push(pass(f, "_meta field present"));
          } else {
            results.push(fail(f, "no _meta field in JSON output",
              "Add _meta with provenance info"));
          }
        } catch {
          results.push(skip(f, "output not JSON object"));
        }
      }
    }
  }

  // F11: --version
  {
    const f = Factor.Evolution;
    const r = execBin(bin, ["--version"]);
    if (r.code !== 0) {
      results.push(
        fail(f, `--version exited ${r.code}`,
          "Ensure --version exits 0"));
    } else {
      results.push(pass(f, "--version exits 0"));
    }
  }

  // F12: auth
  {
    const f = Factor.AuthLifecycle;
    const si = spec.state_introspection;
    if (!si?.auth_commands || si.auth_commands.length === 0) {
      results.push(skip(f, "no auth_commands declared"));
    } else {
      const r = execBin(bin, ["auth", "status"]);
      if (r.code === 0) {
        results.push(pass(f, "auth status exits 0"));
      } else {
        const r2 = execBin(bin, ["auth"]);
        if (r2.code === 0) {
          results.push(pass(f, "auth exits 0"));
        } else {
          results.push(fail(f, "auth command failed",
            "Implement auth status/auth commands"));
        }
      }
    }
  }

  return results;
}

// --- Public API ---

/** Run static checks against a toolspec YAML file. */
export function runStatic(toolspecPath: string): CheckResult[] {
  const spec = loadSpec(toolspecPath);
  return runStaticChecks(spec);
}

/** Run runtime checks against a binary + toolspec. */
export function runRuntime(
  binaryPath: string,
  toolspecPath: string,
): CheckResult[] {
  const spec = loadSpec(toolspecPath);
  return runRuntimeChecks(binaryPath, spec);
}

/** Run both static + runtime checks. If binaryPath is empty,
 *  only static checks run. */
export function run(
  binaryPath: string,
  toolspecPath: string,
): Report {
  const spec = loadSpec(toolspecPath);
  let results = runStaticChecks(spec);

  if (binaryPath) {
    const rtResults = runRuntimeChecks(binaryPath, spec);
    results = mergeResults(results, rtResults);
  }

  const score = results.filter((r) => r.status === "pass").length;
  return {
    binary: binaryPath,
    toolspec: toolspecPath,
    results,
    score,
    total: 12,
  };
}

function mergeResults(
  staticR: CheckResult[],
  runtime: CheckResult[],
): CheckResult[] {
  const byFactor = new Map<number, CheckResult>();
  for (const r of staticR) byFactor.set(r.factor, r);
  for (const r of runtime) {
    const existing = byFactor.get(r.factor);
    if (!existing || existing.status === "skip") {
      byFactor.set(r.factor, r);
    }
  }

  const out: CheckResult[] = [];
  for (
    let f = Factor.SelfDescribing;
    f <= Factor.AuthLifecycle;
    f++
  ) {
    const r = byFactor.get(f);
    if (r) out.push(r);
  }
  return out;
}

/** Render report as text or JSON string. */
export function formatReport(
  r: Report,
  format: string = "text",
): string {
  if (format === "json") {
    return JSON.stringify(r, null, 2) + "\n";
  }
  return formatText(r);
}

function statusIcon(s: string): string {
  switch (s) {
    case "pass": return "PASS";
    case "fail": return "FAIL";
    case "warn": return "WARN";
    case "skip": return "SKIP";
    default: return "????";
  }
}

function formatText(r: Report): string {
  const lines: string[] = [
    "",
    "  12-Factor AI CLI Compliance Report",
    "  ══════════════════════════════════",
  ];
  if (r.binary) lines.push(`  Binary   : ${r.binary}`);
  if (r.toolspec) lines.push(`  Toolspec : ${r.toolspec}`);
  lines.push("");

  for (const cr of r.results) {
    const icon = statusIcon(cr.status);
    const fNum = String(cr.factor).padStart(2, " ");
    lines.push(
      `  ${icon}  F${fNum} ${cr.name.padEnd(20)} ` +
      `${cr.details ?? ""}`,
    );
    if (cr.suggestion) {
      lines.push(`       └─ ${cr.suggestion}`);
    }
  }

  lines.push("");
  lines.push(`  Score: ${r.score}/${r.total} factors passing`);
  lines.push("");
  return lines.join("\n");
}
