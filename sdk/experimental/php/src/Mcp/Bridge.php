<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Exposes a command tree to the surfaces, applying the safety policy.
 *
 * Ports Go's `Bridge`. The leaf set is discovered once at construction —
 * so commands added later are not exposed — while each leaf's flags are
 * read live at list time, which is what lets the lazily-registered
 * `--help` flag appear in a later `tools/list`.
 */
final class Bridge
{
    /** @var array<string, Leaf> keyed by dotted tool name */
    private array $leaves = [];

    public function __construct(
        private readonly Command $root,
        private readonly Policy $policy = new Policy(),
    ) {
        $this->discover($root, []);
    }

    /**
     * Depth-first walk registering every runnable leaf.
     *
     * Children are visited in cobra's sorted order, which fixes the order
     * of the emitted `tools` array.
     *
     * @param list<string> $path
     */
    private function discover(Command $node, array $path): void
    {
        foreach ($node->sortedChildren() as $child) {
            $childPath = [...$path, $child->name];

            if ($child->isLeaf()) {
                $leaf = new Leaf(
                    path: $childPath,
                    description: $child->description,
                    flags: $child->visibleFlags(),
                    class: SafetyClass::classify($child->annotations),
                    enabled: $this->defaultEnabled(),
                    runner: $child->runner,
                );
                $this->leaves[$leaf->toolName()] = $leaf;
                $this->commands[$leaf->toolName()] = $child;
                continue;
            }

            $this->discover($child, $childPath);
        }
    }

    /** @var array<string, Command> keyed by dotted tool name */
    private array $commands = [];

    /**
     * Surfaces a leaf is exposed on when its config omits the field.
     *
     * @return array<string, bool>
     */
    private function defaultEnabled(): array
    {
        $enabled = [];
        foreach ($this->policy->resolvedDefaults() as $surface) {
            $enabled[$surface->value] = true;
        }

        return $enabled;
    }

    /**
     * Leaves in discovery order.
     *
     * Flags are re-read from the live command so that post-execution
     * mutations are reflected.
     *
     * @return list<Leaf>
     */
    public function leaves(): array
    {
        $out = [];
        foreach ($this->leaves as $name => $leaf) {
            $out[] = new Leaf(
                path: $leaf->path,
                description: $leaf->description,
                flags: $this->commands[$name]->visibleFlags(),
                class: $leaf->class,
                enabled: $leaf->enabled,
                runner: $leaf->runner,
            );
        }

        return $out;
    }

    /**
     * Resolves a dotted tool name to its leaf, or null when unknown.
     */
    public function resolveLeaf(string $toolName): ?Leaf
    {
        $leaf = $this->leaves[$toolName] ?? null;
        if (null === $leaf) {
            return null;
        }

        return new Leaf(
            path: $leaf->path,
            description: $leaf->description,
            flags: $this->commands[$toolName]->visibleFlags(),
            class: $leaf->class,
            enabled: $leaf->enabled,
            runner: $leaf->runner,
        );
    }

    /**
     * Invokes a leaf on the given surface.
     *
     * The policy gate runs before the runner, so a destructive leaf blocked
     * on a remote surface never executes.
     *
     * @param array<string, mixed> $arguments
     *
     * @throws UnknownCommandException  when the tool name resolves to nothing
     * @throws SurfaceNotEnabledException when the leaf is not exposed on $surface
     * @throws DestructiveBlockedException when the policy gate refuses the call
     */
    public function invoke(string $toolName, array $arguments, Surface $surface): Result
    {
        $leaf = $this->resolveLeaf($toolName);
        if (null === $leaf) {
            throw new UnknownCommandException('cmdsurface: unknown command: '.$toolName);
        }

        if (!$leaf->enabledOn($surface)) {
            throw new SurfaceNotEnabledException(
                'cmdsurface: surface not enabled for command: '.$leaf->pathKey().' on '.$surface->value,
            );
        }

        if (!$this->policy->allowed($leaf->class, $surface)) {
            throw new DestructiveBlockedException(
                'cmdsurface: destructive command blocked on this surface: '
                .$leaf->pathKey().' on '.$surface->value,
            );
        }

        $command = $this->commands[$toolName];
        $command->markExecuted();

        $runner = $leaf->runner;

        return null === $runner ? new Result() : $runner($arguments);
    }
}
