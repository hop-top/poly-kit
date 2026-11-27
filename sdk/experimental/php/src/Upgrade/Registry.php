<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * Holds migrations for one or more migrators.
 *
 * `Registry::default()` is the process-wide registry a migrator reads unless
 * it is given its own; `new Registry()` is an empty one that ignores it.
 */
final class Registry
{
    private static ?self $default = null;

    /** @var list<Migration> */
    private array $migrations = [];

    public static function default(): self
    {
        return self::$default ??= new self();
    }

    public function register(Migration $migration): void
    {
        $this->migrations[] = $migration;
    }

    /** Drops every migration. For tests. */
    public function reset(): void
    {
        $this->migrations = [];
    }

    /**
     * Migrations of `$schema` where `$from < version <= $to`, ascending by
     * semver, versions as registered. Ties have no defined order.
     *
     * @return list<Migration>
     */
    public function pending(string $schema, string $from, string $to): array
    {
        $fromV = Semver::parse($from);
        $toV = Semver::parse($to);
        $keep = [];
        foreach ($this->migrations as $m) {
            if ($m->schema !== $schema) {
                continue;
            }
            $v = Semver::parse($m->version);
            if ($v->compareTo($fromV) > 0 && $v->compareTo($toV) <= 0) {
                $keep[] = [$v, $m];
            }
        }
        usort($keep, static fn (array $a, array $b): int => $a[0]->compareTo($b[0]));

        return array_map(static fn (array $p): Migration => $p[1], $keep);
    }
}
