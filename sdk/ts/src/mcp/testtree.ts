/**
 * Test command trees mirroring the Go lock trees that generated the
 * cross-language wire fixtures.
 *
 * The Go generator (surface_mcp_fixtures_gen_test.go) drives TWO
 * trees: `legacyLockTree` for the legacy cases and `modernLockTree`
 * for the modern ones. They are deliberately different — the legacy
 * tree's `widget add` carries extra `force`/`tag` flags that the
 * modern tree omits — so the fixtures cannot be served from one
 * shared tree. Both are reproduced here.
 *
 * This module is test scaffolding, not part of the published surface.
 */

import {
  DestructiveBlockedError,
  defaultPolicy,
  policyAllowed,
  SURFACE_MCP,
  UnknownCommandError,
  type FlagSchema,
  type Invocation,
  type InvokeResult,
  type Leaf,
  type McpBridge,
} from './types.js';

interface TreeLeaf extends Leaf {
  /** Text the leaf prints on stdout when invoked. */
  stdout?: string;
}

const str = (description: string): FlagSchema => ({
  type: 'string',
  description,
});
const int = (description: string): FlagSchema => ({
  type: 'integer',
  description,
});
const bool = (description: string): FlagSchema => ({
  type: 'boolean',
  description,
});
const strArray = (description: string): FlagSchema => ({
  type: 'array',
  description,
  items: { type: 'string' },
});

/**
 * The Go legacy lock tree:
 *
 *   root
 *   ├── widget
 *   │   ├── add     (write; name str required, count int, force bool,
 *   │   │            tag []str; hidden/deprecated flags excluded)
 *   │   └── delete  (destructive)
 *   ├── secret      (auth-required)
 *   ├── deploy      (requires-confirmation)
 *   └── ping        (read; the happy-path exec target)
 *
 * Leaf order matches Go's `Bridge.Leaves()` enumeration, which sorts
 * by dotted tool name — the order the fixture's `tools` array pins.
 */
function legacyLeaves(): TreeLeaf[] {
  return [
    { path: ['deploy'], description: 'Deploy', properties: {}, class: { requiresConfirmation: true } },
    { path: ['ping'], description: 'Ping the server', properties: {}, stdout: 'pong\n' },
    { path: ['secret'], description: 'Locked', properties: {}, class: { authRequired: true } },
    {
      path: ['widget', 'add'],
      description: 'Add a widget',
      properties: {
        name: str('widget name'),
        count: int('widget count'),
        force: bool('force flag'),
        tag: strArray('tag list'),
      },
      required: ['name'],
      stdout: 'added\n',
    },
    {
      path: ['widget', 'delete'],
      description: 'Delete a widget',
      properties: {},
      class: { destructive: true },
      stdout: 'deleted\n',
    },
  ];
}

/**
 * The Go modern lock tree: same shape, but `widget add` declares only
 * `name` (required) and `count`.
 */
function modernLeaves(): TreeLeaf[] {
  return [
    { path: ['deploy'], description: 'Deploy', properties: {}, class: { requiresConfirmation: true } },
    { path: ['ping'], description: 'Ping the server', properties: {}, stdout: 'pong\n' },
    { path: ['secret'], description: 'Locked', properties: {}, class: { authRequired: true } },
    {
      path: ['widget', 'add'],
      description: 'Add a widget',
      properties: { name: str('widget name'), count: int('widget count') },
      required: ['name'],
      stdout: 'added\n',
    },
    {
      path: ['widget', 'delete'],
      description: 'Delete a widget',
      properties: {},
      class: { destructive: true },
      stdout: 'deleted\n',
    },
  ];
}

