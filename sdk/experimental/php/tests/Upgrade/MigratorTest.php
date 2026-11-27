<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade;

use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Tests\Upgrade\Support\CollectingPublisher;
use HopTop\Kit\Tests\Upgrade\Support\LockingDriver;
use HopTop\Kit\Tests\Upgrade\Support\RecordingDriver;
use HopTop\Kit\Tests\Upgrade\Support\TempDir;
use HopTop\Kit\Upgrade\EventPublisher;
use HopTop\Kit\Upgrade\MigrateException;
use HopTop\Kit\Upgrade\Migration;
use HopTop\Kit\Upgrade\MigrationTopics;
use HopTop\Kit\Upgrade\Migrator;
use HopTop\Kit\Upgrade\Registry;
use HopTop\Kit\Upgrade\UpgradeErrors;
use HopTop\Kit\Upgrade\UpgradeException;
use HopTop\Kit\Upgrade\VersionFile;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/** What the corpus cannot say: typed errors, the data layout, the PHP-specific seams. */
final class MigratorTest extends TestCase
{
    use TempDir;

    /** @var list<string> */
    private array $calls = [];

    private function rec(): \Closure
    {
        return function (string $e): void {
            $this->calls[] = $e;
        };
    }

    private function migrator(Registry $reg, string $target = '2.0.0', mixed ...$opts): Migrator
    {
        return new Migrator(...(['tool' => 't', 'version' => $target, 'registry' => $reg, 'dataDir' => "{$this->tmp}/data"] + $opts));
    }

    private function mig(string $v, ?\Closure $up = null, bool $down = false, ?\Closure $verify = null): Migration
    {
        return new Migration($v, 'db', $up ?? static function (): void {}, $down ? static function (): void {} : null, $verify);
    }

