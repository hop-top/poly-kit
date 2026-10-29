import * as fs from "fs";
import * as yaml from "js-yaml";
import type {
  Command as CommanderCommand,
  Option as CommanderOption,
} from "commander";

// --- Types ---

export type SafetyLevel = "safe" | "caution" | "dangerous";

export interface Contract {
  idempotent: boolean;
  sideEffects?: string[];
  retryable: boolean;
  preConditions?: string[];
}

export interface Safety {
  level: SafetyLevel;
  requiresConfirmation?: boolean;
  permissions?: string[];
}

export interface OutputSchema {
  format?: string;
  fields?: string[];
  example?: string;
}

export interface StateIntrospection {
  configCommands?: string[];
  envVars?: string[];
  authCommands?: string[];
}

export interface Provenance {
  source?: string;
  retrievedAt?: string;
  confidence?: number;
}

export interface Intent {
  domain?: string;
  category?: string;
  tags?: string[];
}

export interface Flag {
  name: string;
  short?: string;
  type?: string;
  description?: string;
  /** Flag must be supplied (commander `makeOptionMandatory`). */
  required?: boolean;
  deprecated?: boolean;
  replacedBy?: string;
}

/** A declared positional argument (mirrors Go toolspec.Arg). */
export interface Arg {
  name: string;
  /** False/absent for an optional argument. */
  required?: boolean;
}

export interface Command {
  name: string;
  /** One-line summary of the command. */
  short?: string;
  aliases?: string[];
  flags?: Flag[];
  /** Declared positional arguments, in order. */
  args?: Arg[];
  /** Command takes positional arguments it does not declare. */
  undeclaredArgs?: boolean;
  /** Exit-code symbols the command may produce (kit/exit-codes). */
  exitCodes?: string[];
  children?: Command[];
  contract?: Contract;
  safety?: Safety;
  previewModes?: string[];
  outputSchema?: OutputSchema;
  deprecated?: boolean;
  deprecatedSince?: string;
  replacedBy?: string;
  intent?: Intent;
  suggestedNext?: string[];
}

export interface ErrorPattern {
  pattern: string;
  fix: string;
  source?: string;
  cause?: string;
  fixes?: string[];
  confidence?: number;
  provenance?: Provenance;
}

export interface Workflow {
  name: string;
  steps: string[];
  after?: Record<string, string[]>;
  provenance?: Provenance;
}

export interface ToolSpec {
  name: string;
  schemaVersion: string;
  commands: Command[];
  flags?: Flag[];
  errorPatterns?: ErrorPattern[];
  workflows?: Workflow[];
  stateIntrospection?: StateIntrospection;
}

// --- YAML mapping helpers ---

// snake_case YAML -> camelCase TS
function mapFlag(raw: any): Flag {
  return {
    name: raw.name ?? "",
    short: raw.short,
    type: raw.type,
    description: raw.description,
    required: raw.required,
    deprecated: raw.deprecated,
    replacedBy: raw.replaced_by,
  };
}

function mapArg(raw: any): Arg {
  return { name: raw.name ?? "", required: raw.required };
}

function mapProvenance(raw: any): Provenance | undefined {
  if (!raw) return undefined;
  return {
    source: raw.source,
    retrievedAt: raw.retrieved_at,
    confidence: raw.confidence,
  };
}

function mapContract(raw: any): Contract | undefined {
  if (!raw) return undefined;
  return {
    idempotent: raw.idempotent ?? false,
    sideEffects: raw.side_effects,
    retryable: raw.retryable ?? false,
    preConditions: raw.pre_conditions,
  };
}

function mapSafety(raw: any): Safety | undefined {
  if (!raw) return undefined;
  return {
    level: raw.level,
    requiresConfirmation: raw.requires_confirmation,
    permissions: raw.permissions,
  };
}

function mapOutputSchema(raw: any): OutputSchema | undefined {
  if (!raw) return undefined;
  return {
    format: raw.format,
    fields: raw.fields,
    example: raw.example,
  };
}

function mapIntent(raw: any): Intent | undefined {
  if (!raw) return undefined;
  return {
    domain: raw.domain,
    category: raw.category,
    tags: raw.tags,
  };
}

