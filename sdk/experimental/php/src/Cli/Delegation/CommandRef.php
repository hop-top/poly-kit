<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

/** What the engine reads of a command. */
final readonly class CommandRef
{
    /**
     * @param string $verb command path below the root ("widget add")
     * @param string|null $sideEffect effective tier (declared or inferred)
     */
    public function __construct(
        public string $verb,
        public ?string $sideEffect = null,
        public ?string $permissions = null,
    ) {
    }
}
