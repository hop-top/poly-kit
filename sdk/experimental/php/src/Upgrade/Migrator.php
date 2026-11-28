<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Closure;
use Exception;
use HopTop\Kit\Serve\Cancellation;
use Throwable;

/**
 * Runs versioned migrations across schema drivers.
 *
 * Per schema, in order: take the driver's lock when it is a `Locker` (held
 * until the schema's run ends, rollback included), read the version (when
 * none is stored, ask the baseline closure and record its answer), collect
 * the pending migrations, take a backup (and save the version the run starts
 * at beside it), then per migration `up`, `verify` and the version record,
 * then the final verify. A `backup` that throws `BackupUnsupported` lets the
 * run go on with no backup. On failure, unless `manualRollback`: `down` in
 * reverse (recording the version before each), else `restore` and record the
 * starting version. A migration whose `up` failed counts as applied but
 * cannot be downed (its `down` is never called): the run restores the backup
 * at once and no older `down` runs. When a `down` is missing or fails, or an
 * `up` failed, and no backup can restore the schema, the exception chain
 * holds `NotRestored` (`UpgradeErrors::isNotRestored()`) and the stored
 * version stays at the last step still applied.
 *
 * Cancellation is cooperative: the `Cancellation` given to `run()` is
 * checked before each `up`. A migration cancelled mid-way is not interrupted.
 *
 * A crash re-applies at most the one migration whose `up` had not been
 * recorded, so migrations should stay idempotent.
 */
final class Migrator
{
    private const DEFAULT_RETENTION = 5;

    public readonly string $version;

    private readonly Registry $registry;
    private readonly MigrationTopics $topics;
    private readonly int $retention;

    /** @var list<SchemaDriver> */
    private array $drivers = [];

    /** @var list<AppliedMigration> */
    private array $history = [];

    /** @var Closure(string): void */
    private readonly Closure $warn;

    private readonly MigrationEvents $events;

    /**
     * @param string $tool names the data dir (`$XDG_DATA_HOME/hop/<tool>`)
     * @param string $version the target; one leading `v` is dropped
     * @param bool $manualRollback do not roll back on failure; `rollbackLatest()` does it on request
     * @param int $backupRetention backups kept per schema; 0 means 5
     * @param ?Registry $registry defaults to `Registry::default()`
     * @param ?string $dataDir keeps version files and backups here instead of the default
     * @param (Closure(string, string): void)|null $verify `fn(schema, version)` checks a schema's final state, throws on failure
     * @param (Closure(string): string)|null $baseline `fn(schema)` returns what an unversioned store is at ('' leaves it unversioned), throws on failure
     * @param (Closure(string): void)|null $warn receives each warning line; defaults to `error_log`
     */
    public function __construct(
        public readonly string $tool,
        string $version,
        private readonly bool $manualRollback = false,
        int $backupRetention = 0,
        ?Registry $registry = null,
        private readonly ?string $dataDir = null,
        private readonly ?Closure $verify = null,
        private readonly ?Closure $baseline = null,
        ?EventPublisher $events = null,
        ?MigrationTopics $topics = null,
        ?Closure $warn = null,
    ) {
        $this->events = new MigrationEvents($events);
        $this->version = str_starts_with($version, 'v') ? substr($version, 1) : $version;
        $this->retention = $backupRetention > 0 ? $backupRetention : self::DEFAULT_RETENTION;
        $this->registry = $registry ?? Registry::default();
        $this->topics = $topics ?? new MigrationTopics();
        $this->warn = $warn ?? static function (string $line): void {
            error_log($line);
        };
    }

    public function addDriver(SchemaDriver $driver): void
    {
        $this->drivers[] = $driver;
    }

    /**
     * Runs pending migrations for every driver, in the order added; stops at
     * the first schema that fails.
     *
     * @throws MigrateException
     */
    public function run(?Cancellation $cancel = null): void
    {
        $start = hrtime(true);
        $this->events->emit($this->topics->runStarted, ['tool' => $this->tool, 'to' => $this->version]);
        $applied = 0;
        foreach ($this->drivers as $d) {
            try {
                $applied += $this->runSchema($d, $cancel);
            } catch (UpgradeException $e) {
                $err = new MigrateException("migrate {$d->name()}: {$e->getMessage()}", $d->name(), $e);
                $this->events->emit($this->topics->runFailed, [
                    'tool' => $this->tool, 'to' => $this->version, 'schema' => $d->name(),
                    'error' => $err->getMessage(), 'duration_ms' => MigrationEvents::millis($start),
                ]);
                throw $err;
            }
        }
        $this->events->emit($this->topics->runCompleted, [
            'tool' => $this->tool, 'to' => $this->version, 'applied' => $applied, 'duration_ms' => MigrationEvents::millis($start),
        ]);
    }

