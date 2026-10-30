/**
 * Port of Go's `toolspec.Manifest` and `BuildManifest`
 * (go/ai/toolspec/spec.go, go/ai/toolspec/cli/spec_command.go): the
 * flat, snake_case capability manifest a `<tool> spec` command emits,
 * projected from a commander tree carrying kit/* annotations.
 *
 * Byte parity with Go is pinned by
 * sdk/tests/cross-lang/fixtures/toolspec-manifest*.json.
 */

import type {
  Command as CommanderCommand,
  Option as CommanderOption,
} from "commander";
import {
  annotationsOf,
  encodeGoJSON,
  isBuiltin,
  isHidden,
  isRunnable,
  pflagTypes,
  splitCSV,
  takesNoValue,
  walkCommands,
  type Annotations,
} from "./toolspec-internal.js";

/** Env var a harness sets to request a manifest schema version. */
export const SCHEMA_VERSION_ENV = "KIT_TOOLSPEC_SCHEMA";
/** Newest manifest schema version kit emits. */
export const LATEST_SCHEMA_VERSION = "1.1";
const KNOWN_SCHEMA_VERSIONS = ["1.0", LATEST_SCHEMA_VERSION];

/** Value side_effect / idempotent carry when nothing was declared. */
export const SIDE_EFFECT_UNKNOWN = "unknown";
export const IDEMPOTENT_UNKNOWN = "unknown";

export interface ManifestArg {
  name: string;
  required?: boolean;
}

export interface ManifestFlag {
  name: string;
  short?: string;
  type?: string;
  description?: string;
  default?: string;
  since_version?: string;
}

export interface ManifestExample {
  title: string;
  command: string;
  output?: string;
}

export interface ManifestNextStep {
  when?: string;
  suggest: string;
  reason?: string;
}

/** One invocable command. Field order is the Go struct order. */
export interface ManifestCommand {
  path: string[];
  short: string;
  side_effect: string;
  idempotent: string;
  args?: ManifestArg[];
  flags?: ManifestFlag[];
  exit_codes?: string[];
  deprecated?: boolean;
  deprecated_since?: string;
  removal_target?: string;
  since_version?: string;
  long?: string;
  retryable: boolean;
  /** Raw JSON Schema text, embedded verbatim (Go RawJSON). */
  output_schema?: string;
  output_schema_version?: string;
  examples?: ManifestExample[];
  next_steps?: ManifestNextStep[];
  top_level_verb?: boolean;
  hierarchical?: boolean;
  passthrough?: boolean;
  destructive_token_required?: boolean;
  dry_run_supported: boolean;
  dry_run_rationale?: string;
  reserved?: boolean;
  hidden?: boolean;
}

export interface Manifest {
  tool: string;
  version: string;
  schema_version: string;
  commands: ManifestCommand[];
}

export interface BuildManifestOptions {
  /** Include deprecated commands (Go --include-deprecated). */
  includeDeprecated?: boolean;
  /** Depth-1 verbs the tool reserves (Go Root.IsReserved). */
  reserved?: Iterable<string>;
  /**
   * The command being executed, if any. It gets the -h/--help flag
   * cobra adds to the executing command, as a live Go run lists.
   */
  invokedCommand?: CommanderCommand;
}

// --- Schema negotiation ---

function parseSchemaVersion(s: string): [number, number] | undefined {
  const t = s.trim();
  const dot = t.indexOf(".");
  if (dot < 0) return undefined;
  const parts = [t.slice(0, dot), t.slice(dot + 1)];
  const out: number[] = [];
  for (const p of parts) {
    if (!/^[0-9]+$/.test(p)) return undefined;
    const n = Number(p);
    if (!Number.isSafeInteger(n)) return undefined;
    out.push(n);
  }
  return [out[0], out[1]];
}

function after(a: [number, number], b: [number, number]): boolean {
  return a[0] !== b[0] ? a[0] > b[0] : a[1] > b[1];
}

/**
 * Resolve the emitted schema_version (Go NegotiateSchemaVersion):
 * declared is a floor; a well-formed request raises it to the highest
 * known version not above the request; nothing is ever refused.
 */
export function negotiateSchemaVersion(
  declared: string,
  requested: string,
): string {
  const floor = parseSchemaVersion(declared);
  if (!floor) return declared;
  const ceiling = parseSchemaVersion(requested);
  if (!ceiling) return declared;
  let resolved = declared;
  let best = floor;
  for (const v of KNOWN_SCHEMA_VERSIONS) {
    const known = parseSchemaVersion(v)!;
    if (after(known, best) && !after(known, ceiling)) {
      resolved = v;
      best = known;
    }
  }
  return resolved;
}

