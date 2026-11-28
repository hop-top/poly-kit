<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade;

use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Tests\Upgrade\Support\CollectingPublisher;
use HopTop\Kit\Tests\Upgrade\Support\Corpus;
use HopTop\Kit\Tests\Upgrade\Support\LockingDriver;
use HopTop\Kit\Tests\Upgrade\Support\LockingStoringDriver;
use HopTop\Kit\Tests\Upgrade\Support\RecordingDriver;
use HopTop\Kit\Tests\Upgrade\Support\StoringDriver;
use HopTop\Kit\Upgrade\Migration;
use HopTop\Kit\Upgrade\Migrator;
use HopTop\Kit\Upgrade\Registry;
use HopTop\Kit\Upgrade\SchemaDriver;
use HopTop\Kit\Upgrade\VersionFile;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * Runs `migrator.json` from contracts/upgrade-v1 against a recording driver
 * modelled on the Go runner's. Same strictness as `UpgradeConformanceTest`:
 * every scenario, driver, migration and step field is checked by name.
 */
final class UpgradeMigratorConformanceTest extends TestCase
{
    use Corpus;

    private const TOOL = 'conftool';

    private const SCENARIO = [
        'name', 'target', 'manual_rollback', 'retention', 'drivers', 'migrations', 'backups', 'recorded_backups',
        'locked_backups', 'baseline', 'final_verify', 'final_verify_fail', 'data_dir', 'registry',
        'default_migrations', 'steps',
    ];
    private const DRIVER = [
        'name', 'version', 'backup_fail', 'restore_fail', 'backup_unsupported', 'restore_unsupported', 'version_fail',
        'set_version_fail', 'set_version_fail_for', 'stores_version', 'lock', 'lock_fail', 'lock_blocked',
        'lock_sets_version', 'unlock_fail',
    ];
    private const MIGRATION = ['schema', 'version', 'fail', 'down', 'down_fail', 'verify', 'verify_fail', 'cancel'];
    private const STEP = [
        'op', 'canceled', 'events', 'warnings', 'error', 'calls', 'history', 'versions', 'backups', 'stored',
        'pre_run_versions', 'no_version_dir', 'status',
    ];

    /** @return iterable<string, array{int}> */
    public static function scenarios(): iterable
    {
        [$f] = self::load('migrator.json');
        self::strict($f, [...self::HEADER, 'scenarios'], 'migrator.json');
        foreach ($f['scenarios'] as $i => $s) {
            yield $s['name'] => [$i];
        }
    }

    #[Test]
    public function corpusHasScenarios(): void
    {
        [$f] = self::load('migrator.json');
        self::assertCount(109, $f['scenarios']);
    }

    #[Test]
    #[DataProvider('scenarios')]
    public function scenario(int $i): void
    {
        [$f] = self::load('migrator.json');
        $sc = $f['scenarios'][$i];
        self::strict($sc, self::SCENARIO, 'scenario');
        $tmp = sys_get_temp_dir() . '/kit-mig-' . bin2hex(random_bytes(6));
        $oldXdg = getenv('XDG_DATA_HOME');
        putenv('XDG_DATA_HOME=' . $tmp . '/xdg');
        Registry::default()->reset();
        try {
            $this->exercise($sc, $tmp);
        } finally {
            Registry::default()->reset();
            putenv($oldXdg === false ? 'XDG_DATA_HOME' : 'XDG_DATA_HOME=' . $oldXdg);
            self::rmrf($tmp);
        }
    }

