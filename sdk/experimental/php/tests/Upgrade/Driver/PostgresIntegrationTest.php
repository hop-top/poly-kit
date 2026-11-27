<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Driver;

use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Tests\Upgrade\Support\Alarm;
use HopTop\Kit\Tests\Upgrade\Support\PgFixtures;
use HopTop\Kit\Tests\Upgrade\Support\Proc;
use HopTop\Kit\Tests\Upgrade\Support\TempDir;
use HopTop\Kit\Upgrade\BackupUnsupported;
use HopTop\Kit\Upgrade\CanceledException;
use HopTop\Kit\Upgrade\Driver\Postgres;
use HopTop\Kit\Upgrade\MigrateException;
use HopTop\Kit\Upgrade\Migration;
use HopTop\Kit\Upgrade\Migrator;
use HopTop\Kit\Upgrade\Registry;
use HopTop\Kit\Upgrade\UpgradeErrors;
use HopTop\Kit\Upgrade\UpgradeException;
use PDO;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * The postgres driver against a real Postgres 16 (`make test-php-upgrade-integration`).
 * Excluded from the default run: it needs Docker.
 */
#[Group('integration')]
final class PostgresIntegrationTest extends TestCase
{
    use PgFixtures;
    use TempDir;

    private function driver(string $table, string $name = 'app', ?PDO $db = null): Postgres
    {
        return new Postgres($db ?? self::pg(), $table, static fn (): PDO => self::pg(), name: $name);
    }

    /** @return \Closure(): void the unlock; fails the test when the lock is not granted at once */
    private function lockAtOnce(Postgres $d): \Closure
    {
        $cancel = new Cancellation();
        $stop = Alarm::available() ? Alarm::cancelAfter($cancel, 2) : static function (): void {};
        try {
            return $d->lock($cancel);
        } catch (CanceledException) {
            self::fail('a second session must acquire the lock at once');
        } finally {
            $stop();
        }
    }

    private function migrator(Registry $reg, Postgres $d, bool $manual = false): Migrator
    {
        $m = new Migrator('pgtest', '1.2.0', manualRollback: $manual, registry: $reg, dataDir: "{$this->tmp}/data");
        $m->addDriver($d);

        return $m;
    }

    /** 1.1.0 applies; 1.2.0 fails; no Down anywhere. */
    private function failingRegistry(): Registry
    {
        $reg = new Registry();
        $reg->register(new Migration('1.1.0', 'app', static function (): void {}));
        $reg->register(new Migration('1.2.0', 'app', static fn () => throw new \RuntimeException('boom')));

        return $reg;
    }

    #[Test]
    public function versionRoundTripAndUpsert(): void
    {
        $t = self::table();
        $d = $this->driver($t, 'myschema');
        $other = $this->driver($t, 'other');
        self::assertSame('', $d->version());
        $d->setVersion('1.0.0');
        $other->setVersion('3.0.0');
        $d->setVersion('2.0.0');
        self::assertSame('2.0.0', $d->version());
        self::assertSame('3.0.0', $other->version());
        self::assertSame(1, (int) self::scalar(self::pg(), "SELECT count(*) FROM $t WHERE schema_name = 'myschema'"), 'upsert keeps one row per schema');
        self::assertSame('2.0.0', $this->driver($t, 'myschema')->version(), 'a second driver over the table keeps the data');
        self::assertTrue($d->storesVersion());
        self::assertSame('myschema', $d->name());
    }

    #[Test]
    public function theTableIsExactlyTheOneTheGoDriverCreates(): void
    {
        $t = self::table();
        $this->driver($t);
        $db = self::pg();
        $cols = $db->query("SELECT column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '') FROM information_schema.columns WHERE table_name = '$t' ORDER BY ordinal_position")->fetchAll(PDO::FETCH_COLUMN);
        self::assertSame(['schema_name text NO ', "version text NO ''::text"], $cols);
        self::assertSame('p', self::scalar($db, "SELECT contype FROM pg_constraint WHERE conrelid = '$t'::regclass"), 'schema_name is the primary key');
    }

    #[Test]
    public function concurrentConstructionOverOneNewTableSucceeds(): void
    {
        $t = self::table();
        $a = self::pg();
        $b = self::pg();
        // Two sessions inside the DDL transaction at once would collide in the catalog without the advisory lock.
        $this->driver($t, 'a', $a);
        $this->driver($t, 'b', $b);
        self::assertSame('', $this->driver($t, 'a')->version());
    }

