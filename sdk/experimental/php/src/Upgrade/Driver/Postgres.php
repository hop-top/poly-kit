<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade\Driver;

use Closure;
use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Upgrade\BackupUnsupported;
use HopTop\Kit\Upgrade\CanceledException;
use HopTop\Kit\Upgrade\Locker;
use HopTop\Kit\Upgrade\SchemaDriver;
use HopTop\Kit\Upgrade\UpgradeException;
use HopTop\Kit\Upgrade\VersionStorer;
use PDO;
use PDOException;
use PDOStatement;

/**
 * Schema driver that keeps its version in a PostgreSQL table, over a PDO the
 * caller opens (`pdo_pgsql`); kit depends on no Postgres client library. Same
 * table, SQL and lock key as the Go driver, so both exclude each other.
 *
 * `$db` does the version reads and writes and is never closed or
 * reconfigured (any error mode works). `$connect` returns a connection for
 * the lock: `lock()` calls it once per run and holds that session until
 * unlock, so it must return a session of its own each time (not `$db`, not a
 * persistent connection to the same DSN: both are refused). No
 * transaction-mode pooler in front of the database.
 *
 * `backup` and `restore` throw `BackupUnsupported`: a run goes on without a
 * backup and a failed `up`, or a `down` that is missing or fails, ends
 * `NotRestored`: keep each `up` atomic (one transaction).
 */
final class Postgres implements SchemaDriver, VersionStorer, Locker
{
    private const TABLE_RULE = '/^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/D';

    /** SQL shared verbatim with the Go driver (`?` for its `$1`: PDO numbers the parameters). */
    private const SQL_TRY_LOCK = 'SELECT pg_try_advisory_lock(hashtextextended(?, 0))';
    private const SQL_UNLOCK = 'SELECT pg_advisory_unlock(hashtextextended(?, 0))';
    private const SQL_XACT_LOCK = 'SELECT pg_advisory_xact_lock(hashtextextended(?, 0))';
    private const LOCK_POLL_US = 50_000;

    /** @var Closure(): PDO */
    private readonly Closure $connect;

    /**
     * Validates `$table`, then creates it when absent (under an advisory
     * lock, so concurrent drivers do not collide in the catalog).
     *
     * @param string $table matches `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`
     * @param callable(): PDO $connect a new connection to the same database for the lock
     * @throws UpgradeException on an invalid table name or when the table cannot be created
     */
    public function __construct(
        private readonly PDO $db,
        private readonly string $table,
        callable $connect,
        private readonly string $name = 'postgres',
    ) {
        if (preg_match(self::TABLE_RULE, $table) !== 1) {
            throw new UpgradeException(sprintf('invalid table name: "%s"', $table));
        }
        if (!extension_loaded('pdo_pgsql')) {
            throw new UpgradeException('the postgres upgrade driver needs the pdo_pgsql extension');
        }
        $this->connect = Closure::fromCallable($connect);
        try {
            $this->ensureTable();
        } catch (UpgradeException $e) {
            throw new UpgradeException("postgres upgrade: migrate: {$e->getMessage()}", 0, $e);
        }
    }

    public function name(): string
    {
        return $this->name;
    }

    public function storesVersion(): bool
    {
        return true;
    }

    /** The stored version; no row means "". */
    public function version(): string
    {
        try {
            $st = self::run($this->db, "SELECT version FROM {$this->table} WHERE schema_name = ?", [$this->name]);
            $v = $st->fetchColumn();
        } catch (UpgradeException $e) {
            throw new UpgradeException("postgres upgrade: version: {$e->getMessage()}", 0, $e);
        }

        return $v === false ? '' : (string) $v;
    }

    /** Upserts the row for this schema. */
    public function setVersion(string $version): void
    {
        try {
            self::run(
                $this->db,
                "INSERT INTO {$this->table} (schema_name, version) VALUES (?, ?) "
                . 'ON CONFLICT (schema_name) DO UPDATE SET version = EXCLUDED.version',
                [$this->name, $version],
            );
        } catch (UpgradeException $e) {
            throw new UpgradeException("postgres upgrade: set version: {$e->getMessage()}", 0, $e);
        }
    }

    public function backup(string $dest): void
    {
        throw new BackupUnsupported();
    }

    public function restore(string $src): void
    {
        throw new BackupUnsupported();
    }

    /** The text hashed (server side) into a run's advisory lock key. */
    public static function lockKey(string $table, string $name): string
    {
        return "kit-upgrade:$table:$name";
    }

