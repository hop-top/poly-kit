/**
 * llm.yaml: `{configDir("hop")}/llm.yaml`, read as Go `llm` reads it.
 * Best-effort: a missing, unreadable or malformed file is an empty
 * config. Read on each call, never cached. A hermetic config does not
 * call it.
 *
 * ```yaml
 * default: anthropic://claude-sonnet-4-5
 * fallback: [openai://gpt-4.1]
 * providers:
 *   anthropic:
 *     api_key: ...         # or api_key_env: MY_ANTHROPIC_KEY
 *     base_url: https://gw.corp.example
 *     model: claude-sonnet-4-5
 *     routellm: {...}      # any other key: ProviderConfig.extras
 * ```
 *
 * @internal
 */

import { readFileSync } from "node:fs";
import { join } from "node:path";
import * as yaml from "js-yaml";

import { configDir } from "./xdg";

/** One `providers.<name>` block. */
export interface LLMFileProvider {
  apiKey: string;
  apiKeyEnv: string;
  baseURL: string;
  model: string;
  /** The block's other keys, typed as YAML types them. */
  extras: Record<string, unknown>;
}

/** The parts of llm.yaml the llm core reads. */
export interface LLMFile {
  default: string;
  fallback: string[];
  providers: Map<string, LLMFileProvider>;
}

const EMPTY: LLMFile = { default: "", fallback: [], providers: new Map() };

const FIELDS = new Set(["api_key", "api_key_env", "base_url", "model"]);

/** Reads llm.yaml; an empty config when there is none to read. */
export function readLLMFile(): LLMFile {
  let text: string;
  try {
    text = readFileSync(join(configDir("hop"), "llm.yaml"), "utf8");
  } catch {
    return emptyFile();
  }
  try {
    // Fields decode as Go decodes them into strings: every scalar as
    // written (FAILSAFE). Extras keep their YAML types (CORE).
    return decode(
      yaml.load(text, { schema: yaml.FAILSAFE_SCHEMA }),
      yaml.load(text, { schema: yaml.CORE_SCHEMA })
    );
  } catch {
    return emptyFile();
  }
}

/** The block configuring `scheme`, if llm.yaml has one. */
export function providerBlock(file: LLMFile, scheme: string): LLMFileProvider | undefined {
  return file.providers.get(scheme);
}

function emptyFile(): LLMFile {
  return { ...EMPTY, fallback: [], providers: new Map() };
}

class Shape extends Error {}

function isMap(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** A string field: absent or null is "", a scalar is its text. */
function str(v: unknown): string {
  if (v === undefined || v === null) return "";
  if (typeof v !== "string") throw new Shape();
  return v;
}

/**
 * Builds the config from the file parsed twice; a field of the wrong
 * shape empties the whole file, as a failed Go decode does.
 */
function decode(raw: unknown, typed: unknown): LLMFile {
  if (raw === undefined || raw === null) return emptyFile();
  if (!isMap(raw)) throw new Shape();
  const t = isMap(typed) ? typed : {};
  const out = emptyFile();
  out.default = str(raw["default"]);
  const fb = raw["fallback"];
  if (fb !== undefined && fb !== null) {
    if (!Array.isArray(fb)) throw new Shape();
    out.fallback = fb.map(str);
  }
  const ps = raw["providers"];
  if (ps === undefined || ps === null) return out;
  if (!isMap(ps)) throw new Shape();
  const tps = isMap(t["providers"]) ? t["providers"] : {};
  for (const [name, block] of Object.entries(ps)) {
    const b = block === null ? {} : block;
    if (!isMap(b)) throw new Shape();
    const tb = isMap(tps[name]) ? (tps[name] as Record<string, unknown>) : {};
    const extras: Record<string, unknown> = {};
    for (const k of Object.keys(b)) {
      if (!FIELDS.has(k) && k !== "__proto__") extras[k] = tb[k];
    }
    out.providers.set(name, {
      apiKey: str(b["api_key"]),
      apiKeyEnv: str(b["api_key_env"]),
      baseURL: str(b["base_url"]),
      model: str(b["model"]),
      extras,
    });
  }
  return out;
}