    #[Test]
    public function aCallerPdoOfAnyErrorModeWorksAndIsLeftAsItWas(): void
    {
        foreach ([PDO::ERRMODE_SILENT, PDO::ERRMODE_WARNING, PDO::ERRMODE_EXCEPTION] as $mode) {
            $db = self::pg();
            $db->setAttribute(PDO::ATTR_ERRMODE, $mode);
            $d = $this->driver(self::table(), 'app', $db);
            $d->setVersion('1.0.0');
            self::assertSame('1.0.0', $d->version());
            self::assertSame($mode, $db->getAttribute(PDO::ATTR_ERRMODE));
            self::assertSame(1, (int) self::scalar($db, 'SELECT 1'), 'the driver never closes the handle');
        }
        // In silent mode a failing statement returns false: the driver still reports it.
        $db = self::pg();
        $db->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_SILENT);
        $t = self::table();
        $d = $this->driver($t, 'app', $db);
        $db->exec("DROP TABLE $t");
        try {
            $d->setVersion('1.0.0');
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringContainsString('postgres upgrade: set version:', $e->getMessage());
        }
        self::assertSame(PDO::ERRMODE_SILENT, $db->getAttribute(PDO::ATTR_ERRMODE));
    }

    #[Test]
    public function backupAndRestoreAreUnsupported(): void
    {
        $d = $this->driver(self::table());
        foreach ([fn () => $d->backup("{$this->tmp}/b"), fn () => $d->restore("{$this->tmp}/b")] as $call) {
            try {
                $call();
                self::fail('no exception');
            } catch (BackupUnsupported $e) {
                self::assertTrue(UpgradeErrors::isBackupUnsupported($e));
            }
        }
    }

    #[Test]
    public function twoProcessesRacingOneDatabaseApplyEachMigrationOnce(): void
    {
        $t = self::table();
        $db = self::pg();
        $db->exec("CREATE TABLE {$t}_applied (version TEXT, pid INT)");
        $procs = [new Proc('PgRaceHelper.php', [$t, 'app']), new Proc('PgRaceHelper.php', [$t, 'app'])];
        usleep(700_000); // both past their DDL and waiting on stdin
        foreach ($procs as $p) {
            fwrite($p->stdin(), "go\n");
        }
        foreach ($procs as $i => $p) {
            [$code, $out] = $p->finish();
            self::assertSame(0, $code, "racer $i:\n$out");
            self::assertStringContainsString('done', $out);
        }
        $got = array_count_values($db->query("SELECT version FROM {$t}_applied")->fetchAll(PDO::FETCH_COLUMN));
        self::assertSame(['1.1.0' => 1, '1.2.0' => 1, '1.3.0' => 1], $got);
        self::assertSame('1.3.0', $this->driver($t)->version());
    }

    #[Test]
    public function theLockIsReleasedAfterAFailedRunAndASecondSessionAcquiresAtOnce(): void
    {
        $t = self::table();
        try {
            $this->migrator($this->failingRegistry(), $this->driver($t))->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertStringContainsString('boom', $e->getMessage());
        }
        ($this->lockAtOnce($this->driver($t)))();
        self::assertTrue(self::keyIsFree(Postgres::lockKey($t, 'app')));
    }

    #[Test]
    public function aMissingDownEndsNotRestoredAndTheRowKeepsTheLastStep(): void
    {
        $t = self::table();
        $d = $this->driver($t);
        $d->setVersion('1.0.0');
        try {
            $this->migrator($this->failingRegistry(), $d)->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertTrue(UpgradeErrors::isNotRestored($e));
            self::assertTrue(UpgradeErrors::isBackupUnsupported($e));
        }
        self::assertSame('1.1.0', $d->version());
    }

    #[Test]
    public function manualRollbackLatestFindsNoBackupAndRestoresNothing(): void
    {
        $t = self::table();
        $d = $this->driver($t);
        $d->setVersion('1.0.0');
        $m = $this->migrator($this->failingRegistry(), $d, manual: true);
        try {
            $m->run();
        } catch (MigrateException) {
        }
        try {
            $m->rollbackLatest();
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertTrue(UpgradeErrors::isNotRestored($e));
        }
        self::assertSame('1.1.0', $d->version(), 'nothing was restored');
        ($this->lockAtOnce($this->driver($t)))(); // rollbackLatest released its lock
    }

    #[Test]
    public function aRawSessionHoldingTheExactKeyTheGoDriverComputesExcludesTheDriver(): void
    {
        if (!Alarm::available()) {
            self::markTestSkipped('pcntl is needed to cancel from a signal');
        }
        $t = self::table();
        $d = $this->driver($t, 'app');
        // The key text the Go driver hashes, spelled out: kit-upgrade:<table>:<schema name>.
        $raw = self::pg();
        self::assertTrue((bool) self::scalar($raw, 'SELECT pg_try_advisory_lock(hashtextextended(?, 0))', "kit-upgrade:$t:app"));

        $cancel = new Cancellation();
        $stop = Alarm::cancelAfter($cancel, 1);
        $began = hrtime(true);
        try {
            $d->lock($cancel);
            self::fail('acquired while a raw session holds the key');
        } catch (CanceledException) {
        } finally {
            $stop();
        }
        self::assertGreaterThan(800, (hrtime(true) - $began) / 1e6, 'it waited');

        self::scalar($raw, 'SELECT pg_advisory_unlock(hashtextextended(?, 0))', "kit-upgrade:$t:app");
        ($this->lockAtOnce($d))();
    }

    #[Test]
    public function aCancelledWaitHoldsNothing(): void
    {
        $t = self::table();
        $a = $this->driver($t);
        $unlockA = $a->lock();
        $cancel = new Cancellation();
        $cancel->cancel();
        try {
            $this->driver($t)->lock($cancel);
            self::fail('no exception');
        } catch (CanceledException $e) {
            self::assertSame('context canceled', $e->getMessage());
        }
        $unlockA();
        $unlockA(); // runs once
        self::assertTrue(self::keyIsFree(Postgres::lockKey($t, 'app')), 'the cancelled waiter took nothing');
        ($this->lockAtOnce($this->driver($t)))();
    }

    #[Test]
    public function aWaiterAcquiresAsSoonAsTheHolderReleases(): void
    {
        if (!Alarm::available()) {
            self::markTestSkipped('pcntl is needed to cancel from a signal');
        }
        $t = self::table();
        $a = $this->driver($t);
        $unlockA = $a->lock();
        $cancel = new Cancellation();
        $stop = Alarm::cancelAfter($cancel, 1);
        try {
            $this->driver($t)->lock($cancel);
            self::fail('no exception');
        } catch (CanceledException) {
        } finally {
            $stop();
        }
        $unlockA();
        ($this->lockAtOnce($this->driver($t)))();
    }

    #[Test]
    public function theLockConnectionMustNotBeTheCallersSession(): void
    {
        $t = self::table();
        $db = self::pg();
        $d = new Postgres($db, $t, static fn (): PDO => $db, name: 'app');
        try {
            $d->lock();
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringContainsString('lock needs a connection of its own', $e->getMessage());
            self::assertStringContainsString('backend pid ' . self::pid($db), $e->getMessage());
        }
        self::assertSame(1, (int) self::scalar($db, 'SELECT 1'), "the caller's session is untouched");
        self::assertTrue(self::keyIsFree(Postgres::lockKey($t, 'app')));

        // Two persistent connections to one DSN are one session too.
        $p1 = self::pg(true);
        $d = new Postgres($p1, self::table(), static fn (): PDO => self::pg(true), name: 'app');
        $this->expectException(UpgradeException::class);
        $this->expectExceptionMessage('lock needs a connection of its own');
        $d->lock();
    }

    #[Test]
    public function theMigratorReportsAnOwnSessionRefusalAsALockError(): void
    {
        $db = self::pg();
        $d = new Postgres($db, self::table(), static fn (): PDO => $db, name: 'app');
        try {
            $this->migrator($this->failingRegistry(), $d)->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertStringContainsString('lock:', $e->getMessage());
        }
    }

    #[Test]
    public function aPersistentLockConnectionIsUnlockedExplicitlyBeforeItIsReleased(): void
    {
        $t = self::table();
        $key = Postgres::lockKey($t, 'app');
        $d = new Postgres(self::pg(), $t, static fn (): PDO => self::pg(true, 'kit_php_lock'), name: 'app');
        $unlock = $d->lock();
        self::assertFalse(self::keyIsFree($key), 'held');
        $unlock();
        // The pooled session lives on in this process, so only the explicit unlock can have freed the key.
        self::assertTrue(self::keyIsFree($key), 'freed by pg_advisory_unlock, not by dropping the object');
        $alive = self::scalar(self::pg(), "SELECT count(*) FROM pg_stat_activity WHERE application_name = 'kit_php_lock'");
        self::assertSame(1, (int) $alive, 'the persistent session itself is still open: the pooling hazard is real');
    }

    #[Test]
    public function aFailedUnlockEndsThePersistentSessionAndFreesTheKey(): void
    {
        $t = self::table();
        $key = Postgres::lockKey($t, 'app');
        $held = null;
        $d = new Postgres(self::pg(), $t, static function () use (&$held): PDO {
            return $held = self::pg(true, 'kit_php_fail');
        }, name: 'app');
        $unlock = $d->lock();
        $pid = self::pid($held);
        $held->exec('SELECT pg_advisory_unlock_all()'); // the lock is gone: the unlock reports false
        try {
            $unlock();
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringContainsString('unlock: lock was not held by this session', $e->getMessage());
        }
        $probe = self::pg();
        $deadline = microtime(true) + 5;
        while (self::alive($probe, $pid) && microtime(true) < $deadline) {
            usleep(50_000);
        }
        self::assertFalse(self::alive($probe, $pid), 'the session behind the failed unlock was terminated');
        $unlock(); // runs once
        self::assertTrue(self::keyIsFree($key));
    }

    #[Test]
    public function anUnlockThatErrorsBecauseTheSessionWasKilledStillFreesTheKey(): void
    {
        $t = self::table();
        $d = $this->driver($t);
        $unlock = $d->lock();
        $killed = (int) self::scalar(self::pg(), "SELECT count(pg_terminate_backend(pid)) FROM pg_locks WHERE locktype = 'advisory' AND granted AND ((classid::bigint << 32) | objid::bigint) = hashtextextended(?, 0)", Postgres::lockKey($t, 'app'));
        self::assertSame(1, $killed, 'one session holds the lock');
        try {
            $unlock();
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringContainsString('postgres upgrade: unlock:', $e->getMessage());
        }
        ($this->lockAtOnce($this->driver($t)))();
    }
}
