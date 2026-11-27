<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Throwable;

/** One schema's state: stored version, target and pending count; `$error` when the version could not be read. */
final readonly class SchemaStatus
{
    public function __construct(
        public string $schema,
        public string $current,
        public string $target,
        public int $pending,
        public ?Throwable $error = null,
    ) {}
}