    /**
     * Pending migration counts per driver. Takes no lock and never calls the
     * baseline closure.
     *
     * @return list<SchemaStatus>
     */
    public function status(): array
    {
        $out = [];
        foreach ($this->drivers as $d) {
            $err = null;
            $stored = '';
            try {
                $stored = $d->version();
            } catch (Throwable $e) {
                $err = $e;
            }
            $pending = $this->registry->pending($d->name(), $stored, $this->version);
            $out[] = new SchemaStatus($d->name(), $stored, $this->version, count($pending), $err);
        }

        return $out;
    }

    /** @return list<AppliedMigration> migrations applied by this migrator */
    public function history(): array
    {
        return $this->history;
    }

    /**
     * Restores the latest backup of each driver, then records the version the
     * run that took it started at (saved beside the backup). A backup without
     * that record is still restored, but the version is left as it is and the
     * exception says so. A `Locker` is locked around its schema's rollback
     * (lock, backup lookup, restore, record, unlock); the version is not read
     * and no events are published. A schema with no backup, or whose restore
     * is unsupported, fails with `NotRestored` (and `BackupUnsupported`) in
     * the chain; nothing is recorded for it. Manual mode only.
     *
     * @throws UpgradeException
     */
    public function rollbackLatest(): void
    {
        if (!$this->manualRollback) {
            throw new UpgradeException('rollback: only available in manual rollback mode');
        }
        foreach ($this->drivers as $d) {
            try {
                $this->rollbackSchema($d);
            } catch (UpgradeException $e) {
                throw new MigrateException("rollback {$d->name()}: {$e->getMessage()}", $d->name(), $e);
            }
        }
    }

    private function rollbackSchema(SchemaDriver $d): void
    {
        $release = $this->lockDriver($d, null);
        try {
            $store = $this->store($d->name());
            $latest = $store->latestBackup();
            [$preRun, $recorded] = $store->readPreRun($latest);
            $this->restore($d, $latest);
            if (!$recorded) {
                throw new UpgradeException('restored ' . basename($latest) . ' but it records no pre-run version; version left as is');
            }
            try {
                $this->setVersion($d, $preRun);
            } catch (Throwable $e) {
                throw StepFailure::record($preRun, $e);
            }
        } finally {
            $release();
        }
    }

    /** `restore` with an unsupported restore as `NotRestored`. */
    private function restore(SchemaDriver $d, string $src): void
    {
        try {
            $d->restore($src);
        } catch (BackupUnsupported $e) {
            throw new NotRestored('not restored: restore unsupported', 0, $e);
        } catch (Throwable $e) {
            throw $e instanceof UpgradeException ? $e : new UpgradeException($e->getMessage(), 0, $e);
        }
    }

    /** Takes the driver's lock when it has one; the result releases it and never throws. */
    private function lockDriver(SchemaDriver $d, ?Cancellation $cancel): Closure
    {
        if (!$d instanceof Locker) {
            return static function (): void {};
        }
        try {
            $unlock = $d->lock($cancel);
        } catch (Throwable $e) {
            throw new UpgradeException("lock: {$e->getMessage()}", 0, $e);
        }

        return function () use ($unlock, $d): void {
            try {
                $unlock();
            } catch (Exception $e) {
                ($this->warn)("warning: unlock {$d->name()}: {$e->getMessage()}");
            }
        };
    }

    private function runSchema(SchemaDriver $d, ?Cancellation $cancel): int
    {
        $release = $this->lockDriver($d, $cancel);
        try {
            return $this->runLocked($d, $cancel);
        } finally {
            $release();
        }
    }

    private function runLocked(SchemaDriver $d, ?Cancellation $cancel): int
    {
        try {
            $current = $d->version();
        } catch (Throwable $e) {
            throw new UpgradeException("read version: {$e->getMessage()}", 0, $e);
        }
        if ($current === '' && $this->baseline !== null) {
            $current = $this->stampBaseline($d);
        }
        $pending = $this->registry->pending($d->name(), $current, $this->version);
        if ($pending === []) {
            return 0;
        }

        $store = $this->store($d->name());
        $bk = $this->takeBackup($store, $d, $pending[0]->version, $current);
        try {
            $n = $this->applyAll($d, $current, $pending, $cancel);
        } catch (StepFailure $f) {
            throw $this->fail($d, $current, $bk, $f);
        }
        if ($bk['path'] !== '') {
            try {
                $store->prune();
            } catch (UpgradeException $e) {
                ($this->warn)("warning: prune old backups for {$d->name()}: {$e->getMessage()}");
            }
        }

        return $n;
    }

