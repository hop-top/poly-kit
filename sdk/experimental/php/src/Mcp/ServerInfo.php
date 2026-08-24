<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The mount's advertised identity.
 *
 * Reported by the legacy `initialize` result and by the modern result
 * `_meta`, which must agree.
 */
final readonly class ServerInfo
{
    public function __construct(
        public string $name = 'cmdsurface',
        public string $version = '0.0.0',
    ) {
    }

    /** @return array{name: string, version: string} */
    public function toArray(): array
    {
        return ['name' => $this->name, 'version' => $this->version];
    }
}