function mapCommand(raw: any): Command {
  return {
    name: raw.name ?? "",
    short: raw.short,
    aliases: raw.aliases,
    flags: raw.flags?.map(mapFlag),
    args: raw.args?.map(mapArg),
    undeclaredArgs: raw.undeclared_args,
    exitCodes: raw.exit_codes,
    children: raw.children?.map(mapCommand),
    contract: mapContract(raw.contract),
    safety: mapSafety(raw.safety),
    previewModes: raw.preview_modes,
    outputSchema: mapOutputSchema(raw.output_schema),
    deprecated: raw.deprecated,
    deprecatedSince: raw.deprecated_since,
    replacedBy: raw.replaced_by,
    intent: mapIntent(raw.intent),
    suggestedNext: raw.suggested_next,
  };
}

function mapStateIntrospection(
  raw: any,
): StateIntrospection | undefined {
  if (!raw) return undefined;
  return {
    configCommands: raw.config_commands,
    envVars: raw.env_vars,
    authCommands: raw.auth_commands,
  };
}

function mapErrorPattern(raw: any): ErrorPattern {
  return {
    pattern: raw.pattern ?? "",
    fix: raw.fix ?? "",
    source: raw.source,
    cause: raw.cause,
    fixes: raw.fixes,
    confidence: raw.confidence,
    provenance: mapProvenance(raw.provenance),
  };
}

function mapWorkflow(raw: any): Workflow {
  return {
    name: raw.name ?? "",
    steps: raw.steps ?? [],
    after: raw.after,
    provenance: mapProvenance(raw.provenance),
  };
}

// --- Public API ---

/** Parse a YAML toolspec file into a ToolSpec object. */
export function loadToolSpec(yamlPath: string): ToolSpec {
  const content = fs.readFileSync(yamlPath, "utf-8");
  const raw = yaml.load(content) as any;

  return {
    name: raw.name ?? "",
    schemaVersion: raw.schema_version ?? "",
    commands: (raw.commands ?? []).map(mapCommand),
    flags: raw.flags?.map(mapFlag),
    errorPatterns: raw.error_patterns?.map(mapErrorPattern),
    workflows: raw.workflows?.map(mapWorkflow),
    stateIntrospection: mapStateIntrospection(
      raw.state_introspection,
    ),
  };
}

const VALID_SAFETY_LEVELS: Set<string> = new Set([
  "safe",
  "caution",
  "dangerous",
]);

/** Validate a ToolSpec, returning a list of error strings. */
export function validateToolSpec(spec: ToolSpec): string[] {
  const errors: string[] = [];

  if (!spec.name) errors.push("name is required");
  if (!spec.schemaVersion) errors.push("schemaVersion is required");
  if (!spec.commands || spec.commands.length === 0) {
    errors.push("at least one command is required");
  }

  const validateCmd = (cmd: Command, path: string) => {
    if (!cmd.name) {
      errors.push(`${path}: command name is required`);
    }
    if (cmd.safety && !VALID_SAFETY_LEVELS.has(cmd.safety.level)) {
      errors.push(
        `${path}: invalid safety level "${cmd.safety.level}"` +
          ` (must be safe|caution|dangerous)`,
      );
    }
    if (cmd.exitCodes?.some((c) => typeof c !== "string" || c.trim() === "")) {
      errors.push(`${path}: exit code symbols must be non-empty strings`);
    }
    if (cmd.children) {
      for (const child of cmd.children) {
        validateCmd(child, `${path}/${child.name || "<unnamed>"}`);
      }
    }
  };

  for (const cmd of spec.commands ?? []) {
    validateCmd(cmd, cmd.name || "<unnamed>");
  }

  return errors;
}

/** BFS search for a command by name (mirrors Go FindCommand). */
export function findCommand(
  spec: ToolSpec,
  name: string,
): Command | undefined {
  const queue: Command[] = [...spec.commands];
  while (queue.length > 0) {
    const c = queue.shift()!;
    if (c.name === name) return c;
    if (c.children) queue.push(...c.children);
  }
  return undefined;
}

export * from "./toolspec-manifest.js";

// --- Build from a commander tree ---

/**
 * Per-command fields merged over what the commander tree yields.
 * Structural fields (name, aliases, flags, args, children) always
 * come from the tree.
 */
