/**
 * @module output/formatter
 *
 * Core Formatter interface + Options/OptionSpec/ColumnSpec types and
 * parseOptions helper. Mirrors Go's go/console/output Formatter interface.
 */

/** Kinds of values an OptionSpec accepts. */
export type OptionType = 'string' | 'int' | 'bool' | 'enum';

/** Describes one option accepted by a Formatter via --format-opt key=value. */
export interface OptionSpec {
  readonly name: string;
  readonly type: OptionType;
  readonly default?: string | number | boolean;
  readonly usage: string;
  readonly enum?: readonly string[];
}

/**
 * Validated map of option values produced by parseOptions. Keys are
 * OptionSpec.name; values are coerced to the spec's declared type.
 */
export type Options = Readonly<Record<string, string | number | boolean>>;

/**
 * Column metadata used by table/csv/text formatters and --cols validation.
 *
 * - `header` is the user-visible label and the value matched against --cols.
 * - `key` is the property name on the row object.
 * - `priority` (optional) controls hide-on-overflow for table; higher wins.
 */
export interface ColumnSpec {
  readonly header: string;
  readonly key: string;
  readonly priority?: number;
}

/**
 * A Formatter encodes structured data to a writable stream.
 *
 * Implementations declare their key, file extensions, and the option keys
 * they accept. The dispatch helper validates --format-opt input against
 * options() before invoking render(), so render() may trust opts to only
 * contain declared keys with values coerced to declared types.
 */
export interface Formatter<T = unknown> {
  /** Unique format identifier exposed via --format <key>. */
  readonly key: string;

  /**
   * File extensions (with leading dot, e.g. ".csv") that map to this
   * formatter for --output extension inference. May be empty.
   */
  readonly extensions: readonly string[];

  /** Option specs accepted by this formatter via --format-opt key=value. */
  readonly options: readonly OptionSpec[];

  /**
   * Renders data to out.
   *
   * @param out Destination writable stream.
   * @param data Single row or readonly array of rows.
   * @param opts Validated options; only declared keys with coerced values.
   * @param cols User-requested column projection; empty means "all columns".
   */
  render(
    out: NodeJS.WritableStream,
    data: T | readonly T[],
    opts: Options,
    cols: readonly string[],
  ): Promise<void> | void;
}

/**
 * Validates raw `key=value` pairs against specs and returns a coerced
 * Options map. Unknown keys, type errors, and out-of-enum values throw an
 * Error listing the offending key and the valid set.
 *
 * A pair without `=` (e.g. `"no-header"`) is treated as bool true; only
 * valid when the matching spec has type `'bool'`.
 *
 * Defaults from specs fill in any keys not present in pairs.
 */
export function parseOptions(
  pairs: readonly string[],
  specs: readonly OptionSpec[],
): Options {
  const specByName = new Map<string, OptionSpec>();
  for (const s of specs) {
    specByName.set(s.name, s);
  }

  const out: Record<string, string | number | boolean> = {};
  for (const raw of pairs) {
    const eq = raw.indexOf('=');
    const hasEq = eq !== -1;
    const key = (hasEq ? raw.slice(0, eq) : raw).trim();
    const val = hasEq ? raw.slice(eq + 1) : '';
    if (key === '') {
      throw new Error(`empty option key in "${raw}"`);
    }
    const spec = specByName.get(key);
    if (!spec) {
      const valid = specs.map(s => s.name).join(', ');
      throw new Error(`unknown option "${key}" (valid: ${valid})`);
    }
    if (!hasEq) {
      if (spec.type !== 'bool') {
        throw new Error(`option "${key}" requires a value (e.g. ${key}=...)`);
      }
      out[key] = true;
      continue;
    }
    out[key] = coerce(spec, val);
  }

  for (const s of specs) {
    if (Object.prototype.hasOwnProperty.call(out, s.name)) continue;
    if (s.default !== undefined) {
      out[s.name] = s.default;
    }
  }
  return out;
}

function coerce(
  spec: OptionSpec,
  val: string,
): string | number | boolean {
  switch (spec.type) {
    case 'string':
      return val;
    case 'int': {
      // Go's strconv.Atoi accepts only base-10, no leading/trailing space.
      if (!/^-?\d+$/.test(val)) {
        throw new Error(`option "${spec.name}": "${val}" is not an int`);
      }
      const n = Number(val);
      if (!Number.isSafeInteger(n)) {
        throw new Error(`option "${spec.name}": "${val}" is not an int`);
      }
      return n;
    }
    case 'bool': {
      // Mirrors Go's strconv.ParseBool accepted set.
      const truthy = new Set(['1', 't', 'T', 'TRUE', 'true', 'True']);
      const falsy = new Set(['0', 'f', 'F', 'FALSE', 'false', 'False']);
      if (truthy.has(val)) return true;
      if (falsy.has(val)) return false;
      throw new Error(`option "${spec.name}": "${val}" is not a bool`);
    }
    case 'enum': {
      const allowed = spec.enum ?? [];
      if (allowed.includes(val)) return val;
      throw new Error(
        `option "${spec.name}": "${val}" not in {${allowed.join(', ')}}`,
      );
    }
  }
}

/** Pretty-print an OptionType. */
export function optionTypeName(t: OptionType): string {
  return t;
}