// --- Annotation reading (Go cmdreflect / cmdmeta) ---

function flagSince(ann: Annotations): Map<string, string> {
  const out = new Map<string, string>();
  for (const entry of (ann["kit/flag-since"] ?? "").split(",")) {
    const e = entry.trim();
    const eq = e.indexOf("=");
    if (eq < 0) continue;
    out.set(e.slice(0, eq).trim(), e.slice(eq + 1).trim());
  }
  return out;
}

/** Go's json decoding matches keys exactly first, then case-insensitively. */
function fieldOf(rec: Record<string, unknown>, name: string): unknown {
  if (name in rec) return rec[name];
  const k = Object.keys(rec).find((x) => x.toLowerCase() === name);
  return k === undefined ? undefined : rec[k];
}

/** Decode a JSON list annotation; absent or malformed yields undefined. */
function decodeList<T>(
  raw: string | undefined,
  fields: { name: string; required: boolean }[],
): T[] | undefined {
  if (!raw) return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return undefined;
  }
  if (parsed === null) return [];
  if (!Array.isArray(parsed)) return undefined;
  const out: T[] = [];
  for (const item of parsed) {
    if (item === null) {
      out.push(Object.fromEntries(fields.filter((f) => f.required).map((f) => [f.name, ""])) as T);
      continue;
    }
    if (typeof item !== "object" || Array.isArray(item)) return undefined;
    const rec: Record<string, string> = {};
    for (const f of fields) {
      const v = fieldOf(item as Record<string, unknown>, f.name);
      if (v === undefined || v === null) {
        if (f.required) rec[f.name] = "";
        continue;
      }
      if (typeof v !== "string") return undefined;
      if (v !== "" || f.required) rec[f.name] = v;
    }
    out.push(rec as T);
  }
  return out;
}

const DRY_RUN_SIDE_EFFECTS = new Set([
  "write",
  "write-local",
  "write-shared",
  "destructive",
  "destructive-local",
  "destructive-shared",
]);

function dryRunSupported(ann: Annotations): boolean {
  switch (ann["kit/dry-run"]) {
    case "opted-out":
      return false;
    case "supported":
      return true;
  }
  return DRY_RUN_SIDE_EFFECTS.has(ann["kit/side-effect"] ?? "");
}

/**
 * Map a commander option onto pflag's Type() and DefValue spelling,
 * which is what the Go manifest carries.
 */
function flagTypeAndDefault(opt: CommanderOption): [string, string] {
  const def = opt.defaultValue;
  const declared = pflagTypes.get(opt);
  if (declared) return [declared, def === undefined ? "" : String(def)];
  if (opt.variadic) {
    const items = def === undefined ? [] : Array.isArray(def) ? def : [def];
    return ["stringSlice", `[${items.map(String).join(",")}]`];
  }
  // A negated --no-x is its own pflag bool, unset (false) by default;
  // commander's default for it belongs to the x attribute, not the flag.
  if (opt.negate) return ["bool", "false"];
  if (takesNoValue(opt)) return ["bool", def === undefined ? "false" : String(def)];
  return ["string", def === undefined ? "" : String(def)];
}

function manifestFlags(
  cmd: CommanderCommand,
  ann: Annotations,
  withHelp: boolean,
): ManifestFlag[] | undefined {
  const since = flagSince(ann);
  const visible = (cmd.options as readonly CommanderOption[]).filter((o) => !o.hidden);
  const flags: ManifestFlag[] = [];
  for (const o of visible) {
    const name = (o.long ?? o.short ?? "").replace(/^-+/, "");
    if (name === "") continue;
    const [type, def] = flagTypeAndDefault(o);
    const f: ManifestFlag = { name };
    if (o.long && o.short) f.short = o.short.replace(/^-/, "");
    f.type = type;
    if (o.description) f.description = o.description;
    if (def !== "") f.default = def;
    const sv = since.get(name);
    if (sv) f.since_version = sv;
    flags.push(f);
  }
  // cobra adds -h/--help to the command it executes (and only that
  // one) before its RunE runs, so a live Go spec lists it there.
  if (withHelp && !flags.some((f) => f.name === "help")) {
    const help: ManifestFlag = { name: "help" };
    if (!flags.some((f) => f.short === "h")) help.short = "h";
    help.type = "bool";
    help.description = `help for ${cmd.name()}`;
    help.default = "false";
    flags.push(help);
  }
  flags.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  return flags.length > 0 ? flags : undefined;
}

function manifestArgs(cmd: CommanderCommand, ann: Annotations): ManifestArg[] | undefined {
  const declared = splitCSV(ann["kit/args"]);
  const args: ManifestArg[] = declared
    ? declared.map((n) =>
        n.endsWith("?") ? { name: n.slice(0, -1) } : { name: n, required: true },
      )
    : cmd.registeredArguments.map((a) =>
        a.required ? { name: a.name(), required: true } : { name: a.name() },
      );
  return args.length > 0 ? args : undefined;
}