    /** @param array<string, mixed> $sc */
    private function exercise(array $sc, string $tmp): void
    {
        $xdgBase = "$tmp/xdg/hop/" . self::TOOL;
        $base = ($sc['data_dir'] ?? false) ? "$tmp/custom" : $xdgBase;

        foreach ($sc['recorded_backups'] ?? [] as $schema => $recorded) {
            foreach ($recorded as $name => $pre) {
                self::mkdirs("$base/backups/$schema/$name");
                file_put_contents("$base/backups/$schema/$name.version", $pre);
            }
        }
        foreach ($sc['backups'] ?? [] as $schema => $dirs) {
            self::mkdirs("$base/backups/$schema");
            foreach ($dirs as $d) {
                self::mkdirs("$base/backups/$schema/$d");
            }
        }
        foreach ($sc['locked_backups'] ?? [] as $schema => $dirs) {
            self::mkdirs("$base/backups/$schema");
            foreach ($dirs as $d) {
                self::mkdirs("$base/backups/$schema/$d/locked");
                file_put_contents("$base/backups/$schema/$d/locked/f", '');
                chmod("$base/backups/$schema/$d/locked", 0500);
            }
        }

        $calls = [];
        $record = static function (string $e) use (&$calls): void {
            $calls[] = $e;
        };
        $ctx = new Cancellation();
        $build = function (array $mc) use ($record, $ctx): Migration {
            self::strict($mc, self::MIGRATION, 'migration');
            $schema = $mc['schema'];
            $v = $mc['version'];

            return new Migration(
                $v,
                $schema,
                static function () use ($mc, $record, $ctx, $schema, $v): void {
                    $record("$schema up $v");
                    if ($mc['cancel'] ?? false) {
                        $ctx->cancel();
                    }
                    self::failIf($mc['fail'] ?? '');
                },
                ($mc['down'] ?? false) || ($mc['down_fail'] ?? '') !== ''
                    ? static function () use ($mc, $record, $schema, $v): void {
                        $record("$schema down $v");
                        self::failIf($mc['down_fail'] ?? '');
                    }
                    : null,
                ($mc['verify'] ?? false) || ($mc['verify_fail'] ?? '') !== ''
                    ? static function () use ($mc, $record, $schema, $v): void {
                        $record("$schema verify $v");
                        self::failIf($mc['verify_fail'] ?? '');
                    }
                    : null,
            );
        };

        $reg = Registry::default();
        if (($sc['registry'] ?? '') === 'own') {
            $reg = new Registry();
            foreach ($sc['default_migrations'] ?? [] as $mc) {
                Registry::default()->register($build($mc));
            }
        }
        foreach ($sc['migrations'] as $mc) {
            $reg->register($build($mc));
        }

        $publisher = new CollectingPublisher();
        $warnings = [];
        $verify = ($sc['final_verify'] ?? false) || ($sc['final_verify_fail'] ?? '') !== ''
            ? static function (string $schema, string $version) use ($record, $sc): void {
                $record("$schema final_verify $version");
                self::failIf($sc['final_verify_fail'] ?? '');
            }
            : null;
        $baseline = array_key_exists('baseline', $sc)
            ? static function (string $schema) use ($record, $sc): string {
                $record("$schema baseline");
                $b = $sc['baseline'][$schema] ?? [];
                self::strict($b, ['version', 'error'], 'baseline');
                self::failIf($b['error'] ?? '');

                return $b['version'] ?? '';
            }
            : null;
        $m = new Migrator(
            self::TOOL,
            $sc['target'],
            manualRollback: $sc['manual_rollback'] ?? false,
            backupRetention: $sc['retention'] ?? 0,
            registry: $reg,
            dataDir: ($sc['data_dir'] ?? false) ? $base : null,
            verify: $verify,
            baseline: $baseline,
            events: $publisher,
            warn: static function (string $line) use (&$warnings): void {
                $warnings[] = $line;
            },
        );
        foreach ($sc['drivers'] as $dc) {
            self::strict($dc, self::DRIVER, 'driver');
            $m->addDriver(self::driver($dc, $record));
        }

        foreach ($sc['steps'] as $n => $st) {
            self::strict($st, self::STEP, 'step');
            foreach ($st['events'] ?? [] as $e) {
                self::strict($e, ['topic', 'payload'], 'event');
            }
            foreach ($st['history'] ?? [] as $h) {
                self::strict($h, ['schema', 'version'], 'history entry');
            }
            foreach ($st['status'] ?? [] as $s) {
                self::strict($s, ['schema', 'current', 'target', 'pending', 'error'], 'status entry');
            }
            $calls = [];
            $publisher->events = [];
            $warnings = [];
            $cancel = $ctx;
            if ($st['canceled'] ?? false) {
                $cancel = new Cancellation();
                $cancel->cancel();
            }
            $err = null;
            try {
                match ($st['op']) {
                    'run' => $m->run($cancel),
                    'rollback_latest' => $m->rollbackLatest(),
                    'status' => self::checkStatus($m, $st, $n),
                    default => self::fail("step $n: unknown op {$st['op']}"),
                };
            } catch (\PHPUnit\Framework\Exception $e) {
                throw $e;
            } catch (\Throwable $e) {
                $err = $e->getMessage();
            }
            self::assertSame([], $publisher->problems, "step $n");
            self::assertSame($st['error'] ?? null, $err, "step $n: error");
            if (isset($st['calls'])) {
                self::assertSame($st['calls'], $calls, "step $n: calls");
            }
            if (isset($st['history'])) {
                self::assertSame(
                    self::json($st['history']),
                    self::json(array_map(static fn ($h): array => ['schema' => $h->schema, 'version' => $h->version], $m->history())),
                    "step $n: history",
                );
            }
            if (isset($st['events'])) {
                self::assertSame(self::json($st['events']), self::json($publisher->events), "step $n: events");
            }
            if (isset($st['warnings'])) {
                self::assertCount(count($st['warnings']), $warnings, "step $n: logged " . json_encode($warnings));
                foreach ($st['warnings'] as $j => $w) {
                    self::assertStringStartsWith($w, $warnings[$j], "step $n: warning $j");
                }
            }
            foreach ($st['versions'] ?? [] as $schema => $want) {
                self::assertSame($want, VersionFile::readIn($base, (string) $schema), "step $n: $schema version file");
            }
            foreach ($st['backups'] ?? [] as $schema => $want) {
                self::assertSame($want, self::listBackups($base, (string) $schema), "step $n: $schema backups");
            }
            if (isset($st['stored'])) {
                $got = [];
                foreach ($m->status() as $s) {
                    $got[$s->schema] = $s->current;
                }
                foreach ($st['stored'] as $schema => $want) {
                    self::assertSame($want, $got[$schema] ?? null, "step $n: $schema stored version");
                }
            }
            foreach ($st['pre_run_versions'] ?? [] as $schema => $want) {
                self::assertSame(self::json($want), self::json(self::listPreRun($base, (string) $schema)), "step $n: $schema pre-run versions");
            }
            foreach ($st['no_version_dir'] ?? [] as $schema) {
                self::assertFileDoesNotExist("$base/$schema", "step $n: $schema has a version dir");
            }
            if ($sc['data_dir'] ?? false) {
                self::assertFileDoesNotExist($xdgBase, "step $n: XDG data dir touched despite dataDir");
            }
        }
    }