    private function store(string $schema): BackupStore
    {
        return new BackupStore($this->resolvedDataDir(), $schema, $this->retention);
    }

    /**
     * Makes the backup directory (named after the first pending version),
     * asks the driver to fill it and saves the starting version beside it. An
     * unsupported backup is no failure: the directory this call created is
     * removed again and the run goes on with no backup.
     *
     * @return array{path: string, unsupported: ?Throwable}
     */
    private function takeBackup(BackupStore $store, SchemaDriver $d, string $first, string $current): array
    {
        $existed = $store->hasBackup($first);
        try {
            $path = $store->backupDir($first);
        } catch (UpgradeException $e) {
            throw new UpgradeException("prepare backup: {$e->getMessage()}", 0, $e);
        }
        try {
            $d->backup($path);
        } catch (BackupUnsupported $e) {
            if (!$existed) {
                try {
                    FsUtil::removeAll($path);
                } catch (UpgradeException) {
                    // the empty directory is left behind; the run goes on
                }
            }

            return ['path' => '', 'unsupported' => $e];
        } catch (Throwable $e) {
            throw new UpgradeException("backup: {$e->getMessage()}", 0, $e);
        }
        try {
            $store->savePreRun($path, $current);
        } catch (UpgradeException $e) {
            throw new UpgradeException("prepare backup: {$e->getMessage()}", 0, $e);
        }

        return ['path' => $path, 'unsupported' => null];
    }

    /** Asks the baseline closure what an unversioned store is at, records a non-empty answer, returns the version the run continues from. */
    private function stampBaseline(SchemaDriver $d): string
    {
        try {
            $v = (string) ($this->baseline)($d->name());
        } catch (Throwable $e) {
            throw new UpgradeException("baseline: {$e->getMessage()}", 0, $e);
        }
        if ($v === '') {
            return '';
        }
        try {
            $this->setVersion($d, $v);
        } catch (Throwable $e) {
            $r = StepFailure::record($v, $e);
            throw new UpgradeException("baseline: {$r->getMessage()}", 0, $r);
        }
        $this->events->emit($this->topics->baselined, ['tool' => $this->tool, 'schema' => $d->name(), 'from' => '', 'to' => $v]);

        return $v;
    }

    /**
     * `up`, `verify` and the version record for each pending migration, then
     * the final verify.
     *
     * @param list<Migration> $pending
     * @throws StepFailure
     */
    private function applyAll(SchemaDriver $d, string $current, array $pending, ?Cancellation $cancel): int
    {
        $schema = $d->name();
        $from = $current;
        $applied = [];
        foreach ($pending as $mig) {
            $ev = ['tool' => $this->tool, 'schema' => $schema, 'from' => $from, 'to' => $mig->version];
            if ($cancel !== null && $cancel->cancelled()) {
                throw $this->failed($ev, 'canceled', new CanceledException(), $applied);
            }
            $this->events->emit($this->topics->started, $ev);
            $t = hrtime(true);
            try {
                ($mig->up)();
            } catch (Throwable $e) {
                // a failed up is an applied step that cannot be downed: it joins the undo list, newest
                throw $this->failed($ev + ['duration_ms' => MigrationEvents::millis($t)], 'up', $e, [...$applied, $mig], true);
            }
            $this->events->emit($this->topics->applied, $ev + ['duration_ms' => MigrationEvents::millis($t)]);
            if ($mig->verify !== null) {
                $t = hrtime(true);
                try {
                    ($mig->verify)();
                } catch (Throwable $e) {
                    // up completed: the rollback must undo this step too
                    throw $this->failed($ev + ['duration_ms' => MigrationEvents::millis($t)], 'verify', $e, [...$applied, $mig]);
                }
                $this->events->emit($this->topics->verified, $ev + ['duration_ms' => MigrationEvents::millis($t)]);
            }
            $t = hrtime(true);
            try {
                $this->setVersion($d, $mig->version);
            } catch (Throwable $e) {
                throw $this->failed($ev + ['duration_ms' => MigrationEvents::millis($t)], 'record', $e, [...$applied, $mig]);
            }
            $applied[] = $mig;
            $this->history[] = new AppliedMigration($mig->version, $mig->schema);
            $from = $mig->version;
        }
        if ($this->verify !== null) {
            $ev = ['tool' => $this->tool, 'schema' => $schema, 'from' => $current, 'to' => $from];
            $t = hrtime(true);
            try {
                ($this->verify)($schema, $from);
            } catch (Throwable $e) {
                throw $this->failed($ev + ['duration_ms' => MigrationEvents::millis($t)], 'final', $e, $applied);
            }
            $this->events->emit($this->topics->verified, $ev + ['duration_ms' => MigrationEvents::millis($t), 'final' => true]);
        }

        return count($applied);
    }

