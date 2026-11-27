<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade;

use HopTop\Kit\Upgrade\DocApplied;
use HopTop\Kit\Upgrade\DocApplyException;
use HopTop\Kit\Upgrade\DocGapException;
use HopTop\Kit\Upgrade\DocInvalidException;
use HopTop\Kit\Upgrade\DocNewerException;
use HopTop\Kit\Upgrade\DocOptions;
use HopTop\Kit\Upgrade\DocStep;
use HopTop\Kit\Upgrade\DocumentMigrator;
use HopTop\Kit\Upgrade\DocVersionException;
use HopTop\Kit\Upgrade\Migration;
use HopTop\Kit\Upgrade\Registry;
use HopTop\Kit\Upgrade\Semver;
use HopTop\Kit\Upgrade\VerifyException;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * Runs the shared corpus in contracts/upgrade-v1. The vectors are the
 * source of truth: a failure means this port drifted from the contract (Go
 * is canonical), not that a vector should be edited to fit.
 *
 * Strict: unknown fields fail, `version` must be `v1`, and every `.json` in
 * the corpus directory is either consumed or listed in PENDING by name.
 */
final class UpgradeConformanceTest extends TestCase
{
    /** Corpus files this runner consumes. */
    private const CONSUMED = ['versions.json', 'document.json'];

    /**
     * Corpus files a later change consumes: file => why it is not run yet.
     * The Migrator change moves `migrator.json` to CONSUMED and deletes
     * migratorJsonIsPending().
     */
    private const PENDING = [
        'migrator.json' => 'the PHP Migrator is not ported yet; this runner consumes the file once it is',
    ];

    private const HEADER = ['$schema', '$id', 'version', 'description'];

    private const KINDS = [
        'newer' => DocNewerException::class,
        'version' => DocVersionException::class,
        'gap' => DocGapException::class,
        'step' => DocApplyException::class,
        'verify' => VerifyException::class,
        'invalid' => DocInvalidException::class,
    ];

    private static function dir(): string
    {
        $dir = __DIR__;
        while (!is_dir($dir . '/contracts/upgrade-v1')) {
            $parent = dirname($dir);
            if ($parent === $dir) {
                throw new \RuntimeException('contracts/upgrade-v1 not found');
            }
            $dir = $parent;
        }

        return $dir . '/contracts/upgrade-v1';
    }

    /**
     * @param list<string> $allowed
     * @param array<mixed> $obj
     */
    private static function strict(array $obj, array $allowed, string $where): void
    {
        self::assertSame([], array_values(array_diff(array_map('strval', array_keys($obj)), $allowed)), "$where: unknown field");
    }

    /** @return array{0: array<string, mixed>, 1: \stdClass} decoded as arrays, and as objects */
    private static function load(string $file): array
    {
        $text = (string) file_get_contents(self::dir() . '/' . $file);
        /** @var array<string, mixed> $arr */
        $arr = json_decode($text, true, 512, JSON_THROW_ON_ERROR);
        /** @var \stdClass $obj */
        $obj = json_decode($text, false, 512, JSON_THROW_ON_ERROR);
        self::assertSame('v1', $arr['version'] ?? null, "$file: version");
        foreach (['description', '$schema', '$id'] as $k) {
            self::assertIsString($arr[$k] ?? null, "$file: $k");
        }

        return [$arr, $obj];
    }

    #[Test]
    public function everyCorpusFileIsConsumedOrPending(): void
    {
        $found = array_map('basename', glob(self::dir() . '/*.json') ?: []);
        sort($found);
        $known = array_merge(self::CONSUMED, array_keys(self::PENDING));
        sort($known);
        self::assertSame($known, $found, 'corpus files and runner classification differ');
    }

    #[Test]
    public function migratorJsonIsPending(): void
    {
        self::assertArrayHasKey('migrator.json', self::PENDING);
        [$arr] = self::load('migrator.json');
        self::assertNotEmpty($arr['scenarios'] ?? null);
        self::markTestSkipped('migrator.json pending: ' . self::PENDING['migrator.json']);
    }

    /** @return iterable<string, array{array<string, mixed>}> */
    public static function compareCases(): iterable
    {
        [$f] = self::load('versions.json');
        self::strict($f, [...self::HEADER, 'compare', 'pending'], 'versions.json');
        foreach ($f['compare'] as $c) {
            self::strict($c, ['name', 'a', 'b', 'cmp'], 'compare');
            yield $c['name'] => [$c];
        }
    }