    /** @param array<string, mixed> $dc */
    private static function driver(array $dc, \Closure $record): SchemaDriver
    {
        $lock = $dc['lock'] ?? false;
        $stores = $dc['stores_version'] ?? false;

        return match (true) {
            $lock && $stores => new LockingStoringDriver($dc, $record),
            $lock => new LockingDriver($dc, $record),
            $stores => new StoringDriver($dc, $record),
            default => new RecordingDriver($dc, $record),
        };
    }

    /** @param array<string, mixed> $st */
    private static function checkStatus(Migrator $m, array $st, int $n): void
    {
        self::assertNotNull($st['status'] ?? null, "step $n: status op without status");
        $got = array_map(static fn ($s): array => [
            'schema' => $s->schema, 'current' => $s->current, 'target' => $s->target,
            'pending' => $s->pending, 'error' => $s->error?->getMessage(),
        ], $m->status());
        $want = array_map(static fn (array $s): array => $s + ['error' => null], $st['status']);
        self::assertSame(self::json($want), self::json($got), "step $n: status");
    }

    private static function mkdirs(string $dir): void
    {
        if (!is_dir($dir)) {
            mkdir($dir, 0750, true);
        }
    }

    private static function failIf(string $msg): void
    {
        if ($msg !== '') {
            throw new \RuntimeException($msg);
        }
    }

    /** Canonical JSON: object keys sorted, so key order never matters. */
    private static function json(mixed $v): string
    {
        $sort = static function (mixed $x) use (&$sort): mixed {
            if (!is_array($x)) {
                return $x;
            }
            $x = array_map($sort, $x);
            if (!array_is_list($x)) {
                ksort($x, SORT_STRING);
            }

            return $x;
        };

        return json_encode($sort($v), JSON_THROW_ON_ERROR);
    }

    /** @return list<string> */
    private static function listBackups(string $base, string $schema): array
    {
        $dir = "$base/backups/$schema";
        $names = [];
        foreach (is_dir($dir) ? (array) scandir($dir) : [] as $n) {
            if ($n !== '.' && $n !== '..' && is_dir("$dir/$n")) {
                $names[] = $n;
            }
        }
        sort($names, SORT_STRING);

        return $names;
    }

    /** @return array<string, string> the version saved beside each backup directory */
    private static function listPreRun(string $base, string $schema): array
    {
        $dir = "$base/backups/$schema";
        $out = [];
        foreach (is_dir($dir) ? (array) scandir($dir) : [] as $n) {
            if (!is_dir("$dir/$n") && str_ends_with($n, '.version')) {
                $out[substr($n, 0, -8)] = (string) file_get_contents("$dir/$n");
            }
        }

        return $out;
    }

    private static function rmrf(string $path): void
    {
        if (is_link($path) || is_file($path)) {
            @unlink($path);

            return;
        }
        if (!is_dir($path)) {
            return;
        }
        @chmod($path, 0750);
        foreach ((array) scandir($path) as $n) {
            if ($n !== '.' && $n !== '..') {
                self::rmrf("$path/$n");
            }
        }
        @rmdir($path);
    }
}
