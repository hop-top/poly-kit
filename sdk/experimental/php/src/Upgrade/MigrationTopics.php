<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use InvalidArgumentException;

/**
 * The topics a migrator publishes on. Run level: `runStarted`, then
 * `runCompleted` or `runFailed`. `baselined` follows a baseline stamp of an
 * unversioned schema, before that schema's migrations. Per migration:
 * `started`, then `applied` and `verified`, or `failed`; `reverted` follows
 * a rollback that succeeded. An empty topic is not published.
 */
final readonly class MigrationTopics
{
    public function __construct(
        public string $runStarted = 'kit.core.migration.run_started',
        public string $runCompleted = 'kit.core.migration.run_completed',
        public string $runFailed = 'kit.core.migration.run_failed',
        public string $started = 'kit.core.migration.started',
        public string $applied = 'kit.core.migration.applied',
        public string $verified = 'kit.core.migration.verified',
        public string $failed = 'kit.core.migration.failed',
        public string $reverted = 'kit.core.migration.reverted',
        public string $baselined = 'kit.core.migration.baselined',
    ) {}

    /**
     * Every topic under a 3-segment prefix (`myapp.core.migration`); a
     * segment is lowercase letters, digits or underscores.
     */
    public static function withPrefix(string $prefix): self
    {
        if (preg_match('/^[a-z0-9_]+\.[a-z0-9_]+\.[a-z0-9_]+$/', $prefix) !== 1) {
            throw new InvalidArgumentException("migration topic prefix \"$prefix\" must be 3 segments of lowercase letters, digits or underscores");
        }

        return new self(
            "$prefix.run_started",
            "$prefix.run_completed",
            "$prefix.run_failed",
            "$prefix.started",
            "$prefix.applied",
            "$prefix.verified",
            "$prefix.failed",
            "$prefix.reverted",
            "$prefix.baselined",
        );
    }
}