    /**
     * @param array<string, mixed> $ev
     * @param list<Migration> $undo
     */
    private function failed(array $ev, string $stage, Throwable $err, array $undo, bool $upFailed = false): StepFailure
    {
        $this->events->emit($this->topics->failed, $ev + ['stage' => $stage, 'error' => $err->getMessage()]);

        return new StepFailure($stage, (string) $ev['to'], (string) $ev['from'], $err, $undo, $upFailed);
    }

    /**
     * Rolls back (in auto mode) and builds the schema's exception.
     *
     * @param array{path: string, unsupported: ?Throwable} $bk
     */
    private function fail(SchemaDriver $d, string $current, array $bk, StepFailure $f): UpgradeException
    {
        $err = $f->toException();
        if ($this->manualRollback || $f->undo === []) {
            return $err;
        }
        $t = hrtime(true);
        try {
            $method = $this->rollback($d, $current, $f->undo, $f->upFailed, $bk);
        } catch (UpgradeException $rb) {
            return new MigrateException("{$err->getMessage()} (rollback also failed: {$rb->getMessage()})", '', $err, $rb);
        }
        $this->events->emit($this->topics->reverted, [
            'tool' => $this->tool, 'schema' => $d->name(), 'from' => $f->version, 'to' => $current,
            'method' => $method, 'duration_ms' => MigrationEvents::millis($t),
        ]);

        return $err;
    }

    /**
     * `down` in reverse order, recording the version before each migration
     * once its `down` succeeded; falls back to `restore` (and records the
     * starting version) when a `down` is missing or fails. Returns which of
     * the two undid the changes. When `$upFailed`, the newest step's `up`
     * failed: its `down` is never called (a `down` assumes a completed `up`),
     * so the rollback restores at once and no older `down` runs. A failed
     * record stops the rollback. With no backup, or an unsupported restore,
     * nothing is restored: `NotRestored`, and the version stays at the last
     * record, the step whose `down` was missing, failed or not callable.
     *
     * @param list<Migration> $applied
     * @param array{path: string, unsupported: ?Throwable} $bk
     */
    private function rollback(SchemaDriver $d, string $current, array $applied, bool $upFailed, array $bk): string
    {
        for ($i = count($applied) - 1; $i >= 0; $i--) {
            $mig = $applied[$i];
            if (($upFailed && $i === count($applied) - 1) || !$this->down($mig)) {
                if ($bk['path'] === '') {
                    throw new NotRestored('not restored: no backup was taken', 0, $bk['unsupported']);
                }
                $this->restore($d, $bk['path']);
                try {
                    $this->setVersion($d, $current);
                } catch (Throwable $e) {
                    throw StepFailure::record($current, $e);
                }

                return 'restore';
            }
            $prev = $i > 0 ? $applied[$i - 1]->version : $current;
            try {
                $this->setVersion($d, $prev);
            } catch (Throwable $e) {
                throw StepFailure::record($prev, $e);
            }
        }

        return 'down';
    }

    /** Runs a migration's `down`; false when there is none or it threw. */
    private function down(Migration $mig): bool
    {
        if ($mig->down === null) {
            return false;
        }
        try {
            ($mig->down)();
        } catch (Throwable) {
            return false;
        }

        return true;
    }

    /**
     * Records a version for the driver. A `VersionStorer` owns its version:
     * only `setVersion` is called, no version file is touched. Any other
     * driver gets the version file first (the migrator's canonical copy;
     * drivers read it back with `VersionFile`) and then `setVersion` as a
     * notification, so a refused `setVersion` leaves the file at the new
     * version.
     */
    private function setVersion(SchemaDriver $d, string $version): void
    {
        if ($d instanceof VersionStorer && $d->storesVersion()) {
            $d->setVersion($version);

            return;
        }
        VersionFile::write($this->resolvedDataDir(), $d->name(), $version);
        $d->setVersion($version);
    }

    private function resolvedDataDir(): string
    {
        return $this->dataDir !== null && $this->dataDir !== '' ? $this->dataDir : VersionFile::dataDir($this->tool);
    }
}
