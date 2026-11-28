<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Driver;

use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Tests\Upgrade\Support\Alarm;
use HopTop\Kit\Tests\Upgrade\Support\Proc;
use HopTop\Kit\Tests\Upgrade\Support\SqliteFixtures;
use HopTop\Kit\Tests\Upgrade\Support\TempDir;
use HopTop\Kit\Upgrade\CanceledException;
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

/** The sqlite driver through the Migrator, its lock, and two real processes. */
final class SqliteRunTest extends TestCase
{
    use SqliteFixtures;
    use TempDir {
        setUp as private tempSetUp;
        tearDown as private tempTearDown;
    }

    private string $path = '';

    private string $data = '';

    private ?PDO $db = null;

    protected function setUp(): void
    {
        $this->tempSetUp();
        $this->path = "{$this->tmp}/app.db";
        $this->data = "{$this->tmp}/data";
        $this->db = self::openWal($this->path);
        self::insert($this->db, 1);
        mkdir("{$this->data}/appdb", 0750, true);
        file_put_contents("{$this->data}/appdb/version", '1.0.0');
    }

    protected function tearDown(): void
    {
        $this->db = null;
        $this->tempTearDown();
    }

    /** 1.1.0 adds row 2; 1.2.0 optionally lets go of the handle, then fails. */
    private function registry(bool $withDown, bool $closeBefore1_2 = false): Registry
    {
        $reg = new Registry();
        $reg->register(new Migration(
            '1.1.0',
            'appdb',
            fn () => self::insert($this->db, 2),
            $withDown ? fn () => $this->db->exec('DELETE FROM t WHERE x = 2') : null,
        ));
        $reg->register(new Migration('1.2.0', 'appdb', function () use ($closeBefore1_2): void {
            if ($closeBefore1_2) {
                $this->db = null; // path-only restore needs no handle on the file
            }
            throw new \RuntimeException('simulated failure');
        }));

        return $reg;
    }

    private function migrator(Registry $reg, Sqlite $d, bool $manual = false): Migrator
    {
        $m = new Migrator('tool', '1.2.0', manualRollback: $manual, registry: $reg, dataDir: $this->data);
        $m->addDriver($d);

        return $m;
    }

    private function driver(bool $handle = false): Sqlite
    {
        return new Sqlite($this->path, name: 'appdb', dataDir: $this->data, db: $handle ? $this->db : null);
    }

    private function version(): string
    {
        return VersionFile::readIn($this->data, 'appdb');
    }

    private function requireLockFree(Sqlite $d): void
    {
        $unlock = $d->lock((new Cancellation()));
        $unlock();
        self::assertTrue(true);
    }

    #[Test]
    public function pathOnlyFailureRestoresTheFile(): void
    {
        $d = $this->driver();
        try {
            $this->migrator($this->registry(false, true), $d)->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertStringContainsString('simulated failure', $e->getMessage());
            self::assertFalse(UpgradeErrors::isNotRestored($e));
        }
        self::assertSame([1], self::rows($this->path), 'restored to the pre-run state');
        foreach (self::sidecars($this->path) as $s) {
            self::assertFileDoesNotExist($s);
        }
        self::assertSame('1.0.0', $this->version());
        self::assertFileExists("{$this->data}/appdb/.lock", 'lock beside the version file');
        $this->requireLockFree($d);
    }

    #[Test]
    public function withAHandleAMissingDownEndsNotRestoredAndKeepsTheBackup(): void
    {
        $d = $this->driver(true);
        try {
            $this->migrator($this->registry(false), $d)->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertTrue(UpgradeErrors::isNotRestored($e));
            self::assertTrue(UpgradeErrors::isBackupUnsupported($e));
            self::assertStringContainsString('simulated failure', $e->getMessage());
        }
        self::assertSame('1.1.0', $this->version(), 'stays at the last step applied');
        self::assertSame([1, 2], self::rows($this->path));
        self::assertSame([1], self::rows("{$this->data}/backups/appdb/pre-1.1.0/app.db"));
        self::assertSame(1, (int) $this->db->query('SELECT 1')->fetchColumn(), "caller's handle closed");
        $this->requireLockFree($d);
    }

