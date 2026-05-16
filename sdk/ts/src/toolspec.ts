import * as fs from "fs";
import * as yaml from "js-yaml";

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
  deprecated?: boolean;
  replacedBy?: string;
}

export interface Command {
  name: string;
  aliases?: string[];
  flags?: Flag[];
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
    deprecated: raw.deprecated,
    replacedBy: raw.replaced_by,
  };
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
    aliases: raw.aliases,
    flags: raw.flags?.map(mapFlag),
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
