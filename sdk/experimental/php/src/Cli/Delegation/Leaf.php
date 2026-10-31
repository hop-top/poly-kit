<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

/** Everything a gate decision reads about the leaf being run. */
final readonly class Leaf
{
    /**
     * @param list<string> $path below the root
     * @param array<string, string> $annotations
     */
    public function __construct(
        public string $program,
        public array $path,
        public array $annotations = [],
        public string $format = '',
        public string $confirmToken = '',
    ) {
    }

    public function verb(): string
    {
        return implode(' ', $this->path);
    }

    public function fullPath(): string
    {
        return implode(' ', [$this->program, ...$this->path]);
    }

    public function name(): string
    {
        return [] === $this->path ? '' : $this->path[\count($this->path) - 1];
    }
}
