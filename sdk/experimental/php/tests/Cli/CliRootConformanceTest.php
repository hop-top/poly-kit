<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Cli;

use HopTop\Kit\Cli\CommandSpec;
use HopTop\Kit\Cli\Delegation\PolicyLoader;
use HopTop\Kit\Cli\Delegation\PromptSource;
use HopTop\Kit\Cli\FlagDef;
use HopTop\Kit\Cli\Invocation;
use HopTop\Kit\Cli\Root;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * Replays the delegation + `infer` slice of contracts/cli-root-v1. The
 * vectors are the source of truth: a failure means this port drifted from
 * the contract (Go is canonical), not that a vector should be edited.
 * Every vector is either replayed or listed below with a reason; a vector
 * nobody classified fails the suite instead of vanishing.
 */
final class CliRootConformanceTest extends TestCase
{
    /** Trees whose features this port does not carry. */
    private const SKIP_TREES = [
        'main' => 'root surface (persistent flags, version, help layout, aliases, autocorrect) not ported',
        'strict' => 'Root.Validate / validation modes not ported',
        'strict_bad' => 'Root.Validate / validation modes not ported',
        'sig' => 'signature validators not ported',
        'sig_ok' => 'signature validators not ported',
        'store' => 'alias store commands not ported',
        'cfgcmd' => 'config path|paths commands not ported',
        'cfgnone' => 'config path|paths commands not ported',
    ];

    /** Vectors inside a replayed tree that are not replayed. */
    private const SKIP_CASES = [
        'a malformed annotation is not inferred over' => 'divergence: Go runs the leaf unvalidated here; '
            . 'the port has no Root.Validate, so the gate fails closed (USAGE) on a malformed kit/side-effect',
    ];

    private const SKIP_PREFIXES = [
        'chdir' => '-C/--chdir not ported',
        'config' => '-c/--config layering not ported',
        'flag validator' => 'flag validators not ported',
    ];

    private const REPLAYED_TREES = ['ops', 'infer'];

    /** @var array{trees: array<string, array<string, mixed>>, cases: list<array<string, mixed>>}|null */
    private static ?array $doc = null;

    /** @return array{trees: array<string, array<string, mixed>>, cases: list<array<string, mixed>>} */
    private static function doc(): array
    {
        if (self::$doc === null) {
            $dir = __DIR__;
            while (!is_file($dir . '/contracts/cli-root-v1/vectors.json')) {
                $parent = dirname($dir);
                if ($parent === $dir) {
                    throw new \RuntimeException('contracts/cli-root-v1/vectors.json not found');
                }
                $dir = $parent;
            }
            /** @var array{trees: array<string, array<string, mixed>>, cases: list<array<string, mixed>>} $d */
            $d = json_decode((string) file_get_contents($dir . '/contracts/cli-root-v1/vectors.json'), true, 512, JSON_THROW_ON_ERROR);
            self::$doc = $d;
        }

        return self::$doc;
    }

    /** @param array<string, mixed> $case */
    private static function skipReason(array $case): ?string
    {
        $name = (string) $case['name'];
        $tree = (string) $case['tree'];
        if (isset(self::SKIP_TREES[$tree])) {
            return self::SKIP_TREES[$tree];
        }
        if (isset(self::SKIP_CASES[$name])) {
            return self::SKIP_CASES[$name];
        }
        foreach (self::SKIP_PREFIXES as $prefix => $why) {
            if (str_starts_with($name, $prefix)) {
                return $why;
            }
        }

        return null;
    }

    /** @return iterable<string, array{array<string, mixed>}> */
    public static function cases(): iterable
    {
        foreach (self::doc()['cases'] as $c) {
            yield (string) $c['name'] => [$c];
        }
    }

    #[Test]
    public function everyTreeIsClassified(): void
    {
        $known = array_merge(self::REPLAYED_TREES, array_keys(self::SKIP_TREES));
        self::assertSame([], array_values(array_diff(array_keys(self::doc()['trees']), $known)));
    }

    #[Test]
    public function skipsNameRealVectors(): void
    {
        $names = array_column(self::doc()['cases'], 'name');
        self::assertSame([], array_values(array_diff(array_keys(self::SKIP_CASES), $names)));
    }

    #[Test]
    public function theSliceReplaysTheDelegationCases(): void
    {
        $run = array_filter(self::doc()['cases'], static fn (array $c): bool => self::skipReason($c) === null);
        self::assertGreaterThanOrEqual(40, count($run));
    }