function manifestCommand(
  cmd: CommanderCommand,
  path: string[],
  hidden: boolean,
  reserved: boolean,
  invoked: boolean,
): ManifestCommand {
  const ann = annotationsOf(cmd);
  // commander's summary() is the one-liner when set; description()
  // then holds the long text (cobra Short / Long).
  const summary = cmd.summary();
  const short = summary || cmd.description();
  const long = summary ? cmd.description() : "";

  // Keys are inserted in Go struct order; the encoder keeps it.
  const out = {
    path,
    short,
    side_effect: ann["kit/side-effect"] || SIDE_EFFECT_UNKNOWN,
    idempotent: ann["kit/idempotent"] || IDEMPOTENT_UNKNOWN,
  } as ManifestCommand;

  const args = manifestArgs(cmd, ann);
  if (args) out.args = args;
  const flags = manifestFlags(cmd, ann, invoked);
  if (flags) out.flags = flags;
  const exitCodes = splitCSV(ann["kit/exit-codes"]);
  if (exitCodes) out.exit_codes = exitCodes;

  const deprecatedSince = ann["kit/deprecated-since"] ?? "";
  const removalTarget = ann["kit/removal-target"] ?? "";
  if (deprecatedSince || removalTarget) out.deprecated = true;
  if (deprecatedSince) out.deprecated_since = deprecatedSince;
  if (removalTarget) out.removal_target = removalTarget;
  if (ann["kit/since"]) out.since_version = ann["kit/since"];

  if (long) out.long = long;
  out.retryable = ann["kit/retryable"] === "true";
  const schema = ann["kit/output-schema"];
  if (schema) {
    out.output_schema = schema;
    if (ann["kit/output-schema-version"]) {
      out.output_schema_version = ann["kit/output-schema-version"];
    }
  }
  const examples = decodeList<ManifestExample>(ann["kit/examples"], [
    { name: "title", required: true },
    { name: "command", required: true },
    { name: "output", required: false },
  ]);
  if (examples && examples.length > 0) out.examples = examples;
  const nextSteps = decodeList<ManifestNextStep>(ann["kit/next-steps"], [
    { name: "when", required: false },
    { name: "suggest", required: true },
    { name: "reason", required: false },
  ]);
  if (nextSteps && nextSteps.length > 0) out.next_steps = nextSteps;

  if (ann["kit/top-level-verb"] === "true") out.top_level_verb = true;
  if (ann["kit/hierarchical"] === "true") out.hierarchical = true;
  if (ann["kit/passthrough"] === "true") out.passthrough = true;
  const token = ann["kit/destructive-token"];
  if (token === "required" || token === "true") out.destructive_token_required = true;
  out.dry_run_supported = dryRunSupported(ann);
  if (ann["kit/dry-run-rationale"]) out.dry_run_rationale = ann["kit/dry-run-rationale"];
  if (reserved) out.reserved = true;
  if (hidden) out.hidden = true;
  return out;
}

/**
 * Project a commander tree into the Go-shaped Manifest (Go
 * BuildManifest). Lists every runnable command except framework
 * builtins (help, completion, man) and, unless includeDeprecated,
 * deprecated ones; hidden and reserved commands stay listed and say
 * so. Children are visited in name order and flags sorted by name,
 * as cobra and pflag do by default.
 */
export function buildManifest(
  program: CommanderCommand,
  schemaVersion: string,
  opts: BuildManifestOptions = {},
): Manifest {
  const m: Manifest = {
    tool: program.name(),
    version: program.version() ?? "",
    schema_version: schemaVersion,
    commands: [],
  };
  walkCommands(program, opts.reserved, (cmd, parent, path, reserved) => {
    const ann = annotationsOf(cmd);
    const deprecated = !!(ann["kit/deprecated-since"] || ann["kit/removal-target"]);
    if (!isRunnable(cmd) || isBuiltin(cmd, parent)) return;
    if (deprecated && !opts.includeDeprecated) return;
    m.commands.push(
      manifestCommand(cmd, path, isHidden(cmd, parent), reserved, cmd === opts.invokedCommand),
    );
  });
  return m;
}

/**
 * Render a Manifest exactly as Go's `output.Render(w, "json", m)`:
 * two-space indent, HTML-escaped strings, trailing newline, fields
 * in Go struct order with omitempty applied.
 */
export function renderManifestJSON(m: Manifest): string {
  return encodeGoJSON(m, 0, new Set(["output_schema"])) + "\n";
}
