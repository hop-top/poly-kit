<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli;

/** What a leaf handler sees. */
final class Invocation
{
    /**
     * @param list<string> $path command path below the root
     * @param array<string, string|bool> $flags the leaf's own flags that were set
     * @param list<string> $args positionals
     * @param resource $stdout
     * @param resource $stderr
     */
    public function __construct(
        public readonly array $path,
        public readonly array $flags,
        public readonly array $args,
        public readonly mixed $stdout,
        public readonly mixed $stderr,
        private readonly bool $dryRun = false,
    ) {
    }

    /** Whether kit applied `--dry-run` to this run. */
    public function isDryRun(): bool
    {
        return $this->dryRun;
    }
}
