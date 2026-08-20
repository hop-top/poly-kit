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
 * `helpFlagOn` reproduces a real Go-side artifact the fixtures
 * captured: cobra attaches a command's `--help` flag lazily, on first
 * execution, and the generator drives one long-lived server per era.
 * By the time the later `legacy/protocol-version-header-is-not-modern`
 * case lists tools, the earlier `legacy/tools-call/read` case has
 * executed `ping`, so `ping`'s schema has grown a `help` property
 * that the first `legacy/tools-list` case did not see. The fixtures
 * pin both shapes, so the difference is modelled rather than
 * normalized away.
 */
function makeBridge(
  source: () => TreeLeaf[],
  opts: { helpFlagAfterInvoke?: boolean } = {},
): McpBridge {
  const leaves = source();
  const executed = new Set<string>();
  const policy = defaultPolicy();

  const visible = (): Leaf[] =>
    leaves.map((l) => {
      const key = l.path.join('.');
      if (!opts.helpFlagAfterInvoke || !executed.has(key)) return l;
      return {
        ...l,
        properties: {
          ...l.properties,
          help: bool(`help for ${l.path[l.path.length - 1]}`),
        },
      };
    });

  return {
    leaves: visible,
    invoke(inv: Invocation): InvokeResult {
      const key = inv.path.join('.');
      const leaf = leaves.find((l) => l.path.join('.') === key);
      if (leaf === undefined) {
        throw new UnknownCommandError(`unknown command: ${key}`);
      }
      if (!policyAllowed(policy, leaf.class ?? {}, inv.meta.surface)) {
        throw new DestructiveBlockedError(leaf.path.join(' '), inv.meta.surface);
      }
      executed.add(key);
      return { stdout: leaf.stdout ?? '', exitCode: 0 };
    },
  };
}

/** A bridge over the Go legacy lock tree. */
export function legacyLockBridge(): McpBridge {
  return makeBridge(legacyLeaves, { helpFlagAfterInvoke: true });
}

/** A bridge over the Go modern lock tree. */
export function modernLockBridge(): McpBridge {
  return makeBridge(modernLeaves);
}

export { SURFACE_MCP };
