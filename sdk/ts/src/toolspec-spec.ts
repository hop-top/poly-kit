/**
 * Port of Go's `<tool> spec` subcommand and `spec coverage`
 * (go/ai/toolspec/cli/spec_command.go, coverage.go,
 * coverage_command.go). Output bytes for json / kit-manifest are
 * pinned against Go by sdk/tests/cross-lang/fixtures/toolspec-spec*.json.
 */

import type { EventEmitter } from "events";
import { Command, Option } from "commander";
import * as yaml from "js-yaml";
import { EXIT_CONFLICT, EXIT_GENERIC } from "./output/error.js";
import { render as renderOutput, type Format } from "./output.js";
import {
  annotationsOf,
  encodeGoJSON,
  goFormat0,
  isBuiltin,
  isRunnable,
  pflagTypes,
  walkCommands,
} from "./toolspec-internal.js";
import {
  buildManifest,
  negotiateSchemaVersion,
  renderManifestJSON,
  SCHEMA_VERSION_ENV,
  type Manifest,
} from "./toolspec-manifest.js";
import {
  COVERAGE_REPORT_OUTPUT_SCHEMA,
  MANIFEST_OUTPUT_SCHEMA,
} from "./toolspec-spec-schemas.js";

/** Annotation marking kit's spec subcommands (Go specCommandAnnotation). */
export const SPEC_COMMAND_ANNOTATION = "kit/spec-command";

const KIT_MANIFEST = "kit-manifest";
const KIT_MANIFEST_ALIASES = ["kit", "manifest"];
const LEGACY_FORMATS = ["json", "yaml", "table", "csv", "text"];

export interface RegisterSpecOptions {
  /** Depth-1 verbs the tool reserves; "spec" is always added. */
  reserved?: Iterable<string>;
}

type Annotated = Command & { annotations?: Record<string, string> };

/** Value and whether it was given on the command line (pflag Changed). */
function formatFlag(cmd: Command): [string, boolean] {
  const v = cmd.optsWithGlobals<{ format?: unknown }>().format;
  const src = cmd.getOptionValueSourceWithGlobals("format");
  return [typeof v === "string" ? v : "", src === "cli"];
}

/** yaml with JSON-projected (snake_case) keys; strings double-quoted. */
function toYAML(v: unknown): string {
  return yaml.dump(v, { quotingType: '"', lineWidth: -1 });
}

function manifestYAML(m: Manifest): string {
  const projected = {
    ...m,
    commands: m.commands.map((c) =>
      c.output_schema === undefined
        ? c
        : { ...c, output_schema: JSON.parse(c.output_schema) as unknown },
    ),
  };
  return toYAML(projected);
}

/**
 * Attach a `spec` subcommand emitting the tool's capability manifest
 * (Go RegisterSpecCommand). Call once after registering every
 * command.
 *
 * - `--format`: `kit-manifest` (default; aliases `kit`, `manifest`)
 *   and `json` emit Go-identical JSON; `yaml` emits the same document
 *   as YAML; `table`/`csv`/`text` emit nothing, as Go does today.
 *   The program's global `--format` is used when it has one,
 *   otherwise spec adds its own.
 * - `--version`: print only the schema version.
 * - `--include-deprecated`: list deprecated commands too.
 *
 * schemaVersion is a floor: each run resolves the emitted version
 * against KIT_TOOLSPEC_SCHEMA (negotiateSchemaVersion).
 *
 * commander parses a program's own options anywhere on the line, so
 * a program-level `--version` (commander `.version()`, which
 * createCLI sets) would answer `spec --version` with the tool
 * version. The program's version listener is wrapped so a literal
 * `--version` is forwarded to spec when spec is the command being
 * executed; `tool --version`, `-v`, and `--version` on any other
 * command keep commander's behaviour. See forwardVersionToSpec.
 */