    #[Test]
    public function aRollbackWithNoBackupIsNotRestoredAndBackupUnsupported(): void
    {
        $reg = new Registry();
        $reg->register($this->mig('1.1.0'));
        $reg->register($this->mig('1.2.0', static fn () => throw new \RuntimeException('boom')));
        $m = $this->migrator($reg);
        $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => '1.0.0', 'backup_unsupported' => true], $this->rec()));
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertSame('migrate db: up 1.2.0 failed: boom (rollback also failed: not restored: no backup was taken)', $e->getMessage());
            self::assertTrue(UpgradeErrors::isNotRestored($e));
            self::assertTrue(UpgradeErrors::isBackupUnsupported($e));
            self::assertFalse(UpgradeErrors::isVerify($e));
            self::assertFalse(UpgradeErrors::isCanceled($e));
            self::assertSame('1.1.0', VersionFile::readIn("{$this->tmp}/data", 'db'), 'version stays at the last record');
        }
    }

    #[Test]
    public function aSuccessfulRollbackIsNeitherNotRestoredNorUnsupported(): void
    {
        $reg = new Registry();
        $reg->register($this->mig('1.1.0', static fn () => throw new \RuntimeException('boom'), down: true));
        $m = $this->migrator($reg);
        $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => '1.0.0'], $this->rec()));
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertFalse(UpgradeErrors::isNotRestored($e));
            self::assertFalse(UpgradeErrors::isBackupUnsupported($e));
            self::assertSame('boom', $e->getPrevious()?->getPrevious()?->getMessage());
        }
    }

    #[Test]
    public function verifyAndCancelAreTypedInTheChain(): void
    {
        $reg = new Registry();
        $reg->register($this->mig('1.1.0', verify: static fn () => throw new \RuntimeException('bad')));
        $m = $this->migrator($reg, '1.1.0', manualRollback: true);
        $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => '1.0.0'], $this->rec()));
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertTrue(UpgradeErrors::isVerify($e));
            self::assertSame('migrate db: verify 1.1.0 failed: bad', $e->getMessage());
        }

        $c = new Cancellation();
        $c->cancel();
        try {
            $m->run($c);
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertTrue(UpgradeErrors::isCanceled($e));
            self::assertSame('migrate db: up 1.1.0 canceled: context canceled', $e->getMessage());
        }
    }

    #[Test]
    public function manualRollbackLatestDetectsNothingToRestore(): void
    {
        $m = $this->migrator(new Registry(), manualRollback: true);
        $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => '1.0.0'], $this->rec()));
        try {
            $m->rollbackLatest();
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertSame('rollback db: no backups found for db', $e->getMessage());
            self::assertTrue(UpgradeErrors::isNotRestored($e));
            self::assertTrue(UpgradeErrors::isBackupUnsupported($e));
        }
    }

    #[Test]
    public function rollbackLatestNeedsManualMode(): void
    {
        $this->expectExceptionMessage('rollback: only available in manual rollback mode');
        $this->migrator(new Registry())->rollbackLatest();
    }

    #[Test]
    public function theLockIsReleasedWhenARunThrowsAnythingAtAll(): void
    {
        $reg = new Registry();
        $reg->register($this->mig('1.1.0', static fn () => throw new \Error('not an Exception')));
        $m = $this->migrator($reg);
        $m->addDriver(new LockingDriver(['name' => 'db', 'version' => '1.0.0', 'lock' => true], $this->rec()));
        try {
            $m->run();
            self::fail('no exception');
        } catch (MigrateException) {
            // an Error from up() is a failed step like any other
        }
        self::assertSame('db unlock', end($this->calls));
    }

    #[Test]
    public function aPublisherThatThrowsNeverFailsARun(): void
    {
        $reg = new Registry();
        $reg->register($this->mig('1.1.0'));
        $pub = new class implements EventPublisher {
            public function publish(string $topic, string $source, array $payload): void
            {
                throw new \RuntimeException('down');
            }
        };
        $m = $this->migrator($reg, events: $pub);
        $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => '1.0.0'], $this->rec()));
        $m->run();
        self::assertCount(1, $m->history());
    }

    #[Test]
    public function topicsCanBeMovedUnderAnotherPrefix(): void
    {
        $reg = new Registry();
        $reg->register($this->mig('1.1.0'));
        $pub = new CollectingPublisher();
        $m = $this->migrator($reg, events: $pub, topics: MigrationTopics::withPrefix('myapp.core.migration'));
        $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => '1.0.0'], $this->rec()));
        $m->run();
        self::assertSame(
            ['myapp.core.migration.run_started', 'myapp.core.migration.started', 'myapp.core.migration.applied', 'myapp.core.migration.run_completed'],
            array_column($pub->events, 'topic'),
        );
        $this->expectException(\InvalidArgumentException::class);
        MigrationTopics::withPrefix('Bad.prefix');
    }

    #[Test]
    public function layoutMatchesTheOtherPorts(): void
    {
        $reg = new Registry();
        $reg->register($this->mig('1.1.0'));
        $m = $this->migrator($reg);
        $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => '1.0.0'], $this->rec()));
        $m->run();
        self::assertSame('1.1.0', file_get_contents("{$this->tmp}/data/db/version"));
        self::assertDirectoryExists("{$this->tmp}/data/backups/db/pre-1.1.0");
        self::assertSame('1.0.0', file_get_contents("{$this->tmp}/data/backups/db/pre-1.1.0.version"));
        self::assertSame(0600, fileperms("{$this->tmp}/data/db/version") & 0777);
    }

    #[Test]
    public function theDefaultDataDirFollowsXdgDataHome(): void
    {
        $old = getenv('XDG_DATA_HOME');
        putenv("XDG_DATA_HOME={$this->tmp}/xdg");
        try {
            self::assertSame("{$this->tmp}/xdg/hop/tool", VersionFile::dataDir('tool'));
            $reg = new Registry();
            $reg->register($this->mig('1.1.0'));
            $m = new Migrator('tool', 'v2.0.0', registry: $reg);
            self::assertSame('2.0.0', $m->version);
            $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => ''], $this->rec()));
            $m->run();
            self::assertSame('1.1.0', VersionFile::read('tool', 'db'));
        } finally {
            putenv($old === false ? 'XDG_DATA_HOME' : "XDG_DATA_HOME=$old");
        }
    }

    #[Test]
    public function aMigrationCancelledMidRunStopsBeforeTheNextStepAndRollsBack(): void
    {
        $c = new Cancellation();
        $reg = new Registry();
        $reg->register($this->mig('1.1.0', static function () use ($c): void {
            $c->cancel();
        }, down: true));
        $reg->register($this->mig('1.2.0'));
        $m = $this->migrator($reg, '1.2.0');
        $m->addDriver(new RecordingDriver(['name' => 'db', 'version' => '1.0.0'], $this->rec()));
        try {
            $m->run($c);
            self::fail('no exception');
        } catch (MigrateException $e) {
            self::assertSame('migrate db: up 1.2.0 canceled: context canceled', $e->getMessage());
            self::assertTrue(UpgradeErrors::isCanceled($e));
        }
        self::assertSame('1.0.0', VersionFile::readIn("{$this->tmp}/data", 'db'));
    }
}
