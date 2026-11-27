<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

use PDO;

/**
 * Connections to the Postgres the integration target started. The target
 * exports `KIT_UPGRADE_PG_DSN`, `KIT_UPGRADE_PG_USER` and `KIT_UPGRADE_PG_PASSWORD`;
 * without them, or without `pdo_pgsql`, the tests fail: running this group
 * and checking nothing is not a pass.
 */
trait PgFixtures
{
    private static function requireServer(): void
    {
        self::assertTrue(extension_loaded('pdo_pgsql'), 'pdo_pgsql is required by the integration group');
        self::assertNotSame(false, getenv('KIT_UPGRADE_PG_DSN'), 'KIT_UPGRADE_PG_DSN is not set: run `make test-php-upgrade-integration`');
    }

    private static function pg(bool $persistent = false, string $appName = ''): PDO
    {
        self::requireServer();
        $dsn = (string) getenv('KIT_UPGRADE_PG_DSN') . ($appName !== '' ? ";options='--application_name=$appName'" : '');

        return new PDO($dsn, (string) getenv('KIT_UPGRADE_PG_USER'), (string) getenv('KIT_UPGRADE_PG_PASSWORD'), [
            PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
            PDO::ATTR_PERSISTENT => $persistent,
        ]);
    }

    /** A valid, unique table name. */
    private static function table(): string
    {
        return 'v_' . bin2hex(random_bytes(6));
    }

    private static function scalar(PDO $c, string $sql, mixed ...$params): mixed
    {
        $st = $c->prepare($sql);
        $st->execute($params);

        return $st->fetchColumn();
    }

    private static function pid(PDO $c): int
    {
        return (int) self::scalar($c, 'SELECT pg_backend_pid()');
    }

    private static function alive(PDO $probe, int $pid): bool
    {
        return (bool) self::scalar($probe, 'SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = ?)', $pid);
    }

    /** Whether a fresh session can take the advisory lock for the key text right now (and releases it again). */
    private static function keyIsFree(string $keyText): bool
    {
        $c = self::pg();
        $got = (bool) self::scalar($c, 'SELECT pg_try_advisory_lock(hashtextextended(?, 0))', $keyText);
        if ($got) {
            self::scalar($c, 'SELECT pg_advisory_unlock(hashtextextended(?, 0))', $keyText);
        }

        return $got;
    }
}