export function registerSpecCommand(
  program: Command,
  schemaVersion: string,
  opts: RegisterSpecOptions = {},
): Command {
  const reserved = [...(opts.reserved ?? []), "spec"];
  const tool = program.name();

  const spec = new Command("spec")
    .summary("Emit machine-readable capability manifest")
    .description(
      "Emit the tool's full capability manifest as JSON/YAML/table. " +
        "Agents consume this for capability negotiation and dispatch. " +
        "--version prints only the schema version.",
    )
    .option("--version", "Print the schema version only (capability negotiation)")
    .option(
      "--include-deprecated",
      "Include deprecated commands in the manifest (default: hidden)",
    );
  if (!program.options.some((o) => o.long === "--format")) {
    spec.option("--format <format>", "Output format");
  }
  (spec as Annotated).annotations = {
    "kit/side-effect": "read",
    "kit/idempotent": "yes",
    [SPEC_COMMAND_ANNOTATION]: "true",
    "kit/top-level-verb": "true",
    "kit/output-schema": MANIFEST_OUTPUT_SCHEMA,
    "kit/output-schema-version": schemaVersion,
    "kit/examples": JSON.stringify([
      { title: "Full capabilities", command: `${tool} spec --format json` },
      { title: "Schema probe", command: `${tool} spec --version` },
      { title: "Filter to old clients", command: `${tool} spec --api-version=1.0` },
    ]),
  };

  let versionForwarded = false;
  forwardVersionToSpec(program, spec, () => {
    versionForwarded = true;
  }, () => {
    versionForwarded = false;
  });

  spec.action(function (this: Command) {
    const out = (s: string): void => this.configureOutput().writeOut!(s);
    const resolved = negotiateSchemaVersion(
      schemaVersion,
      process.env[SCHEMA_VERSION_ENV] ?? "",
    );
    const local = this.opts<{ version?: boolean; includeDeprecated?: boolean }>();
    const [given, changed] = formatFlag(this);
    const format = changed ? given : "";

    if (local.version || versionForwarded) {
      const vf = format === "" ? "json" : format;
      if (vf === "json") {
        out(encodeGoJSON({ schema_version: resolved }, 0, new Set()) + "\n");
      } else if (vf === "yaml") {
        out(toYAML({ schema_version: resolved }));
      } else {
        out(resolved + "\n");
      }
      return;
    }

    let outputFormat: string;
    if (format === "" || format === KIT_MANIFEST || KIT_MANIFEST_ALIASES.includes(format)) {
      outputFormat = "json";
    } else if (LEGACY_FORMATS.includes(format)) {
      outputFormat = format;
    } else {
      const valid = [KIT_MANIFEST, ...LEGACY_FORMATS].join(", ");
      this.error(`unknown --format ${JSON.stringify(format)} (valid: ${valid})`, {
        exitCode: EXIT_GENERIC,
        code: "kit.spec.unknownFormat",
      });
    }

    const manifest = buildManifest(program, resolved, {
      includeDeprecated: local.includeDeprecated === true,
      reserved,
      invokedCommand: this,
    });
    if (outputFormat === "json") out(renderManifestJSON(manifest));
    else if (outputFormat === "yaml") out(manifestYAML(manifest));
    // table/csv/text: the Manifest has no tabular projection; Go
    // renders nothing for it, and so does this port.
  });

  registerCoverageCommand(spec, program, reserved);
  program.addCommand(spec);
  return spec;
}

// --- spec --version forwarding ---

const WRAPPED = new WeakSet<(...args: unknown[]) => void>();

/** The subcommand of cmd that args select, skipping known option values. */
function selectedSubcommand(cmd: Command, args: readonly string[]): [Command, string[]] | undefined {
  const options = [cmd, ...ancestors(cmd)].flatMap((c) => c.options as readonly Option[]);
  const find = (flag: string) =>
    options.find((o) => o.long === flag || o.short === flag);
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--") return undefined;
    if (a.length > 1 && a.startsWith("-")) {
      const long = a.startsWith("--");
      const opt = find(long ? a.split("=")[0] : a.slice(0, 2));
      const inline = long ? a.includes("=") : a.length > 2;
      const next = args[i + 1];
      if (
        opt &&
        !inline &&
        (opt.required || (opt.optional && next !== undefined && !next.startsWith("-")))
      ) {
        i++;
      }
      continue;
    }
    const sub = cmd.commands.find((c) => c.name() === a || c.aliases().includes(a));
    return sub ? [sub, args.slice(i + 1)] : undefined;
  }
  return undefined;
}

