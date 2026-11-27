<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

use PDO;

/** Helpers for the sqlite driver tests; needs `TempDir`. */
trait SqliteFixtures
{
    /** A WAL-mode database with `t(x)`, checkpointing never, so committed rows stay in the -wal file. */
    private static function openWal(string $path): PDO
    {
        $db = new PDO("sqlite:$path", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        $db->exec('PRAGMA journal_mode=WAL');
        $db->exec('PRAGMA wal_autocheckpoint=0');
        $db->exec('CREATE TABLE IF NOT EXISTS t (x INTEGER)');

        return $db;
    }

    private static function insert(PDO $db, int ...$xs): void
    {
        foreach ($xs as $x) {
            $db->prepare('INSERT INTO t (x) VALUES (?)')->execute([$x]);
        }
    }

    /** @return list<int> t(x) in order, read on a connection of its own */
    private static function rows(string $path): array
    {
        $db = new PDO("sqlite:$path", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        $out = array_map('intval', $db->query('SELECT x FROM t ORDER BY x')->fetchAll(PDO::FETCH_COLUMN));
        $db = null;

        return $out;
    }

    private static function integrity(string $path): string
    {
        $db = new PDO("sqlite:$path", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);

        return (string) $db->query('PRAGMA integrity_check')->fetchColumn();
    }

    /** @return list<string> */
    private static function sidecars(string $db): array
    {
        return ["$db-wal", "$db-shm", "$db-journal"];
    }
}