export type CommandAnnotation = Omit<
  Partial<Command>,
  "name" | "aliases" | "flags" | "args" | "children"
>;

export interface FromCommandOptions {
  /** Annotations keyed by space-joined path below the root, e.g. "mission list". */
  annotations?: Record<string, CommandAnnotation>;
  /** Defaults to "1", the version the YAML toolspecs declare. */
  schemaVersion?: string;
}

/**
 * Build a ToolSpec from a constructed commander tree, so CLIs with
 * dynamically registered subcommands need no hand-synced YAML.
 * Mirrors Go's WalkCobra projection: hidden commands and options are
 * omitted, flag names are undashed, root options become spec.flags.
 */
export function toolSpecFromCommand(
  program: CommanderCommand,
  opts: FromCommandOptions = {},
): ToolSpec {
  const annotations = opts.annotations ?? {};
  const spec: ToolSpec = {
    name: program.name(),
    schemaVersion: opts.schemaVersion ?? "1",
    commands: projectChildren(program, [], annotations),
  };
  const flags = projectFlags(program);
  if (flags) spec.flags = flags;
  return spec;
}

function projectChildren(
  cmd: CommanderCommand,
  path: string[],
  annotations: Record<string, CommandAnnotation>,
): Command[] {
  // visibleCommands drops hidden subcommands but appends commander's
  // implicit help placeholder, which is not in cmd.commands.
  const visible = cmd.createHelp().visibleCommands(cmd);
  return visible
    .filter((c) => cmd.commands.includes(c))
    .map((c) => projectCommand(c, [...path, c.name()], annotations));
}

function projectCommand(
  cmd: CommanderCommand,
  path: string[],
  annotations: Record<string, CommandAnnotation>,
): Command {
  const out: Command = { name: cmd.name() };
  const short = cmd.summary() || cmd.description();
  if (short) out.short = short;
  const aliases = cmd.aliases();
  if (aliases.length > 0) out.aliases = [...aliases];
  const flags = projectFlags(cmd);
  if (flags) out.flags = flags;
  const args = cmd.registeredArguments.map((a): Arg =>
    a.required ? { name: a.name(), required: true } : { name: a.name() },
  );
  if (args.length > 0) out.args = args;
  const exitCodes = splitCSV(kitAnnotations(cmd)["kit/exit-codes"]);
  if (exitCodes) out.exitCodes = exitCodes;
  const children = projectChildren(cmd, path, annotations);
  if (children.length > 0) out.children = children;

  const ann = annotations[path.join(" ")];
  if (ann) {
    for (const [k, v] of Object.entries(ann)) {
      if (v !== undefined) (out as any)[k] = v;
    }
  }
  return out;
}

/**
 * kit/* annotations on a commander command. commander has no
 * annotations concept; they ride an optional `annotations` property,
 * the same convention the MCP bridge reads.
 */
function kitAnnotations(cmd: CommanderCommand): Record<string, string> {
  return (cmd as { annotations?: Record<string, string> }).annotations ?? {};
}

/** Comma-separated annotation value, trimmed, empties dropped. */
function splitCSV(s: string | undefined): string[] | undefined {
  if (!s) return undefined;
  const out = s.split(",").map((p) => p.trim()).filter((p) => p !== "");
  return out.length > 0 ? out : undefined;
}

function projectFlags(cmd: CommanderCommand): Flag[] | undefined {
  const out: Flag[] = [];
  for (const opt of cmd.options as readonly CommanderOption[]) {
    if (opt.hidden) continue;
    const name = (opt.long ?? opt.short ?? "").replace(/^-+/, "");
    if (!name) continue;
    const flag: Flag = { name };
    if (opt.long && opt.short) flag.short = opt.short.replace(/^-/, "");
    flag.type = flagType(opt);
    if (opt.description) flag.description = opt.description;
    if (opt.mandatory) flag.required = true;
    out.push(flag);
  }
  return out.length > 0 ? out : undefined;
}

/** pflag-style type names, matching the Go walker's output. */
function flagType(opt: CommanderOption): string {
  if (opt.isBoolean()) return "bool";
  if (opt.variadic) return "stringSlice";
  return "string";
}