/**
 * Builds a bridge over a fixed leaf set, applying the default policy
 * on invoke so the destructive ceiling is exercised exactly as the Go
 * bridge exercises it.
 *
 * The bridge models cobra's lazy help-flag attachment, which is real
 * observable behavior of the Go reference rather than a fixture
 * artifact: cobra attaches a command's `--help` flag on that
 * command's FIRST execution, so a leaf's schema legitimately gains a
 * `help` property once it has been invoked. On a long-lived mount the
 * same `tools/list` bytes therefore produce different responses
 * before and after a `tools/call` — which is what adopters serving
 * from a persistent process actually observe.
 *
 * The rule is applied generically to whichever leaf was executed, not
 * special-cased to `ping` or to any expected fixture bytes: a bridge
 * that hardcoded the answer would pass the sequence while still
 * misrepresenting the mechanism.
 *
 * `leaves()` recomputes from the mutable execution set on every call,
 * which is what lets the surface observe the change — the library
 * re-reads `bridge.leaves()` per request, so a snapshot taken once at
 * construction would hide this behavior.
 */
function makeBridge(source: () => TreeLeaf[]): McpBridge {
  const leaves = source();
  const policy = defaultPolicy();
  // Leaf keys whose command has been executed at least once, and has
  // therefore had its help flag attached by cobra.
  const executed = new Set<string>();

  return {
    leaves: (): Leaf[] =>
      leaves.map((l) => {
        const key = l.path.join('.');
        if (!executed.has(key)) return l;
        return {
          ...l,
          properties: {
            ...l.properties,
            help: bool(`help for ${l.path[l.path.length - 1]}`),
          },
        };
      }),
    invoke(inv: Invocation): InvokeResult {
      const key = inv.path.join('.');
      const leaf = leaves.find((l) => l.path.join('.') === key);
      if (leaf === undefined) {
        throw new UnknownCommandError(`unknown command: ${key}`);
      }
      if (!policyAllowed(policy, leaf.class ?? {}, inv.meta.surface)) {
        throw new DestructiveBlockedError(leaf.path.join(' '), inv.meta.surface);
      }
      // Cobra attaches the help flag as part of executing the command,
      // so this happens only once the policy gate has let it through:
      // a blocked invocation never runs, and never attaches the flag.
      executed.add(key);
      return { stdout: leaf.stdout ?? '', exitCode: 0 };
    },
  };
}

/**
 * The Go MRTR lock tree (`mrtrLockTree`): a single
 * requires-confirmation leaf, isolated from every other fixture tree.
 * `purge` echoes its `--target` flag, so the accepted round-2 response
 * proves the leaf ran with the arguments the state was bound to rather
 * than merely that it ran.
 */
function mrtrLeaves(): TreeLeaf[] {
  return [
    {
      path: ['purge'],
      description: 'Purge a target',
      properties: { target: str('what to purge') },
      class: { requiresConfirmation: true },
    },
    // Go's `vault burn`: destructive AND requires-confirmation, so a
    // fully accepted MRTR exchange still meets the policy gate behind
    // it. The fixture tree has no such leaf, and a leaf that is merely
    // destructive never enters the confirmation gate at all — which
    // would make the ceiling assertion vacuous.
    {
      path: ['vault', 'burn'],
      description: 'Burn the vault',
      properties: {},
      class: { requiresConfirmation: true, destructive: true },
      stdout: 'burned\n',
    },
  ];
}

/** A bridge over the Go legacy lock tree. */
export function legacyLockBridge(): McpBridge {
  return makeBridge(legacyLeaves);
}

/**
 * A bridge over the Go MRTR lock tree, plus the execution counter the
 * Go tests assert on: the whole point of the first round is that the
 * leaf does NOT run, which only an execution count can prove.
 */
export function mrtrLockBridge(): {
  bridge: McpBridge;
  executions: () => number;
} {
  let executions = 0;
  const inner = makeBridge(mrtrLeaves);
  const bridge: McpBridge = {
    leaves: () => inner.leaves(),
    invoke(inv: Invocation): InvokeResult {
      // The policy gate runs inside `inner.invoke` and throws before
      // returning, so a blocked leaf is never counted as executed.
      const res = inner.invoke(inv) as InvokeResult;
      executions += 1;
      if (inv.path.join('.') !== 'purge') return res;
      const target = inv.flags?.target;
      return {
        ...res,
        stdout: `purged ${typeof target === 'string' ? target : ''}\n`,
      };
    },
  };
  return { bridge, executions: () => executions };
}

/** A bridge over the Go modern lock tree. */
export function modernLockBridge(): McpBridge {
  return makeBridge(modernLeaves);
}

export { SURFACE_MCP };
