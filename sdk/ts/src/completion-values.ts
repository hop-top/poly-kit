/**
 * @module completion-values
 * @package @hop-top/kit
 *
 * Dynamic value completion for Commander-based CLIs. Ports Go
 * `console/cli/completion`; behaviour is pinned by
 * `contracts/completion-v1/completers.json`.
 *
 * A {@link Completer} turns the word being completed into
 * {@link CompletionItem}s. `bindFlag` / `bindArgs` (in `completion`)
 * attach one to a flag or to a command's positional arguments; the
 * scripts `completion <shell>` writes call back into the program's
 * hidden `__complete` entry point, which runs the bound completer and
 * prints the result through {@link resolveCompletions} in cobra's wire
 * format.
 *
 * Contract:
 * - Prefix matching in static, config-key and the dimension half of
 *   prefixed completers is case-insensitive; an empty prefix returns
 *   every item. {@link funcCompleter} sees the raw prefix, unfiltered.
 * - A completer fails by throwing (or rejecting); the bridge turns that
 *   into {@link ShellCompDirective.Error}.
 * - {@link fileCompleter} and {@link dirCompleter} yield a directive,
 *   not suggestions: filter by extension (the extensions travel as the
 *   completions), directories only, or plain file completion when no
 *   extension is given.
 */

export interface CompletionItem {
  value: string;
  description?: string;
}

/** What a completer knows about the command line it completes. */
export interface CompletionContext {
  /** Space-separated path of the command below the root; empty for the root. */
  commandPath: string;
  /** Positional arguments already given, flags removed. */
  args: string[];
}

/** What a file completer asks the shell for. */
export type FileFilter =
  | { kind: 'extensions'; extensions: string[] }
  | { kind: 'dirs' };

export interface Completer {
  complete(
    prefix: string,
    ctx?: CompletionContext,
  ): CompletionItem[] | Promise<CompletionItem[]>;
  /** Set by {@link fileCompleter} and {@link dirCompleter}. */
  readonly fileFilter?: FileFilter;
}

/** cobra's shell directive bits, read by the scripts from the `:<n>` line. */
export const ShellCompDirective = {
  Default: 0,
  Error: 1,
  NoSpace: 2,
  NoFileComp: 4,
  FilterFileExt: 8,
  FilterDirs: 16,
  KeepOrder: 32,
} as const;

/** cobra's name for a directive, as printed on stderr after a request. */
export function directiveName(d: number): string {
  if (d >= 64) return `ERROR: unexpected ShellCompDirective value: ${d}`;
  const names: [number, string][] = [
    [ShellCompDirective.Error, 'ShellCompDirectiveError'],
    [ShellCompDirective.NoSpace, 'ShellCompDirectiveNoSpace'],
    [ShellCompDirective.NoFileComp, 'ShellCompDirectiveNoFileComp'],
    [ShellCompDirective.FilterFileExt, 'ShellCompDirectiveFilterFileExt'],
    [ShellCompDirective.FilterDirs, 'ShellCompDirectiveFilterDirs'],
    [ShellCompDirective.KeepOrder, 'ShellCompDirectiveKeepOrder'],
  ];
  const set = names.filter(([bit]) => (d & bit) === bit).map(([, n]) => n);
  return set.length ? set.join(', ') : 'ShellCompDirectiveDefault';
}

/** Completions in wire form plus the directive. */
export interface ResolvedCompletions {
  /** `value`, or `value\tdescription` when there is one. */
  completions: string[];
  directive: number;
}

/**
 * Runs `c` for `toComplete` and converts the result to the shell wire
 * format: values with any description joined by a tab, plus the
 * directive (no file completion on success, error when `c` fails).
 */
export async function resolveCompletions(
  c: Completer,
  toComplete: string,
  ctx: CompletionContext = { commandPath: '', args: [] },
): Promise<ResolvedCompletions> {
  const f = c.fileFilter;
  if (f?.kind === 'dirs') {
    return { completions: [], directive: ShellCompDirective.FilterDirs };
  }
  if (f?.kind === 'extensions') {
    // An empty filter list would filter every candidate out.
    return f.extensions.length === 0
      ? { completions: [], directive: ShellCompDirective.Default }
      : { completions: [...f.extensions], directive: ShellCompDirective.FilterFileExt };
  }
  try {
    const items = await c.complete(toComplete, ctx);
    return {
      completions: items.map(withDescription),
      directive: ShellCompDirective.NoFileComp,
    };
  } catch {
    return { completions: [], directive: ShellCompDirective.Error };
  }
}

/** `value`, or `value\tdescription` when there is one. */
export function withDescription(i: CompletionItem): string {
  return i.description ? `${i.value}\t${i.description}` : i.value;
}