    /**
     * Takes a session-level advisory lock on a connection from `$connect` and
     * holds that session until the returned unlock runs. The wait polls
     * `pg_try_advisory_lock` every 50ms (a blocked `pg_advisory_lock` cannot
     * be interrupted from PHP), so `$cancel` is honoured between tries and a
     * cancelled wait holds nothing (`CanceledException`).
     *
     * The unlock runs `pg_advisory_unlock` on that same connection; when it
     * returns false or fails, the session is ended (see `endSession`) and the
     * unlock throws. A lock statement that fails also ends the session; a
     * cancelled wait holds nothing and just drops the connection.
     *
     * @throws UpgradeException when `$connect` returns the caller's own session
     */
    public function lock(?Cancellation $cancel = null): Closure
    {
        try {
            $conn = ($this->connect)();
            // @phpstan-ignore instanceof.alwaysTrue (a callable's return type is not enforced)
            if (!$conn instanceof PDO) {
                throw new UpgradeException('connect did not return a PDO');
            }
            $mine = self::backendPid($this->db);
            $theirs = self::backendPid($conn);
        } catch (UpgradeException $e) {
            throw new UpgradeException("postgres upgrade: lock: {$e->getMessage()}", 0, $e);
        }
        if ($mine === $theirs) {
            throw new UpgradeException(
                "postgres upgrade: lock needs a connection of its own: connect returned the caller's session "
                . "(backend pid $mine); the lock holds one session for the whole run, so return a new "
                . 'non-persistent connection (or one with another DSN) each time',
            );
        }
        $key = self::lockKey($this->table, $this->name);
        try {
            while (true) {
                if ($cancel !== null && $cancel->cancelled()) {
                    throw new CanceledException(); // no try returned true: nothing held
                }
                if (self::truthy(self::run($conn, self::SQL_TRY_LOCK, [$key])->fetchColumn())) {
                    break;
                }
                usleep(self::LOCK_POLL_US);
            }
        } catch (UpgradeException $e) {
            if (!$e instanceof CanceledException) {
                $this->endSession($conn);
                throw new UpgradeException("postgres upgrade: lock: {$e->getMessage()}", 0, $e);
            }
            throw $e;
        }

        $done = false;

        return function () use (&$conn, $key, &$done): void {
            if ($done) {
                return;
            }
            $done = true;
            $c = $conn;
            $conn = null;
            try {
                if (!self::truthy(self::run($c, self::SQL_UNLOCK, [$key])->fetchColumn())) {
                    throw new UpgradeException('lock was not held by this session');
                }
            } catch (UpgradeException $e) {
                $this->endSession($c);
                throw new UpgradeException("postgres upgrade: unlock: {$e->getMessage()}", 0, $e);
            }
        };
    }

    /**
     * Ends the physical session behind `$conn` so the server drops every
     * advisory lock it holds: `pg_terminate_backend(pg_backend_pid())` from
     * the session itself, which also kills a persistent connection that would
     * otherwise keep the lock after the PDO object is gone. Errors are
     * expected (the server drops the connection mid-statement) and ignored.
     */
    private function endSession(PDO $conn): void
    {
        try {
            $conn->query('SELECT pg_terminate_backend(pg_backend_pid())');
        } catch (PDOException) {
        }
    }

    private function ensureTable(): void
    {
        $db = $this->db;
        try {
            if (!$db->beginTransaction()) {
                throw new UpgradeException('begin: ' . self::errorText($db->errorInfo()));
            }
        } catch (PDOException $e) {
            throw new UpgradeException($e->getMessage(), 0, $e);
        }
        try {
            self::run($db, self::SQL_XACT_LOCK, ["kit-upgrade:{$this->table}:ddl"]);
            self::run($db, "CREATE TABLE IF NOT EXISTS {$this->table} ("
                . "schema_name TEXT PRIMARY KEY, version TEXT NOT NULL DEFAULT '')");
            if (!$db->commit()) {
                throw new UpgradeException('commit: ' . self::errorText($db->errorInfo()));
            }
        } catch (\Throwable $e) {
            try {
                if ($db->inTransaction()) {
                    $db->rollBack();
                }
            } catch (PDOException) {
            }
            throw $e instanceof PDOException ? new UpgradeException($e->getMessage(), 0, $e) : $e;
        }
    }

    /** A boolean column as PDO hands it over. */
    private static function truthy(mixed $v): bool
    {
        return $v === true || $v === 't' || $v === 1 || $v === '1';
    }

    private static function backendPid(PDO $c): int
    {
        return (int) self::run($c, 'SELECT pg_backend_pid()')->fetchColumn();
    }

    /**
     * Runs one statement whatever the handle's error mode: a failure is an
     * `UpgradeException`, the handle's attributes are left as they were.
     *
     * @param list<string> $params
     * @throws UpgradeException
     */
    private static function run(PDO $c, string $sql, array $params = []): PDOStatement
    {
        try {
            $st = $c->prepare($sql);
            if ($st === false) {
                throw new UpgradeException(self::errorText($c->errorInfo()));
            }
            if (!$st->execute($params)) {
                throw new UpgradeException(self::errorText($st->errorInfo()));
            }
        } catch (PDOException $e) {
            throw new UpgradeException($e->getMessage(), 0, $e);
        }

        return $st;
    }

    /** @param array<int, mixed> $info */
    private static function errorText(array $info): string
    {
        return (string) ($info[2] ?? 'statement failed');
    }
}
