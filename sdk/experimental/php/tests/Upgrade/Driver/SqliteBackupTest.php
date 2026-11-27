<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Driver;

use HopTop\Kit\Tests\Upgrade\Support\SqliteFixtures;
use HopTop\Kit\Tests\Upgrade\Support\TempDir;
use HopTop\Kit\Upgrade\BackupUnsupported;
use HopTop\Kit\Upgrade\Driver\Sqlite;
use HopTop\Kit\Upgrade\UpgradeErrors;
use HopTop\Kit\Upgrade\UpgradeException;
use PDO;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/** Backup and restore of the sqlite driver, in both modes. */
final class SqliteBackupTest extends TestCase
{
    use SqliteFixtures;
    use TempDir;

    /** @return array<string, array{bool}> */
    public static function modes(): array
    {
        return ['path only' => [false], 'with db' => [true]];
    }

    #[Test]
    #[DataProvider('modes')]
    public function backupContainsRowsStillInTheWal(bool $withDb): void
    {
        $path = "{$this->tmp}/app.db";
        $db = self::openWal($path);
        self::insert($db, 1, 2, 3);
        self::assertGreaterThan(0, filesize("$path-wal"), 'rows must sit in the WAL');

        (new Sqlite($path, db: $withDb ? $db : null))->backup("{$this->tmp}/backup");

        self::assertSame([1, 2, 3], self::rows("{$this->tmp}/backup/app.db"));
        self::assertSame('ok', self::integrity("{$this->tmp}/backup/app.db"));
        self::assertFileDoesNotExist("{$this->tmp}/backup/app.db-wal", 'backup is one file');
        self::assertFileDoesNotExist("{$this->tmp}/backup/app.db.tmp", 'no temp file left');
    }

    /** @return array<string, array{string}> */
    public static function oddNames(): array
    {
        $odd = ['with space', "o'brien", 'q"uote', 'a?b#c%d', 'x;DROP TABLE t;--', 'semi;colon', ':memory:'];

        return array_combine($odd, array_map(static fn (string $s): array => [$s], $odd));
    }

    #[Test]
    #[DataProvider('oddNames')]
    public function oddPathsStayData(string $odd): void
    {
        $dir = "{$this->tmp}/$odd";
        mkdir($dir, 0750, true);
        $path = "$dir/my db'.sqlite";
        $db = self::openWal($path);
        self::insert($db, 7);

        $bk = "$dir/back up/$odd";
        (new Sqlite($path))->backup($bk);
        self::assertSame([7], self::rows("$bk/my db'.sqlite"));

        (new Sqlite($path, db: $db))->backup($bk); // second backup replaces the first
        self::assertSame([7], self::rows("$bk/my db'.sqlite"));
        self::assertSame([7], self::rows($path), 'source untouched');
    }

    #[Test]
    public function aStaleBackupAndTempFileAreReplaced(): void
    {
        $path = "{$this->tmp}/app.db";
        $db = self::openWal($path);
        self::insert($db, 1);
        mkdir("{$this->tmp}/bk");
        file_put_contents("{$this->tmp}/bk/app.db", 'stale');
        file_put_contents("{$this->tmp}/bk/app.db.tmp", 'stale');
        (new Sqlite($path))->backup("{$this->tmp}/bk");
        self::assertSame([1], self::rows("{$this->tmp}/bk/app.db"));
        self::assertFileDoesNotExist("{$this->tmp}/bk/app.db.tmp");
    }

    #[Test]
    public function aMissingDatabaseFileMeansNothingToBackUp(): void
    {
        (new Sqlite("{$this->tmp}/none.db"))->backup("{$this->tmp}/bk");
        self::assertDirectoryDoesNotExist("{$this->tmp}/bk");
        self::assertFileDoesNotExist("{$this->tmp}/none.db", 'the driver must not create the database');
    }

    #[Test]
    public function aFileThatIsNotADatabaseFailsAndLeavesNoTemp(): void
    {
        file_put_contents("{$this->tmp}/app.db", str_repeat('not a database ', 100));
        try {
            (new Sqlite("{$this->tmp}/app.db"))->backup("{$this->tmp}/bk");
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringStartsWith('backup db: ', $e->getMessage());
        }
        self::assertFileDoesNotExist("{$this->tmp}/bk/app.db.tmp");
        self::assertFileDoesNotExist("{$this->tmp}/bk/app.db");
    }

