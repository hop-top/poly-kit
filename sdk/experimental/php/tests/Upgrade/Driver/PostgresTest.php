<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Driver;

use HopTop\Kit\Upgrade\Driver\Postgres;
use HopTop\Kit\Upgrade\UpgradeException;
use PDO;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/** The parts of the postgres driver that need no server (the rest: PostgresIntegrationTest). */
final class PostgresTest extends TestCase
{
    /** @return array<string, array{string}> */
    public static function badTables(): array
    {
        $bad = ['', '1abc', 'a-b', 'a b', 'a;DROP TABLE x', 'schema.table', str_repeat('a', 65), "ok\n", 'tab"le'];

        return array_combine(array_map(static fn (string $s): string => json_encode($s, JSON_THROW_ON_ERROR), $bad), array_map(static fn (string $s): array => [$s], $bad));
    }

    #[Test]
    #[DataProvider('badTables')]
    public function anInvalidTableNameIsRefusedBeforeAnyIo(string $table): void
    {
        // A PDO that fails the test if it is touched: sqlite has no pg_advisory_xact_lock, and no table may appear.
        $db = new PDO('sqlite::memory:', null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        try {
            new Postgres($db, $table, static fn (): PDO => $db);
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertSame(sprintf('invalid table name: "%s"', $table), $e->getMessage());
        }
        self::assertSame(0, (int) $db->query('SELECT count(*) FROM sqlite_master')->fetchColumn());
    }

    #[Test]
    public function theLockKeyTextIsTheOneTheGoDriverHashes(): void
    {
        self::assertSame('kit-upgrade:schema_versions:app', Postgres::lockKey('schema_versions', 'app'));
    }
}