    /** @param array<string, mixed> $c */
    #[Test]
    #[DataProvider('compareCases')]
    public function compare(array $c): void
    {
        self::assertSame($c['cmp'], Semver::compare($c['a'], $c['b']), "compare({$c['a']}, {$c['b']})");
        self::assertSame(-$c['cmp'], Semver::compare($c['b'], $c['a']), "compare({$c['b']}, {$c['a']})");
    }

    /** @return iterable<string, array{array<string, mixed>}> */
    public static function pendingCases(): iterable
    {
        [$f] = self::load('versions.json');
        foreach ($f['pending'] as $c) {
            self::strict($c, ['name', 'migrations', 'schema', 'from', 'to', 'pending'], 'pending');
            yield $c['name'] => [$c];
        }
    }

    /** @param array<string, mixed> $c */
    #[Test]
    #[DataProvider('pendingCases')]
    public function pending(array $c): void
    {
        $noop = static function (): void {};
        foreach ([Registry::default(), new Registry()] as $i => $reg) {
            $reg->reset();
            foreach ($c['migrations'] as $m) {
                self::strict($m, ['schema', 'version'], 'migration');
                $reg->register(new Migration($m['version'], $m['schema'], $noop));
            }
            $got = array_map(static fn (Migration $m): string => $m->version, $reg->pending($c['schema'], $c['from'], $c['to']));
            self::assertSame($c['pending'], $got, ($i === 0 ? 'default' : 'own') . " registry: {$c['name']}");
            $reg->reset();
        }
    }

    #[Test]
    public function corpusSectionsAreNotEmpty(): void
    {
        [$f] = self::load('versions.json');
        self::assertNotEmpty($f['compare']);
        self::assertNotEmpty($f['pending']);
        [$d] = self::load('document.json');
        self::strict($d, [...self::HEADER, 'scenarios'], 'document.json');
        self::assertNotEmpty($d['scenarios']);
    }

    /** @return iterable<string, array{int}> */
    public static function documentCases(): iterable
    {
        [$d] = self::load('document.json');
        foreach ($d['scenarios'] as $i => $s) {
            yield $s['name'] => [$i];
        }
    }

    private static function canon(mixed $v): string
    {
        $sort = static function (mixed $x) use (&$sort): mixed {
            if ($x instanceof \stdClass) {
                $props = get_object_vars($x);
                ksort($props, SORT_STRING);
                $out = new \stdClass();
                foreach ($props as $k => $val) {
                    $out->{(string) $k} = $sort($val);
                }

                return $out;
            }
            if (is_array($x)) {
                return array_map($sort, $x);
            }

            return $x;
        };

        return json_encode($sort($v), JSON_THROW_ON_ERROR | JSON_PRESERVE_ZERO_FRACTION);
    }

    /**
     * @param array<string, mixed> $arr
     * @return list<DocStep>
     */
    private static function steps(array $arr): array
    {
        $out = [];
        foreach ($arr as $c) {
            self::strict($c, ['from', 'to', 'note', 'ops', 'verify'], 'step');
            $ops = $c['ops'] ?? [];
            $apply = static function (array $doc) use ($ops): array {
                foreach ($ops as $op) {
                    if (isset($op['set'])) {
                        $segs = explode('.', $op['set']['path']);
                        $last = array_pop($segs);
                        $cur = &$doc;
                        foreach ($segs as $seg) {
                            if (!isset($cur[$seg]) || !is_array($cur[$seg])) {
                                $cur[$seg] = [];
                            }
                            $cur = &$cur[$seg];
                        }
                        $cur[$last] = $op['set']['value'];
                        unset($cur);
                    } elseif (isset($op['rename'])) {
                        if (array_key_exists($op['rename']['from'], $doc)) {
                            $doc[$op['rename']['to']] = $doc[$op['rename']['from']];
                            unset($doc[$op['rename']['from']]);
                        }
                    } elseif (isset($op['fail'])) {
                        throw new \RuntimeException($op['fail']);
                    }
                }

                return $doc;
            };
            $verify = null;
            if (isset($c['verify'])) {
                $req = $c['verify']['require'];
                $verify = static function (array $doc) use ($req): void {
                    $cur = $doc;
                    foreach (explode('.', $req) as $seg) {
                        if (!is_array($cur) || !array_key_exists($seg, $cur)) {
                            throw new \RuntimeException('missing ' . $req);
                        }
                        $cur = $cur[$seg];
                    }
                };
            }
            $out[] = new DocStep($c['from'], $c['to'], $apply, $verify, $c['note'] ?? '');
        }

        return $out;
    }

