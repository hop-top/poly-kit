<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli;

use Closure;

/**
 * One node of a kit root's command tree. A node with children is a group;
 * a node without is a leaf and runs `$handler`.
 *
 * `kit/*` annotations ({@see SideEffect}) sit on the leaf; the gate reads
 * them, so an author declares the tier once and gets the delegation gates
 * for free.
 */
final readonly class CommandSpec
{
    /**
     * @param array<string, string> $annotations
     * @param list<FlagDef> $flags
     * @param list<CommandSpec> $children
     * @param (Closure(Invocation): (int|void))|null $handler
     */
    public function __construct(
        public string $name,
        public string $about = '',
        public array $annotations = [],
        public array $flags = [],
        public array $children = [],
        public ?Closure $handler = null,
    ) {
    }

    public function isLeaf(): bool
    {
        return [] === $this->children;
    }

    public function child(string $name): ?self
    {
        foreach ($this->children as $c) {
            if ($c->name === $name) {
                return $c;
            }
        }

        return null;
    }

    public function flag(string $long): ?FlagDef
    {
        foreach ($this->flags as $f) {
            if ($f->long === $long) {
                return $f;
            }
        }

        return null;
    }
}