/**
 * Maps flag names and argument positions to completers, for sharing
 * completers across a command tree (Go `Registry`). A lookup table
 * only: `bindFlag` / `bindArgs` wire a completer into completion.
 */
export class CompletionRegistry {
  private flags = new Map<string, Completer>();
  private args = new Map<string, Completer>();

  register(flag: string, completer: Completer): void {
    this.flags.set(flag, completer);
  }

  registerArg(cmd: string, pos: number, completer: Completer): void {
    this.args.set(`${cmd}:${pos}`, completer);
  }

  forFlag(flag: string): Completer | undefined {
    return this.flags.get(flag);
  }

  forArg(cmd: string, pos: number): Completer | undefined {
    return this.args.get(`${cmd}:${pos}`);
  }
}

/** Filter items by case-insensitive prefix match; keeps duplicates. */
function filterByPrefix(
  items: CompletionItem[],
  prefix: string,
): CompletionItem[] {
  if (!prefix) return [...items];
  const lp = prefix.toLowerCase();
  return items.filter(i => i.value.toLowerCase().startsWith(lp));
}

/** Completer from pre-defined CompletionItems. */
export function staticCompleter(
  ...items: CompletionItem[]
): Completer {
  return {
    complete(prefix: string) {
      return filterByPrefix(items, prefix);
    },
  };
}

/** Completer from plain string values. */
export function staticValues(...values: string[]): Completer {
  const items = values.map(v => ({ value: v }));
  return staticCompleter(...items);
}

/**
 * Completer backed by a callback. The callback sees the raw prefix;
 * nothing is filtered for it. Throw (or reject) to fail.
 */
export function funcCompleter(
  fn: (
    prefix: string,
    ctx: CompletionContext,
  ) => CompletionItem[] | Promise<CompletionItem[]>,
): Completer {
  return {
    complete(prefix, ctx = { commandPath: '', args: [] }) {
      return fn(prefix, ctx);
    },
  };
}

/**
 * Completer for dimensioned values like `env:prod`. While no colon is
 * typed it suggests `dimension:` (matched case-insensitively); after
 * the first colon it delegates the rest to `values` and re-prepends
 * the declared `dimension:`.
 */
export function prefixedCompleter(
  dimension: string,
  values: Completer,
): Completer {
  const lower = dimension.toLowerCase();
  const prepend = (items: CompletionItem[]) =>
    items.map(i => {
      const v = `${dimension}:${i.value}`;
      return i.description ? { value: v, description: i.description } : { value: v };
    });
  return {
    complete(prefix, ctx) {
      const colonIdx = prefix.indexOf(':');
      if (colonIdx < 0) {
        const dim = `${dimension}:`;
        return dim.toLowerCase().startsWith(prefix.toLowerCase())
          ? [{ value: dim, description: `${dimension} values` }]
          : [];
      }
      if (prefix.slice(0, colonIdx).toLowerCase() !== lower) return [];
      const inner = values.complete(prefix.slice(colonIdx + 1), ctx);
      return Array.isArray(inner) ? prepend(inner) : inner.then(prepend);
    },
  };
}

/** The dotted leaf keys of a config tree, lowercased like viper's AllKeys. */
function leafKeys(obj: Record<string, unknown>, prefix: string, out: Set<string>): void {
  for (const [k, v] of Object.entries(obj)) {
    const full = prefix ? `${prefix}.${k.toLowerCase()}` : k.toLowerCase();
    if (v != null && typeof v === 'object' && !Array.isArray(v)) {
      leafKeys(v as Record<string, unknown>, full, out);
    } else {
      out.add(full);
    }
  }
}

/**
 * Completer that suggests config keys: the dotted leaf keys of
 * `config`, lowercased, matched case-insensitively.
 */
export function configKeysCompleter(
  config: Record<string, unknown>,
): Completer {
  return {
    complete(prefix: string) {
      const keys = new Set<string>();
      leafKeys(config, '', keys);
      return filterByPrefix([...keys].map(k => ({ value: k })), prefix);
    },
  };
}

/**
 * Completer that asks the shell for files with these extensions (e.g.
 * `.yaml`); none means any file. Its items are the extensions, which
 * travel as the completions of a filter-by-extension directive.
 */
export function fileCompleter(...extensions: string[]): Completer {
  return {
    fileFilter: { kind: 'extensions', extensions: [...extensions] },
    complete() {
      return extensions.map(e => ({ value: e }));
    },
  };
}

/** Completer that asks the shell for directories only. */
export function dirCompleter(): Completer {
  return {
    fileFilter: { kind: 'dirs' },
    complete() {
      return [];
    },
  };
}