    /** @param array<string, mixed> $case */
    #[Test]
    #[DataProvider('cases')]
    public function vector(array $case): void
    {
        $why = self::skipReason($case);
        if ($why !== null) {
            self::markTestSkipped($why);
        }
        self::assertContains($case['tree'], self::REPLAYED_TREES, "{$case['name']}: unclassified tree");
        /** @var array<string, mixed> $tree */
        $tree = self::doc()['trees'][$case['tree']];

        $tmp = sys_get_temp_dir() . '/kit-cli-root-' . bin2hex(random_bytes(6));
        mkdir($tmp, 0o700, true);
        $prev = getenv('XDG_CONFIG_HOME');
        try {
            self::materialize($tree, $tmp);
            putenv('XDG_CONFIG_HOME=' . $tmp . '/xdg');
            $answer = $case['prompt_answer'] ?? null;
            $prompt = $answer !== null ? PromptSource::fromAnswers([(string) $answer]) : PromptSource::none();
            $args = array_map(static fn (string $a): string => str_replace('{tmp}', $tmp, $a), $case['args']);

            $out = fopen('php://memory', 'w+b');
            $err = fopen('php://memory', 'w+b');
            self::assertIsResource($out);
            self::assertIsResource($err);
            $root = new Root(
                program: (string) $tree['program'],
                version: (string) $tree['version'],
                about: (string) $tree['about'],
                policyLoader: !empty($tree['policy']) ? PolicyLoader::xdg((string) $tree['program']) : null,
                prompt: $prompt,
                stdout: $out,
                stderr: $err,
            );
            /** @var list<array<string, mixed>> $cmds */
            $cmds = $tree['commands'];
            foreach (self::specs($cmds, []) as $spec) {
                $root->add($spec);
            }

            $code = $root->run($args);
            rewind($out);
            rewind($err);
            $stdout = (string) stream_get_contents($out);
            $stderr = (string) stream_get_contents($err);

            self::assertSame($case['exit'], $code, "stdout=$stdout stderr=$stderr");
            if (isset($case['stdout'])) {
                self::assertSame($case['stdout'], $stdout);
            }
            foreach ($case['stdout_contains'] ?? [] as $s) {
                self::assertStringContainsString($s, $stdout);
            }
            foreach ($case['stdout_omits'] ?? [] as $s) {
                self::assertStringNotContainsString($s, $stdout);
            }
            foreach ($case['stderr_contains'] ?? [] as $s) {
                self::assertStringContainsString($s, $stderr);
            }
            if (!empty($case['stderr_json'])) {
                /** @var array<string, mixed> $got */
                $got = json_decode($stderr, true, 512, JSON_THROW_ON_ERROR);
                foreach ($case['stderr_json'] as $k => $v) {
                    self::assertSame($v, $got[$k]);
                }
            }
        } finally {
            $prev === false ? putenv('XDG_CONFIG_HOME') : putenv('XDG_CONFIG_HOME=' . $prev);
            self::rmrf($tmp);
        }
    }

    /**
     * @param list<array<string, mixed>> $cmds
     * @param list<string> $prefix
     * @return list<CommandSpec>
     */
    private static function specs(array $cmds, array $prefix): array
    {
        $out = [];
        foreach ($cmds as $c) {
            $path = [...$prefix, (string) $c['name']];
            if (!empty($c['commands'])) {
                /** @var list<array<string, mixed>> $kids */
                $kids = $c['commands'];
                $out[] = new CommandSpec((string) $c['name'], (string) $c['about'], children: self::specs($kids, $path));
                continue;
            }
            $ann = $c['annotations'] ?? [];
            if (!empty($c['side_effect'])) {
                $ann['kit/side-effect'] = $c['side_effect'];
            }
            if (!empty($c['dry_run'])) {
                $ann['kit/dry-run'] = $c['dry_run'];
            }
            $flags = array_map(
                static fn (array $f): FlagDef => new FlagDef((string) $f['long'], (string) ($f['help'] ?? '')),
                $c['flags'] ?? [],
            );
            $joined = implode(' ', $path);
            $out[] = new CommandSpec(
                (string) $c['name'],
                (string) $c['about'],
                annotations: $ann,
                flags: $flags,
                handler: static function (Invocation $inv) use ($joined): int {
                    fwrite($inv->stdout, json_encode(['dry_run' => $inv->isDryRun(), 'path' => $joined], JSON_THROW_ON_ERROR) . "\n");

                    return 0;
                },
            );
        }

        return $out;
    }

    /** @param array<string, mixed> $tree */
    private static function materialize(array $tree, string $tmp): void
    {
        foreach ($tree['fixtures'] ?? [] as $rel => $content) {
            $target = $tmp . '/' . $rel;
            if (str_ends_with($rel, '/')) {
                @mkdir($target, 0o700, true);
                continue;
            }
            @mkdir(dirname($target), 0o700, true);
            file_put_contents($target, $content);
        }
    }

    private static function rmrf(string $p): void
    {
        if (is_dir($p) && !is_link($p)) {
            foreach (scandir($p) ?: [] as $e) {
                if ($e !== '.' && $e !== '..') {
                    self::rmrf($p . '/' . $e);
                }
            }
            rmdir($p);
        } elseif (file_exists($p) || is_link($p)) {
            unlink($p);
        }
    }
}