    #[Test]
    #[DataProvider('documentCases')]
    public function document(int $i): void
    {
        [$arr, $obj] = self::load('document.json');
        $sc = $arr['scenarios'][$i];
        self::strict($sc, ['name', 'doc', 'text', 'options', 'steps', 'expect'], 'scenario');
        self::strict($sc['options'] ?? [], ['version_field', 'latest', 'missing_is_zero'], 'options');
        self::strict($sc['expect'], ['doc', 'text', 'report', 'error', 'kind'], 'expect');
        $rawSc = $obj->scenarios[$i];
        $opts = new DocOptions(
            $sc['options']['version_field'] ?? '',
            $sc['options']['latest'] ?? 0,
            $sc['options']['missing_is_zero'] ?? false,
        );
        $steps = self::steps($sc['steps']);
        $input = array_key_exists('text', $sc)
            ? $sc['text']
            : json_encode($rawSc->doc, JSON_THROW_ON_ERROR | JSON_PRESERVE_ZERO_FRACTION);
        $exp = $sc['expect'];

        if (isset($exp['error'])) {
            try {
                DocumentMigrator::migrate($input, $steps, $opts);
                self::fail("no error, want {$exp['error']}");
            } catch (\Throwable $e) {
                if ($e instanceof \PHPUnit\Framework\AssertionFailedError) {
                    throw $e;
                }
                self::assertSame($exp['error'], $e->getMessage());
                self::assertInstanceOf(self::KINDS[$exp['kind']], $e, 'kind ' . $exp['kind']);
            }
            // a map input is the same refusal; the caller's array is untouched
            if (is_array($sc['doc'] ?? null) && !array_is_list($sc['doc'])) {
                $in = self::asArrays($rawSc->doc);
                $before = $in;
                try {
                    DocumentMigrator::migrateArray($in, $steps, $opts);
                    self::fail('map: no error');
                } catch (\Throwable $e) {
                    if ($e instanceof \PHPUnit\Framework\AssertionFailedError) {
                        throw $e;
                    }
                    self::assertSame($exp['error'], $e->getMessage());
                    self::assertInstanceOf(self::KINDS[$exp['kind']], $e);
                }
                self::assertEquals($before, $in);
            }

            return;
        }

        $r = DocumentMigrator::migrate($input, $steps, $opts);
        if (isset($exp['text'])) {
            self::assertSame($exp['text'], $r->document, 'byte-identical');
        }
        if (isset($exp['doc'])) {
            self::assertSame(self::canon($rawSc->expect->doc), self::canon(json_decode($r->document, false, 512, JSON_THROW_ON_ERROR)));
        }
        self::assertSame(self::report($exp['report']), self::report($r->report));
        if (!array_key_exists('text', $sc) && is_array($sc['doc'])) {
            $in = self::asArrays($rawSc->doc);
            $before = $in;
            $m = DocumentMigrator::migrateArray($in, $steps, $opts);
            self::assertEquals($before, $in, 'caller array changed');
            self::assertSame(self::report($exp['report']), self::report($m->report));
            if (isset($exp['doc'])) {
                self::assertSame(self::canon($rawSc->expect->doc), self::canon(json_decode(json_encode($m->document, JSON_THROW_ON_ERROR), false)));
            }
        }
    }

    /** Objects as arrays, an empty object kept as an empty stdClass. */
    private static function asArrays(mixed $v): mixed
    {
        if ($v instanceof \stdClass) {
            $a = get_object_vars($v);

            return $a === [] ? $v : array_map(self::asArrays(...), $a);
        }
        if (is_array($v)) {
            return array_map(self::asArrays(...), $v);
        }

        return $v;
    }

    /** @return array{from: int, to: int, applied: list<array{from: int, to: int, note: string}>} */
    private static function report(mixed $r): array
    {
        if (is_array($r)) {
            return [
                'from' => $r['from'], 'to' => $r['to'],
                'applied' => array_map(static fn (array $a): array => ['from' => $a['from'], 'to' => $a['to'], 'note' => $a['note']], $r['applied']),
            ];
        }

        return [
            'from' => $r->from, 'to' => $r->to,
            'applied' => array_map(static fn (DocApplied $a): array => ['from' => $a->from, 'to' => $a->to, 'note' => $a->note], $r->applied),
        ];
    }
}
