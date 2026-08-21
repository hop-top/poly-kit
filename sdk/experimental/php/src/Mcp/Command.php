<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * A node in the bridged command tree.
 *
 * Ports the slice of cobra's `Command` the surface reads. A node with no
 * children and a runner is a leaf; a node with children is a group and is
 * never exposed as a tool.
 */
final class Command
{
    /** @var list<Command> */
    private array $children = [];

    /** @var list<FlagSpec> */
    private array $flags;

    /**
     * Tracks cobra's lazily-registered `--help` flag.
     *
     * Cobra adds a local `help` bool flag to a command the first time it is
     * executed, and the flag then shows up in every later `tools/list`. The
     * wire fixtures capture a list taken before any call and another taken
     * after one, so the two disagree about `ping` — reproducing the
     * mutation is required for byte-exactness, not an implementation
     * detail we are free to skip.
     */
    private bool $helpFlagRegistered = false;

    /**
     * @param array<string, string> $annotations
     * @param list<FlagSpec>        $flags
     */
    public function __construct(
        public readonly string $name,
        public readonly string $description = '',
        array $flags = [],
        public readonly array $annotations = [],
        public readonly ?\Closure $runner = null,
    ) {
        $this->flags = $flags;
    }

    public function addCommand(self ...$children): self
    {
        foreach ($children as $child) {
            $this->children[] = $child;
        }

        return $this;
    }

    /**
     * Children in cobra's listing order — sorted by name.
     *
     * Cobra sorts subcommands alphabetically by default, and that order is
     * what reaches the `tools` array.
     *
     * @return list<Command>
     */
    public function sortedChildren(): array
    {
        $sorted = $this->children;
        usort($sorted, static fn (self $a, self $b): int => strcmp($a->name, $b->name));

        return $sorted;
    }

    public function isLeaf(): bool
    {
        return [] === $this->children && null !== $this->runner;
    }

    /**
     * Flags visible to schema reflection, in pflag's lexicographic order.
     *
     * @return list<FlagSpec>
     */
    public function visibleFlags(): array
    {
        $flags = $this->flags;

        if ($this->helpFlagRegistered) {
            $flags[] = new FlagSpec(
                name: 'help',
                type: 'bool',
                usage: 'help for '.$this->name,
            );
        }

        usort($flags, static fn (FlagSpec $a, FlagSpec $b): int => strcmp($a->name, $b->name));

        return $flags;
    }

    /**
     * Marks this command as executed, registering cobra's implicit `--help`
     * flag exactly once.
     */
    public function markExecuted(): void
    {
        $this->helpFlagRegistered = true;
    }
}
