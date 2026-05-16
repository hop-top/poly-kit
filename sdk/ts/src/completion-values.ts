/**
 * @module completion-values
 * @package @hop-top/kit
 *
 * Dynamic value completion system for Commander-based CLIs.
 * Provides a Completer interface and built-in completers for
 * flags and positional arguments. Shell scripts (from completion.ts)
 * call back via __complete; the registry resolves the right completer.
 */

export interface CompletionItem {
  value: string;
  description?: string;
}

export interface Completer {
  complete(prefix: string): CompletionItem[] | Promise<CompletionItem[]>;
}

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

/** Filter items by case-insensitive prefix match. */
function filterByPrefix(
  items: CompletionItem[],
  prefix: string,
): CompletionItem[] {
  if (!prefix) return items;
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

/** Completer backed by a callback function. */
export function funcCompleter(
  fn: (prefix: string) => CompletionItem[],
): Completer {
  return { complete: fn };
}

/**
 * Completer for dimensioned values like `env:prod`.
 * Before the colon, suggests the dimension prefix.
 * After the colon, delegates to the inner completer.
 */
export function prefixedCompleter(
  dimension: string,
  values: Completer,
): Completer {
  return {
    complete(prefix: string) {
      const colonIdx = prefix.indexOf(':');
      if (colonIdx < 0) {
        // no colon yet — suggest dimension prefix
        return [{ value: `${dimension}:`, description: `${dimension}:...` }];
      }

      const dim = prefix.slice(0, colonIdx);
      if (dim !== dimension) return [];

      const valPrefix = prefix.slice(colonIdx + 1);
      const inner = values.complete(valPrefix);
      if (Array.isArray(inner)) {
        return inner.map(i => ({
          value: `${dimension}:${i.value}`,
          description: i.description,
        }));
      }
      // async path
      return (inner as Promise<CompletionItem[]>).then(items =>
        items.map(i => ({
          value: `${dimension}:${i.value}`,
          description: i.description,
        })),
      );
    },
  };
}

/** Flatten object keys into dot-notation paths. */
function flattenKeys(
  obj: Record<string, any>,
  prefix = '',
): string[] {
  const keys: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const full = prefix ? `${prefix}.${k}` : k;
    keys.push(full);
    if (v != null && typeof v === 'object' && !Array.isArray(v)) {
      keys.push(...flattenKeys(v, full));
    }
  }
  return keys;
}

/** Completer that suggests config keys in dot-notation. */
export function configKeysCompleter(
  config: Record<string, any>,
): Completer {
  return {
    complete(prefix: string) {
      const all = flattenKeys(config);
      return filterByPrefix(
        all.map(k => ({ value: k })),
        prefix,
      );
    },
  };
}

/**
 * Completer that signals shell-level file completion.
 * Returns a marker item; the __complete handler translates
 * this to shell-native file glob directives.
 */
export function fileCompleter(...extensions: string[]): Completer {
  const desc = extensions.length
    ? `Files: ${extensions.join(', ')}`
    : 'Files';
  return {
    complete() {
      return [
        {
          value: '__file__',
          description: desc,
        },
      ];
    },
  };
}

/** Completer that signals shell-level directory completion. */
export function dirCompleter(): Completer {
  return {
    complete() {
      return [{ value: '__dir__', description: 'Directories' }];
    },
  };
}