function ancestors(cmd: Command): Command[] {
  const out: Command[] = [];
  for (let c = cmd.parent; c; c = c.parent) out.push(c);
  return out;
}

/**
 * Route a literal `--version` to spec when spec is the executing
 * command. The program's parseOptions (public) is wrapped per
 * instance to see the user args at each parse; the program's
 * `option:version` listeners (commander 15 registers one in
 * `.version()`, emitted from parseOptions) are wrapped lazily there,
 * so a `.version()` call after registerSpecCommand is covered too.
 * When forwarding, the wrapped listener records the request instead
 * of printing and exiting. commander internals relied on (pinned
 * 15.0.0): the `option:<name>` event convention, that `.version()`
 * reacts through such a listener, that the program's own
 * parseOptions is invoked via `this` (so the instance override
 * applies), and the EventEmitter base typed locally below.
 */
function forwardVersionToSpec(
  program: Command,
  spec: Command,
  forward: () => void,
  reset: () => void,
): void {
  let forwarding = false;
  const original = program.parseOptions.bind(program);

  const wrapListeners = (): void => {
    const versionOpt = (program.options as readonly Option[]).find(
      (o) => o.long === "--version",
    );
    if (!versionOpt) return;
    const event = `option:${versionOpt.name()}`;
    // commander 15's Command extends EventEmitter at runtime; its
    // typings only declare on(), so the emitter surface is typed here.
    const emitter = program as unknown as EventEmitter;
    for (const l of emitter.listeners(event) as ((...a: unknown[]) => void)[]) {
      if (WRAPPED.has(l)) continue;
      const wrapped = function (this: unknown, ...a: unknown[]): void {
        if (forwarding) {
          forward();
          return;
        }
        l.apply(this, a);
      };
      WRAPPED.add(wrapped);
      emitter.removeListener(event, l);
      emitter.on(event, wrapped);
    }
  };

  program.parseOptions = (args: string[]) => {
    reset();
    wrapListeners();
    let executing: Command = program;
    let rest: readonly string[] = args;
    for (let sel = selectedSubcommand(executing, rest); sel; sel = selectedSubcommand(executing, rest)) {
      [executing, rest] = sel;
    }
    forwarding = executing === spec && args.includes("--version");
    try {
      return original(args);
    } finally {
      forwarding = false;
    }
  };
}

// --- spec coverage ---

/** Go CoverageReport: annotation coverage over a command tree. */
export interface CoverageReport {
  tool: string;
  total: number;
  annotated: number;
  unannotated: number;
  inferred: number;
  malformed: number;
  unannotated_paths?: string[];
  malformed_paths?: string[];
  measurable: boolean;
}

const KNOWN_SIDE_EFFECTS = new Set([
  "read",
  "write",
  "write-local",
  "write-shared",
  "destructive",
  "destructive-local",
  "destructive-shared",
  "interactive",
]);

const DESTRUCTIVE_NAMES = new Set(["delete", "remove", "rm", "destroy", "purge", "drop"]);

export interface CoverageOptions {
  /** Depth-1 verbs the tool reserves (not measured). */
  reserved?: Iterable<string>;
}

/**
 * Measure kit/side-effect annotation coverage (Go CoverageOf):
 * runnable commands, excluding framework builtins, reserved verbs and
 * kit's spec commands. Deprecated and hidden commands count.
 */
export function coverageOf(program: Command, opts: CoverageOptions = {}): CoverageReport {
  const rep: CoverageReport = {
    tool: program.name(),
    total: 0,
    annotated: 0,
    unannotated: 0,
    inferred: 0,
    malformed: 0,
    measurable: false,
  };
  const unannotated: string[] = [];
  const malformed: string[] = [];

  walkCommands(program, opts.reserved, (cmd, parent, path, reserved) => {
    const ann = annotationsOf(cmd);
    if (!isRunnable(cmd) || isBuiltin(cmd, parent) || reserved) return;
    if (ann[SPEC_COMMAND_ANNOTATION] === "true") return;
    rep.total++;
    const p = path.join(" ");
    const raw = ann["kit/side-effect"] ?? "";
    if (raw !== "" && !KNOWN_SIDE_EFFECTS.has(raw)) {
      rep.malformed++;
      malformed.push(`${p}=${JSON.stringify(raw)}`);
      unannotated.push(p);
    } else if (raw === "") {
      if (DESTRUCTIVE_NAMES.has(cmd.name())) rep.inferred++;
      rep.unannotated++;
      unannotated.push(p);
    } else {
      rep.annotated++;
    }
  });

  rep.unannotated += rep.malformed;
  if (unannotated.length > 0) rep.unannotated_paths = unannotated.sort();
  if (malformed.length > 0) rep.malformed_paths = malformed.sort();
  rep.measurable = rep.total > 0;
  // Re-insert measurable last so encoded key order matches Go.
  const { measurable, ...rest } = rep;
  return { ...rest, measurable };
}