    #[Test]
    public function withAHandleAFailedFinalVerifyIsRolledBackByDown(): void
    {
        $reg = new Registry();
        $reg->register(new Migration(
            '1.1.0',
            'appdb',
            fn () => self::insert($this->db, 2),
            fn () => $this->db->exec('DELETE FROM t WHERE x = 2'),
        ));
        $m = new Migrator(
            'tool',
            '1.1.0',
            registry: $reg,
            dataDir: $this->data,
            verify: static fn () => throw new \RuntimeException('final check refused'),
        );
        $m->addDriver($this->driver(true));
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertStringContainsString('final verify 1.1.0 failed: final check refused', $e->getMessage());
            self::assertFalse(UpgradeErrors::isNotRestored($e));
        }
        self::assertSame([1], self::rows($this->path));
        self::assertSame('1.0.0', $this->version());
    }

    #[Test]
    public function manualRollbackPathOnlyRestoresTheBackupAndRecordsTheVersion(): void
    {
        $d = $this->driver();
        $m = $this->migrator($this->registry(false), $d, manual: true);
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException) {
        }
        self::assertSame('1.1.0', $this->version());
        self::assertSame([1, 2], self::rows($this->path));

        $this->db = null; // precondition: no handle on the file
        $m->rollbackLatest();
        self::assertSame([1], self::rows($this->path));
        self::assertSame('1.0.0', $this->version());
        foreach (self::sidecars($this->path) as $s) {
            self::assertFileDoesNotExist($s);
        }
        $this->requireLockFree($d);
    }

    #[Test]
    public function manualRollbackWithAHandleIsNotRestored(): void
    {
        $d = $this->driver(true);
        $m = $this->migrator($this->registry(false), $d, manual: true);
        try {
            $m->run();
        } catch (MigrateException) {
        }
        try {
            $m->rollbackLatest();
            self::fail('no exception');
        } catch (\Throwable $e) {
            self::assertTrue(UpgradeErrors::isNotRestored($e));
            self::assertTrue(UpgradeErrors::isBackupUnsupported($e));
        }
        self::assertSame('1.1.0', $this->version());
        self::assertSame([1, 2], self::rows($this->path));
        self::assertSame(1, (int) $this->db->query('SELECT 1')->fetchColumn());
        $this->requireLockFree($d);
    }

    #[Test]
    public function aSuccessfulHandleRunLeavesABackupWithTheWalRows(): void
    {
        $reg = new Registry();
        $reg->register(new Migration('1.1.0', 'appdb', fn () => self::insert($this->db, 2)));
        $m = new Migrator('tool', '1.1.0', registry: $reg, dataDir: $this->data);
        $m->addDriver($this->driver(true));
        $m->run();
        self::assertSame([1], self::rows("{$this->data}/backups/appdb/pre-1.1.0/app.db"));
        self::assertSame([1, 2], self::rows($this->path));
        self::assertSame('1.1.0', $this->version());
    }

    #[Test]
    public function anAdditiveColumnMigrationBaselinesAPreExistingTable(): void
    {
        // A database from before versioning: the table exists, no version file.
        unlink("{$this->data}/appdb/version");
        $this->db->exec('CREATE TABLE users (name TEXT)');
        $this->db->exec("INSERT INTO users (name) VALUES ('alice')");
        $reg = new Registry();
        $reg->register(new Migration('1.0.0', 'appdb', static function (): void {
            throw new \LogicException('the baseline covers 1.0.0');
        }));
        $reg->register(new Migration('1.1.0', 'appdb', fn () => $this->db->exec("ALTER TABLE users ADD COLUMN email TEXT DEFAULT ''")));
        $m = new Migrator('tool', '1.1.0', registry: $reg, dataDir: $this->data, baseline: static fn (string $schema): string => '1.0.0');
        $m->addDriver($this->driver(true));
        $m->run();

        self::assertSame('', (string) $this->db->query("SELECT email FROM users WHERE name = 'alice'")->fetchColumn());
        self::assertSame('1.1.0', $this->version());
        self::assertFileExists("{$this->data}/backups/appdb/pre-1.1.0/app.db");
    }

    #[Test]
    public function theLockIsAFileBesideTheVersionFileInBothDataDirChoices(): void
    {
        $dir = "{$this->tmp}/other";
        $unlock = (new Sqlite('/x/app.db', name: 'appdb', dataDir: $dir))->lock();
        $unlock();
        self::assertFileExists("$dir/appdb/.lock");

        $old = getenv('XDG_DATA_HOME');
        putenv("XDG_DATA_HOME={$this->tmp}/xdg");
        try {
            $unlock = (new Sqlite('/x/app.db', name: 'appdb', tool: 'mytool'))->lock();
            $unlock();
        } finally {
            putenv($old === false ? 'XDG_DATA_HOME' : "XDG_DATA_HOME=$old");
        }
        self::assertFileExists("{$this->tmp}/xdg/hop/mytool/appdb/.lock");
    }

    #[Test]
    public function aSecondDriverWaitsUntilTheFirstUnlocksAndAnAlreadyCancelledWaitHoldsNothing(): void
    {
        $a = $this->driver();
        $unlockA = $a->lock();
        $cancel = new Cancellation();
        $cancel->cancel();
        try {
            $this->driver()->lock($cancel);
            self::fail('no exception');
        } catch (CanceledException $e) {
            self::assertSame('context canceled', $e->getMessage());
        }
        $unlockA();
        $unlockA(); // idempotent
        $this->requireLockFree($a);

        // free lock, cancelled token: still cancelled, still nothing held
        try {
            $this->driver()->lock($cancel);
            self::fail('no exception');
        } catch (CanceledException) {
        }
        $this->requireLockFree($a);
    }

    #[Test]
    public function aLockWaitCancelledMidWaitReturnsAndHoldsNothing(): void
    {
        if (!Alarm::available()) {
            self::markTestSkipped('pcntl is needed to cancel from a signal');
        }
        $holder = $this->driver();
        $unlock = $holder->lock();
        $cancel = new Cancellation();
        $stop = Alarm::cancelAfter($cancel, 1);
        $start = hrtime(true);
        try {
            $this->driver()->lock($cancel);
            self::fail('no exception');
        } catch (CanceledException) {
        } finally {
            $stop();
        }
        $ms = (hrtime(true) - $start) / 1e6;
        self::assertGreaterThan(800, $ms, 'it did wait');
        self::assertLessThan(3000, $ms);
        $unlock();
        $this->requireLockFree($holder);
    }

    #[Test]
    public function anotherProcessHoldingFlockOnThePathTheGoDriverLocksExcludesThisDriver(): void
    {
        // The Go driver locks <data dir>/<schema name>/.lock with flock(2): the path is spelled out here, not derived.
        $goPath = "{$this->data}/appdb/.lock";
        $p = new Proc('FlockHolder.php', [$goPath]);
        self::assertSame('ready', $p->readLine());

        $cancel = new Cancellation();
        $stop = Alarm::available() ? Alarm::cancelAfter($cancel, 1) : static function (): void {};
        if (!Alarm::available()) {
            $cancel->cancel();
        }
        try {
            $this->driver()->lock($cancel);
            self::fail('lock acquired while another process holds the Go driver\'s file');
        } catch (CanceledException) {
        } finally {
            $stop();
        }

        [$code] = $p->finish(); // the holder exits, releasing flock
        self::assertSame(0, $code);
        $this->requireLockFree($this->driver());
    }

    #[Test]
    public function twoProcessesRacingOneFileApplyEachMigrationOnce(): void
    {
        $this->db->exec('CREATE TABLE applied (version TEXT, pid INTEGER)');
        $this->db = null;
        $procs = [new Proc('SqliteRaceHelper.php', [$this->tmp]), new Proc('SqliteRaceHelper.php', [$this->tmp])];
        foreach ($procs as $i => $p) {
            [$code, $out] = $p->finish();
            self::assertSame(0, $code, "helper $i:\n$out");
            self::assertStringContainsString('done', $out);
        }
        $db = new PDO("sqlite:{$this->path}", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        $got = array_count_values($db->query('SELECT version FROM applied')->fetchAll(PDO::FETCH_COLUMN));
        self::assertSame(['1.1.0' => 1, '1.2.0' => 1], $got);
        self::assertSame('1.2.0', $this->version());
    }
}
