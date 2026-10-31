<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli;

/** A leaf-local flag. `type` is `string` (takes a value) or `bool`. */
final readonly class FlagDef
{
    public function __construct(
        public string $long,
        public string $help = '',
        public string $type = 'string',
    ) {
    }

    public function takesValue(): bool
    {
        return 'bool' !== $this->type;
    }
}