function coveragePercent(r: CoverageReport): number {
  return r.total === 0 ? 0 : (r.annotated / r.total) * 100;
}

function coverageMeets(r: CoverageReport, threshold: number): boolean {
  if (threshold <= 0) return true;
  if (!r.measurable) return false;
  return coveragePercent(r) >= threshold;
}

/** Collects TS output-registry renders into a string. */
function captureRender(format: string, v: unknown): string {
  let buf = "";
  const sink = {
    write(chunk: unknown): boolean {
      buf += String(chunk);
      return true;
    },
  } as unknown as NodeJS.WritableStream;
  renderOutput(sink, format as Format, v);
  return buf;
}

function registerCoverageCommand(spec: Command, program: Command, reserved: string[]): void {
  const tool = program.name();
  const min = new Option(
    "--min <percent>",
    "Minimum coverage percentage; exit CONFLICT (4) below it (0 disables the gate)",
  )
    .argParser((v: string) => {
      const n = Number(v);
      if (v.trim() === "" || Number.isNaN(n)) {
        throw new Error(`invalid argument "${v}" for "--min" flag`);
      }
      return n;
    })
    .default(0);
  pflagTypes.set(min, "float64");

  const cmd = new Command("coverage")
    .summary("Report kit/side-effect annotation coverage")
    .description(
      "Report how many of this tool's commands declare a " +
        "kit/side-effect class, list the ones that do not, and " +
        "exit non-zero when coverage falls below --min.\n\n" +
        "An unannotated command is not a read-only command: it " +
        "is one kit knows nothing about, and every safety gate " +
        "that consumes the manifest must fail closed on it. This " +
        "report is how an adopter finds and closes that gap, and " +
        "how CI keeps it closed.",
    )
    .addOption(min);
  (cmd as Annotated).annotations = {
    "kit/side-effect": "read",
    "kit/idempotent": "yes",
    "kit/exit-codes": "OK,CONFLICT",
    [SPEC_COMMAND_ANNOTATION]: "true",
    "kit/output-schema": COVERAGE_REPORT_OUTPUT_SCHEMA,
    "kit/output-schema-version": "1.0",
    "kit/examples": JSON.stringify([
      { title: "See the gap", command: `${tool} spec coverage --format json` },
      { title: "Gate in CI", command: `${tool} spec coverage --min 80` },
    ]),
  };

  cmd.action(function (this: Command) {
    const rep = coverageOf(program, { reserved });
    const [given, changed] = formatFlag(this);
    const format = changed && given !== "" ? given : "json";
    const text =
      format === "json" ? encodeGoJSON(rep, 0, new Set()) + "\n" : captureRender(format, rep);
    this.configureOutput().writeOut!(text);

    const threshold = this.opts<{ min: number }>().min;
    if (coverageMeets(rep, threshold)) return;
    const message = !rep.measurable
      ? `annotation coverage unmeasurable: no reflectable commands in ${JSON.stringify(rep.tool)} ` +
        `(--min ${goFormat0(threshold)}% cannot be evaluated)`
      : `annotation coverage ${goFormat0(coveragePercent(rep))}% (${rep.annotated}/${rep.total}) ` +
        `is below --min ${goFormat0(threshold)}%; unannotated: ` +
        (rep.unannotated_paths ?? []).join(", ");
    this.error(message, { exitCode: EXIT_CONFLICT, code: "kit.spec.coverageBelowMin" });
  });

  spec.addCommand(cmd);
}
