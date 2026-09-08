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
  ConsentingTelemetry = 13,
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
  [Factor.ConsentingTelemetry]: "Consenting Telemetry",
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
  telemetry?: TelemetryYAML;
}

/** The toolspec `telemetry:` block, subject to the F13 check. */
interface TelemetryYAML {
  enabled?: boolean;
  categories?: string[];
  sinks?: string[];
  consent_command?: string;
  consent_subcommands?: string[];
  kill_switch_envs?: string[];
  prompt_version?: string;
  redact_rules?: string;
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

/** True iff the toolspec declares a telemetry block with
 *  enabled: true. Non-opt-in specs skip F13 entirely. */
function telemetryOptedIn(spec: SpecYAML): boolean {
  return spec?.telemetry?.enabled === true;
}

/** Subcommands an opt-in binary MUST expose under its consent
 *  command (typically `<bin> telemetry`). */
const telemetryConsentSubcommandsCanonical = [
  "disable",
  "enable",
  "inspect",
  "reset",
  "status",
];

/** Matches `<UPPERCASE_APP>_TELEMETRY_MODE`. The kit literal
 *  `KIT_TELEMETRY_MODE` matches by construction, as does any
 *  app-prefixed form like `SPACED_TELEMETRY_MODE`. */
const telemetryModeEnvShape = /^[A-Z][A-Z0-9_]*_TELEMETRY_MODE$/;

function hasTelemetryModeEnv(envs: string[]): boolean {
  return envs.some((e) => telemetryModeEnvShape.test(e));
}

/** True when the space-separated path (e.g. "telemetry status")
 *  exists in the command tree. */
function commandPathExists(
  cmds: CmdYAML[],
  path: string[],
): boolean {
  if (path.length === 0) return false;
  for (const c of cmds) {
    if (c.name !== path[0]) continue;
    if (path.length === 1) return true;
    return commandPathExists(c.children ?? [], path.slice(1));
  }
  return false;
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

// warn is a partial pass: the obligation is met in shape but not in
// substance. It carries a suggestion like fail, because the point of a warn
// is that there is something to do about it.
function warn(
  f: Factor,
  details: string,
  suggestion: string,
): CheckResult {
  return {
    factor: f,
    name: factorName(f),
    status: "warn",
    details,
    suggestion,
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

/**
 * F13 static check. When the toolspec opts into telemetry
 * (`telemetry.enabled: true`), asserts the block is well-formed
 * across seven sub-conditions:
 *
 *  1. categories non-empty
 *  2. consent_subcommands contains the canonical set
 *     {disable, enable, inspect, reset, status}
 *  3. kill_switch_envs contains DO_NOT_TRACK
 *  4. kill_switch_envs contains a `<APP>_TELEMETRY_MODE` entry
 *  5. prompt_version non-empty (canonical field name is locked —
 *     aliases like `consent_version` are not read)
 *  6. redact_rules non-empty
 *  7. every declared consent_subcommand maps to a real command
 *     in the commands tree
 *
 * Failures aggregate into a single result: one row per factor.
 */
function checkConsentingTelemetry(spec: SpecYAML): CheckResult {
  const f = Factor.ConsentingTelemetry;
  if (!telemetryOptedIn(spec)) {
    return skip(f, "binary does not opt into telemetry");
  }

  const t = spec.telemetry ?? {};
  const failures: string[] = [];

  if (!t.categories || t.categories.length === 0) {
    failures.push("telemetry.categories is empty");
  }

  const subs = t.consent_subcommands ?? [];
  const missingSubs = telemetryConsentSubcommandsCanonical.filter(
    (req) => !subs.includes(req),
  );
  if (missingSubs.length > 0) {
    failures.push(
      "telemetry.consent_subcommands missing required entries: " +
        missingSubs.join(", "),
    );
  }

  const envs = t.kill_switch_envs ?? [];
  if (!envs.includes("DO_NOT_TRACK")) {
    failures.push(
      "telemetry.kill_switch_envs missing DO_NOT_TRACK",
    );
  }
  if (!hasTelemetryModeEnv(envs)) {
    failures.push(
      "telemetry.kill_switch_envs missing a <APP>_TELEMETRY_MODE " +
        "entry (e.g. KIT_TELEMETRY_MODE or SPACED_TELEMETRY_MODE)",
    );
  }

  if (!(t.prompt_version ?? "").trim()) {
    failures.push(
      "telemetry.prompt_version is empty (canonical field name " +
        "is `prompt_version`; aliases like `consent_version` " +
        "are not accepted)",
    );
  }

  if (!(t.redact_rules ?? "").trim()) {
    failures.push("telemetry.redact_rules is empty");
  }

  // The consent_command schema is `<bin> telemetry [...]`; the
  // leading `<bin>` is the binary name, not a top-level command,
  // so strip it. Fall back to `telemetry <sub>` when the value is
  // empty or a bare binary name.
  const consentTokens = (t.consent_command ?? "").split(/\s+/)
    .filter((tok) => tok.length > 0);
  const consentPath =
    consentTokens.length > 1 ? consentTokens.slice(1) : ["telemetry"];
  const unmappedSubs: string[] = [];
  for (const sub of subs) {
    const full = [...consentPath, sub];
    if (!commandPathExists(spec.commands ?? [], full)) {
      unmappedSubs.push(full.join(" "));
    }
  }
  if (unmappedSubs.length > 0) {
    failures.push(
      "telemetry.consent_subcommands declared but not in " +
        "commands tree: " + unmappedSubs.join(", "),
    );
  }

  if (failures.length === 0) {
    return pass(f,
      "telemetry block well-formed; all consent subcommands declared");
  }

  return fail(f, failures.join("; "),
    "Fix the telemetry block: ensure categories, " +
      "consent_subcommands {disable, enable, inspect, reset, " +
      "status}, kill_switch_envs [DO_NOT_TRACK, <APP>_TELEMETRY_MODE], " +
      "prompt_version, redact_rules are set, and that each " +
      "consent_subcommand maps to a command in the commands tree.");
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

// execBinEnv is execBin with extra environment entries, for probes whose
// obligation is stated against a policy the tool reads from the environment.
// The inherited environment is kept: a binary needing PATH or HOME to start
// at all must still start.
function execBinEnv(
  bin: string,
  args: string[],
  env: Record<string, string>,
): { stdout: string; stderr: string; code: number } {
  try {
    const stdout = execFileSync(bin, args, {
      timeout: 10000,
      stdio: ["pipe", "pipe", "pipe"],
      env: { ...process.env, ...env },
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

// autocorrectEnvVar is the environment name kit reads for its opt-in flag
// autocorrect policy. Set by the probe below to elicit each mode from a tool
// that supports it; a tool that does not simply ignores it, which is what
// makes the probe skip rather than fail.
const autocorrectEnvVar = "KIT_AUTOCORRECT";

// correctedFromField is the envelope key a tool MUST populate when it
// rewrote a flag for the caller.
const correctedFromField = "corrected_from";

// contractsErrorsEnvelope is the original F4 obligation: a bogus flag exits
// non-zero and comes back as a structured envelope carrying a fix.
function contractsErrorsEnvelope(bin: string): CheckResult {
  const f = Factor.ContractsErrors;
  const r = execBin(bin, ["--format", "json", "--bogus-arg-xyzzy"]);
  if (r.code === 0) {
    return fail(f, "bogus arg didn't cause error exit",
      "Unknown flags should cause non-zero exit");
  }
  if (isValidJSON(r.stderr)) {
    let obj: unknown;
    try {
      obj = JSON.parse(r.stderr.trim());
    } catch {
      obj = undefined;
    }
    if (obj && typeof obj === "object" && "code" in obj) {
      if (errorCarriesFix(obj as Record<string, unknown>)) {
        return pass(f, "structured error with code field and recovery guidance");
      }
      return warn(f, "structured error carries no recovery guidance",
        "Populate suggested_fix (or alternatives) with a concrete " +
          "correction so the caller does not need a --help round trip");
    }
  }
  return warn(f, "error output is not structured JSON",
    "Return JSON errors with a 'code' field on stderr");
}

// contractsErrorsAutocorrect checks the per-mode semantics of an opt-in flag
// autocorrect, when the tool has one.
//
// The obligations differ by mode, and each is the thing that mode's existence
// puts at risk:
//
//   - off (and the default, which is off): a mistyped flag exits non-zero.
//     This is the contract every other caller depends on, and a tool that
//     quietly corrects by default has broken it for every script that was
//     relying on the failure.
//   - read: IF a correction was applied — exit 0 on an invocation that should
//     have failed to parse — the envelope MUST carry corrected_from. A run
//     that silently becomes a different run is unauditable, and stderr prose
//     is not something a --format json consumer reads.
//
// Aimed at a READ command, not the root, because a correction is only ever
// legitimate on one: the side-effect gate is per-leaf, and a tool rewriting a
// flag on an unannotated root would be violating that gate rather than
// demonstrating the feature.
//
// The near-miss token is a one-edit typo of --format. `--formt` rather than
// `--forma`: the latter is a PREFIX of --format, --format-opt and
// --format-help, so it is ambiguous by construction and no compliant tool
// would ever correct it — a probe built on it would skip for every tool and
// measure nothing.
function contractsErrorsAutocorrect(bin: string, spec: SpecYAML): CheckResult {
  const f = Factor.ContractsErrors;
  const nearMiss = "--formt=json";

  const readCmd = findReadCommand(spec);
  if (!readCmd) {
    return skip(f, "no read command found to probe autocorrect on");
  }

  // Obligation 1: the default is suggest-only.
  if (execBin(bin, [readCmd, nearMiss]).code === 0) {
    return fail(f,
      "a mistyped flag exited 0 with no autocorrect policy set",
      "Keep autocorrect off by default: a bad flag must exit non-zero " +
        "unless the caller opted in");
  }

  // Obligation 2: explicit off behaves as the default does.
  if (execBinEnv(bin, [readCmd, nearMiss],
    { [autocorrectEnvVar]: "off" }).code === 0) {
    return fail(f,
      "a mistyped flag exited 0 under an explicit off policy",
      "Honor the off value: it must not be read as unset");
  }

  // Obligation 3: under read, a correction that WAS applied is declared.
  const r = execBinEnv(bin, [readCmd, "--format", "json", nearMiss],
    { [autocorrectEnvVar]: "read" });
  if (r.code !== 0) {
    return skip(f, "binary does not apply flag corrections under " +
      autocorrectEnvVar + "=read");
  }
  if (correctionDeclared(r.stderr) || correctionDeclared(r.stdout)) {
    return pass(f, "applied flag correction declares " + correctedFromField);
  }
  return fail(f,
    "a flag correction was applied but no " + correctedFromField +
      " was reported",
    "Emit " + correctedFromField + " in the structured envelope naming the " +
      "token the caller typed, so an agent and an audit log can both see " +
      "that the command that ran is not the command that was asked for");
}

// correctionDeclared reports whether s carries a corrected_from field.
//
// Tolerant of surrounding output on purpose: a tool may write the notice
// alongside logs, and a probe that demanded the stream be exactly one JSON
// document would be testing Factor 3's obligation, not this one. Each line
// is tried as its own document, then the raw text as a fallback for a YAML
// or plaintext rendering.
function correctionDeclared(s: string): boolean {
  for (const line of s.split("\n")) {
    const t = line.trim();
    if (!t.startsWith("{")) continue;
    try {
      const obj = JSON.parse(t);
      if (obj && typeof obj === "object") {
        const v = (obj as Record<string, unknown>)[correctedFromField];
        if (typeof v === "string" && v.trim() !== "") return true;
      }
    } catch {
      // Not a standalone document; the raw-text fallback below still
      // catches a pretty-printed one.
    }
  }
  if (/"corrected_from"\s*:\s*"[^"\s]/.test(s)) return true;
  return s.includes(correctedFromField + ":") ||
    s.includes("Corrected from:");
}

// aggregateContractsErrors folds the two F4 runtime sub-checks into one row,
// per the "one row per factor" model.
//
// Precedence matches the F13 aggregator with one addition it needs and F13
// does not: a warn survives. The envelope sub-check reports warn for a tool
// whose error is unstructured or fix-less, and collapsing that to pass
// because the autocorrect arm skipped would hide the finding on exactly the
// tools that have not adopted autocorrect — which is most of them.
function aggregateContractsErrors(...rs: CheckResult[]): CheckResult {
  const f = Factor.ContractsErrors;
  const failed: string[] = [];
  const skipped: string[] = [];
  let firstWarn: CheckResult | undefined;
  for (const r of rs) {
    // details is optional on the wire; a sub-check that set none
    // contributes nothing to the concatenation rather than "undefined".
    if (r.status === "fail") failed.push(r.details ?? "");
    else if (r.status === "skip") skipped.push(r.details ?? "");
    else if (r.status === "warn" && !firstWarn) firstWarn = r;
  }
  if (failed.length > 0) {
    return fail(f, failed.join("; "),
      "Address each failing sub-condition (structured error envelope; " +
        "autocorrect mode semantics)");
  }
  if (firstWarn) return firstWarn;
  if (skipped.length === rs.length) {
    return skip(f, [...new Set(skipped)].join("; "));
  }
  return pass(f, "structured error with recovery guidance; " +
    "autocorrect mode semantics honored");
}

// errorCarriesFix reports whether a decoded error envelope offers the caller
// a way forward.
//
// Either field satisfies it. A single unambiguous correction belongs in
// suggested_fix; an ambiguous one belongs in alternatives, and a tool that
// declines to guess between candidates is behaving correctly, not
// incompletely. Empty strings and empty lists do not count — a present but
// blank field is the same dead end as an absent one.
//
// A bare `--help` pointer does not count either, in EITHER field. "Run tool
// --help for usage" is precisely the round trip this factor exists to
// eliminate, and accepting it would let a tool pass "with recovery guidance"
// for offering none. A fix that names --help alongside something concrete
// still counts — the concrete part is the guidance.
function errorCarriesFix(obj: Record<string, unknown>): boolean {
  const fix = obj["suggested_fix"];
  if (typeof fix === "string" && isConcreteFix(fix)) return true;
  const alts = obj["alternatives"];
  if (!Array.isArray(alts)) return false;
  return alts.some((a) => typeof a === "string" && isConcreteFix(a));
}

// HELP_FRAMING is every word that exists only to frame a help invocation. A
// fix reduces to nothing once they are removed exactly when it is a pointer
// back at the help page.
const HELP_FRAMING = new Set([
  "--help", "-h", "help", "run", "see", "try", "use", "for", "usage",
  "the", "a", "an", "to", "and", "or", "then", "check", "consult", "with",
]);

// isConcreteFix reports whether s is recovery guidance rather than a pointer
// back at the help page. A bare command-path word ("tool", "sub") counts as
// framing too: "tool sub --help" names a path, not a fix, so a token must
// carry a flag dash or punctuation of its own to count as content.
function isConcreteFix(s: string): boolean {
  const words = s.toLowerCase().split(/[\s'"`,.;:()]+/).filter(Boolean);
  for (const w of words) {
    if (HELP_FRAMING.has(w)) continue;
    if (w.startsWith("-") || /[=<>[\]{}/|@]/.test(w)) return true;
  }
  return false;
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

  // F4: bogus arg returns a structured error carrying a fix
  //
  // Two separate obligations, checked in order of severity:
  //
  //  1. Non-zero exit. A rejected flag that exits 0 is the worst outcome —
  //     the caller cannot tell the invocation failed. Hard fail.
  //  2. A structured envelope that carries recovery guidance. An error whose
  //     only content is "unknown flag: --x" is a dead end: the caller has to
  //     spend a --help round trip to learn what it should have typed. That
  //     round trip is the cost this factor exists to eliminate, so a
  //     structured error WITHOUT a fix is only a partial pass.
  //
  // Both are stated against the SUGGEST-ONLY behavior, which is what this
  // probe elicits: an argument no real flag is close to, and no autocorrect
  // policy of its own. A tool invoked under an autocorrect policy is a
  // different measurement, made by the second sub-check below and folded
  // into the same row.
  results.push(aggregateContractsErrors(
    contractsErrorsEnvelope(bin),
    contractsErrorsAutocorrect(bin, spec),
  ));

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
  results.push(checkConsentingTelemetry(spec));

  if (binaryPath) {
    const rtResults = runRuntimeChecks(binaryPath, spec);
    results = mergeResults(results, rtResults);
  }

  // The denominator counts factors *eligible* to contribute. The 12
  // pre-F13 factors always are, so a non-opt-in binary still scores
  // N/12; only an opt-in binary adds F13 and scores N/13. Skips
  // inside the eligible set (e.g. runtime-only factors on a
  // static-only run) still count toward the denominator.
  const total = telemetryOptedIn(spec) ? 13 : 12;

  const score = results.filter((r) => r.status === "pass").length;
  return {
    binary: binaryPath,
    toolspec: toolspecPath,
    results,
    score,
    total,
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
    f <= Factor.ConsentingTelemetry;
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
