<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Driver;

use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Tests\Upgrade\Support\CollectingPublisher;
use HopTop\Kit\Tests\Upgrade\Support\SqliteFixtures;
use HopTop\Kit\Tests\Upgrade\Support\TempDir;
use HopTop\Kit\Upgrade\Driver\Sqlite;
use HopTop\Kit\Upgrade\MigrateException;
use HopTop\Kit\Upgrade\Migration;
use HopTop\Kit\Upgrade\Migrator;
use HopTop\Kit\Upgrade\Registry;
use HopTop\Kit\Upgrade\UpgradeErrors;
use HopTop\Kit\Upgrade\VersionFile;
use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/** A failed up through the sqlite driver: the failing step cannot be downed, so the backup is restored. */
final class SqliteFailedUpTest extends TestCase
{
    use SqliteFixtures;
    use TempDir {
        setUp as private tempSetUp;
    }

    private string $path = '';

    private string $data = '';

    protected function setUp(): void
    {
        $this->tempSetUp();
        $this->path = "{$this->tmp}/app.db";
        $this->data = "{$this->tmp}/data";
        $db = self::openWal($this->path);
        self::insert($db, 1);
        $db = null; // path-only restore needs no handle on the file
        mkdir("{$this->data}/appdb", 0750, true);
        file_put_contents("{$this->data}/appdb/version", '1.0.0');
    }

    /** An up that changes the schema on its own connection, then fails: a partial change. */
    private function addColumnThenFail(string $col): \Closure
    {
        return function () use ($col): void {
            $db = new PDO("sqlite:{$this->path}", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
            $db->exec("ALTER TABLE t ADD COLUMN $col TEXT");
            $db = null;

            throw new \RuntimeException('boom');
        };
    }

    private function insertRow(int $x): \Closure
    {
        return function () use ($x): void {
            $db = new PDO("sqlite:{$this->path}", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
            self::insert($db, $x);
        };
    }

    /** @return list<string> */
    private function columns(): array
    {
        $db = new PDO("sqlite:{$this->path}", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        $out = array_map('strval', $db->query("SELECT name FROM pragma_table_info('t') ORDER BY cid")->fetchAll(PDO::FETCH_COLUMN));
        $db = null;

        return $out;
    }

    /** @return list<string> the backup directories only (not the .version sidecars) */
    private function backupDirs(): array
    {
        $root = "{$this->data}/backups/appdb";
        $out = [];
        foreach (scandir($root) ?: [] as $n) {
            if ($n !== '.' && $n !== '..' && is_dir("$root/$n")) {
                $out[] = $n;
            }
        }
        sort($out);

        return $out;
    }

    /** @param list<Migration> $migs */
    private function migrator(array $migs, ?CollectingPublisher $pub = null): Migrator
    {
        $reg = new Registry();
        foreach ($migs as $mig) {
            $reg->register($mig);
        }

        return new Migrator('tool', '1.2.0', registry: $reg, dataDir: $this->data, events: $pub);
    }

    private function revertedMethod(CollectingPublisher $pub): ?string
    {
        foreach ($pub->events as $ev) {
            if ($ev['topic'] === 'kit.core.migration.reverted') {
                return (string) $ev['payload']['method'];
            }
        }

        return null;
    }

    private function requireLockFree(Sqlite $d): void
    {
        $unlock = $d->lock(new Cancellation());
        $unlock();
        self::assertTrue(true);
    }

    #[Test]
    public function aFailedFirstUpRestoresTheBackup(): void
    {
        $d = new Sqlite($this->path, name: 'appdb', dataDir: $this->data);
        $pub = new CollectingPublisher();
        $m = $this->migrator([new Migration('1.1.0', 'appdb', $this->addColumnThenFail('note'))], $pub);
        $m->addDriver($d);
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertStringContainsString('up 1.1.0 failed: boom', $e->getMessage());
            self::assertFalse(UpgradeErrors::isNotRestored($e));
        }
        self::assertNotContains('note', $this->columns(), 'the partial change is undone');
        self::assertSame([1], self::rows($this->path));
        self::assertSame('1.0.0', VersionFile::readIn($this->data, 'appdb'));
        self::assertSame(['pre-1.1.0'], $this->backupDirs(), 'the backup stays');
        self::assertSame('restore', $this->revertedMethod($pub));
        $this->requireLockFree($d);
    }

    #[Test]
    public function aFailedLaterUpRestoresWithoutRunningAnyDown(): void
    {
        $downs = 0;
        $d = new Sqlite($this->path, name: 'appdb', dataDir: $this->data);
        $pub = new CollectingPublisher();
        $m = $this->migrator([
            new Migration('1.1.0', 'appdb', $this->insertRow(2), function () use (&$downs): void {
                $downs++;
            }),
            new Migration('1.2.0', 'appdb', $this->addColumnThenFail('note')),
        ], $pub);
        $m->addDriver($d);
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertStringContainsString('up 1.2.0 failed: boom', $e->getMessage());
            self::assertFalse(UpgradeErrors::isNotRestored($e));
        }
        self::assertSame(0, $downs, 'no down runs when the newest step cannot be downed');
        self::assertNotContains('note', $this->columns());
        self::assertSame([1], self::rows($this->path));
        self::assertSame('1.0.0', VersionFile::readIn($this->data, 'appdb'));
        self::assertSame('restore', $this->revertedMethod($pub));
    }

    #[Test]
    public function aFailedUpWithAHandleIsNotRestoredAndRunsNoDown(): void
    {
        $db = new PDO("sqlite:{$this->path}", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        $downs = 0;
        $d = new Sqlite($this->path, name: 'appdb', dataDir: $this->data, db: $db);
        $pub = new CollectingPublisher();
        $m = $this->migrator([
            new Migration('1.1.0', 'appdb', fn () => self::insert($db, 2), function () use (&$downs): void {
                $downs++;
            }),
            new Migration('1.2.0', 'appdb', static fn () => throw new \RuntimeException('boom')),
        ], $pub);
        $m->addDriver($d);
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertTrue(UpgradeErrors::isNotRestored($e));
            self::assertTrue(UpgradeErrors::isBackupUnsupported($e));
            self::assertStringContainsString('up 1.2.0 failed: boom', $e->getMessage());
        }
        self::assertSame(0, $downs);
        self::assertSame('1.1.0', VersionFile::readIn($this->data, 'appdb'), 'stays at the last step applied');
        self::assertSame([1, 2], self::rows($this->path));
        self::assertNull($this->revertedMethod($pub), 'nothing was reverted');
    }
}