    #[Test]
    public function handleModeNeverClosesOrReconfiguresTheCallersHandle(): void
    {
        $path = "{$this->tmp}/app.db";
        $db = self::openWal($path);
        self::insert($db, 1);
        $db->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_WARNING);
        $db->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_NUM);
        $db->setAttribute(PDO::ATTR_TIMEOUT, 3);

        (new Sqlite($path, db: $db))->backup("{$this->tmp}/bk");

        self::assertSame(PDO::ERRMODE_WARNING, $db->getAttribute(PDO::ATTR_ERRMODE));
        self::assertSame(PDO::FETCH_NUM, $db->getAttribute(PDO::ATTR_DEFAULT_FETCH_MODE));
        self::assertFalse($db->inTransaction());
        self::insert($db, 2); // still open and usable
        self::assertSame([1, 2], self::rows($path));
    }

    #[Test]
    public function aSilentHandleReportsFailureAsAnExceptionWithoutChangingItsMode(): void
    {
        $path = "{$this->tmp}/app.db";
        $db = self::openWal($path);
        $db->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_SILENT);
        $db->beginTransaction(); // VACUUM INTO fails inside a transaction
        try {
            (new Sqlite($path, db: $db))->backup("{$this->tmp}/bk");
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringContainsString('transaction', $e->getMessage());
        }
        self::assertSame(PDO::ERRMODE_SILENT, $db->getAttribute(PDO::ATTR_ERRMODE));
        self::assertTrue($db->inTransaction(), 'left as the caller had it');
        self::assertFileDoesNotExist("{$this->tmp}/bk/app.db.tmp");
    }

    #[Test]
    public function handleModeWithoutAPathNamesTheBackupAfterTheSchema(): void
    {
        $db = self::openWal("{$this->tmp}/app.db");
        self::insert($db, 5);
        (new Sqlite('', name: 'appdb', db: $db))->backup("{$this->tmp}/bk");
        self::assertSame([5], self::rows("{$this->tmp}/bk/appdb.db"));
    }

    #[Test]
    public function pathOnlyRestoreReturnsTheEarlierStateWithoutSidecars(): void
    {
        $path = "{$this->tmp}/app.db";
        $db = self::openWal($path);
        self::insert($db, 1, 2);
        $d = new Sqlite($path);
        $d->backup("{$this->tmp}/bk");

        self::insert($db, 3, 4); // later write, still only in the WAL
        mkdir("{$this->tmp}/crash");
        copy($path, "{$this->tmp}/crash/app.db");
        copy("$path-wal", "{$this->tmp}/crash/app.db-wal");
        $db = null; // precondition for restore: no handle (closing may checkpoint; rebuild the crashed state)
        unlink($path);
        foreach (self::sidecars($path) as $s) {
            @unlink($s);
        }
        copy("{$this->tmp}/crash/app.db", $path);
        copy("{$this->tmp}/crash/app.db-wal", "$path-wal");
        file_put_contents("$path-shm", 'stale');
        file_put_contents("$path-journal", 'stale');

        $d->restore("{$this->tmp}/bk");

        foreach (self::sidecars($path) as $s) {
            self::assertFileDoesNotExist($s, "stale sidecar $s");
        }
        self::assertSame([1, 2], self::rows($path));
        self::assertSame('ok', self::integrity($path));
        self::assertSame([1, 2], self::rows("{$this->tmp}/bk/app.db"), 'backup kept');
        foreach ((array) scandir($this->tmp) as $n) {
            self::assertStringNotContainsString('.restore-', (string) $n, 'temp file left');
        }
    }

    #[Test]
    public function restoreKeepsThePermissions(): void
    {
        $path = "{$this->tmp}/app.db";
        $db = self::openWal($path);
        self::insert($db, 1);
        (new Sqlite($path))->backup("{$this->tmp}/bk");
        $db = null;
        chmod($path, 0640);
        (new Sqlite($path))->restore("{$this->tmp}/bk");
        self::assertSame(0640, fileperms($path) & 0777);
    }

    #[Test]
    public function aMissingBackupFailsAndLeavesTheDatabase(): void
    {
        $path = "{$this->tmp}/app.db";
        $db = self::openWal($path);
        self::insert($db, 1);
        $db = null;
        try {
            (new Sqlite($path))->restore("{$this->tmp}/none");
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringStartsWith('open backup: ', $e->getMessage());
            self::assertFalse(UpgradeErrors::isBackupUnsupported($e));
        }
        self::assertSame([1], self::rows($path), 'database untouched');
    }

    #[Test]
    public function handleModeRestoreIsUnsupportedAndTouchesNothing(): void
    {
        $path = "{$this->tmp}/app.db";
        $db = self::openWal($path);
        self::insert($db, 1);
        $d = new Sqlite($path, db: $db);
        $d->backup("{$this->tmp}/bk");
        self::insert($db, 2);

        try {
            $d->restore("{$this->tmp}/bk");
            self::fail('no exception');
        } catch (BackupUnsupported $e) {
            self::assertTrue(UpgradeErrors::isBackupUnsupported($e));
        }
        self::assertSame([1, 2], self::rows($path), 'database untouched');
        self::assertSame([1], self::rows("{$this->tmp}/bk/app.db"), 'backup kept');
        self::assertSame(PDO::ERRMODE_EXCEPTION, $db->getAttribute(PDO::ATTR_ERRMODE));
    }
}
