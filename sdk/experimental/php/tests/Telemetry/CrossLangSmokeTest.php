<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry;

use HopTop\Kit\Telemetry\Consent;
use HopTop\Kit\Telemetry\InstallId;
use HopTop\Kit\Telemetry\Sink\JsonlSink;
use HopTop\Kit\Telemetry\Telemetry;
use PHPUnit\Framework\TestCase;

/**
 * End-to-end smoke test for the PHP telemetry pipeline.
 *
 * Two scopes:
 *
 *   1. Local smoke (always-on): drives the {@see Telemetry} facade
 *      through a real flow — env mode + consent file + JsonlSink — and
 *      asserts that the emitted envelopes have the cross-language
 *      contract shape (schema_version, sdk_lang, installation_id, mode,
 *      occurred_at, event, attrs?). See sdk/docs/telemetry-event-schema.md.
 *
 *   2. Cross-language harness hook (DEFERRED): the shared harness at
 *      sdk/tests/cross-lang/ is not yet shipped at the time this test
 *      landed. When that harness lands, add the PHP fixture script at
 *      sdk/tests/cross-lang/php/record.php and remove the
 *      `markTestSkipped` in {@see testCrossLangHarnessHook}.
 */
class CrossLangSmokeTest extends TestCase
{
    private string $tmpRoot;
    private string|false $prevState;
    private string|false $prevConfig;
    private string|false $prevMode;
    private string|false $prevSink;
    private string|false $prevDoNotTrack;

    protected function setUp(): void
    {
        $this->tmpRoot = sys_get_temp_dir() . '/kit-telemetry-smoke-' . bin2hex(random_bytes(8));
        mkdir($this->tmpRoot . '/state', 0o700, true);
        mkdir($this->tmpRoot . '/config/kit', 0o700, true);

        $this->prevState = getenv('XDG_STATE_HOME');
        $this->prevConfig = getenv('XDG_CONFIG_HOME');
        $this->prevMode = getenv('KIT_TELEMETRY_MODE');
        $this->prevSink = getenv('KIT_TELEMETRY_SINK');
        $this->prevDoNotTrack = getenv('DO_NOT_TRACK');

        putenv('XDG_STATE_HOME=' . $this->tmpRoot . '/state');
        putenv('XDG_CONFIG_HOME=' . $this->tmpRoot . '/config');
        putenv('KIT_TELEMETRY_MODE=full');
        putenv('KIT_TELEMETRY_SINK=jsonl');
        putenv('DO_NOT_TRACK');

        // Persist a granted consent decision so Telemetry::record() proceeds.
        file_put_contents(
            Consent::path(),
            "telemetry:\n  consent:\n    state: granted\n    prompt_version: 1\n    decision_source: test\n",
        );

        Telemetry::resetForTest();
        InstallId::resetForTest();
    }

    protected function tearDown(): void
    {
        Telemetry::resetForTest();
        $this->restore('XDG_STATE_HOME', $this->prevState);
        $this->restore('XDG_CONFIG_HOME', $this->prevConfig);
        $this->restore('KIT_TELEMETRY_MODE', $this->prevMode);
        $this->restore('KIT_TELEMETRY_SINK', $this->prevSink);
        $this->restore('DO_NOT_TRACK', $this->prevDoNotTrack);
        $this->rmrf($this->tmpRoot);
    }

    public function testLocalSmokeProducesContractShapedEnvelope(): void
    {
        $sinkPath = $this->tmpRoot . '/state/kit/telemetry/inbox/smoke.jsonl';
        $sink = new JsonlSink($sinkPath, registerShutdown: false);
        Telemetry::setSink($sink);

        Telemetry::record('kit.cli.command.invoked', [
            'command' => 'kit telemetry status',
            'duration_ms' => 42,
            'exit' => 0,
            // PII shapes to confirm the redactor is wired.
            'user' => 'alice@example.com',
            'ip' => '10.0.0.5',
        ]);

        $sink->flush();

        $this->assertFileExists($sinkPath);
        $lines = array_values(array_filter(
            explode("\n", (string) file_get_contents($sinkPath)),
            static fn (string $l): bool => $l !== '',
        ));
        $this->assertCount(1, $lines);

        $env = json_decode($lines[0], true);
        $this->assertIsArray($env);

        // Cross-language contract shape (ADR-0035 + ADR-0038 +
        // sdk/docs/telemetry-event-schema.md).
        $this->assertSame('1', $env['schema_version']);
        $this->assertSame('php', $env['sdk_lang']);
        $this->assertMatchesRegularExpression('/^[0-9a-f]{64}$/', $env['installation_id']);
        $this->assertSame('full', $env['mode']);
        $this->assertMatchesRegularExpression(
            '/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/',
            $env['occurred_at'],
        );
        $this->assertSame('kit.cli.command.invoked', $env['event']);
        // Old field names must NOT appear (regression guard).
        $this->assertArrayNotHasKey('ts', $env);
        $this->assertArrayNotHasKey('install_id', $env);
        $this->assertArrayNotHasKey('sdk', $env);

        // Attrs present in Full mode + redactor wired.
        $this->assertArrayHasKey('attrs', $env);
        $this->assertSame('<redacted:email>', $env['attrs']['user']);
        $this->assertSame('<redacted:ipv4>', $env['attrs']['ip']);
        $this->assertSame('kit telemetry status', $env['attrs']['command']);
    }

    public function testAnonModeStripsAttrs(): void
    {
        putenv('KIT_TELEMETRY_MODE=anon');

        $sinkPath = $this->tmpRoot . '/state/kit/telemetry/inbox/anon.jsonl';
        $sink = new JsonlSink($sinkPath, registerShutdown: false);
        Telemetry::setSink($sink);

        Telemetry::record('kit.cli.command.invoked', ['command' => 'kit foo']);
        $sink->flush();

        $env = json_decode((string) file_get_contents($sinkPath), true);
        $this->assertSame('anon', $env['mode']);
        $this->assertArrayNotHasKey('attrs', $env);
    }

    public function testOffModeNoOps(): void
    {
        putenv('KIT_TELEMETRY_MODE=off');
        $sinkPath = $this->tmpRoot . '/state/kit/telemetry/inbox/off.jsonl';
        $sink = new JsonlSink($sinkPath, registerShutdown: false);
        Telemetry::setSink($sink);

        Telemetry::record('kit.cli.command.invoked', ['command' => 'kit foo']);
        $sink->flush();

        $this->assertFileDoesNotExist($sinkPath);
    }

    public function testDoNotTrackHonored(): void
    {
        putenv('DO_NOT_TRACK=1');
        $sinkPath = $this->tmpRoot . '/state/kit/telemetry/inbox/dnt.jsonl';
        $sink = new JsonlSink($sinkPath, registerShutdown: false);
        Telemetry::setSink($sink);

        Telemetry::record('kit.cli.command.invoked', ['command' => 'kit foo']);
        $sink->flush();

        $this->assertFileDoesNotExist($sinkPath);
    }

    public function testDeniedConsentNoOps(): void
    {
        // Overwrite with denied state.
        file_put_contents(
            Consent::path(),
            "telemetry:\n  consent:\n    state: denied\n    prompt_version: 1\n    decision_source: test\n",
        );

        $sinkPath = $this->tmpRoot . '/state/kit/telemetry/inbox/denied.jsonl';
        $sink = new JsonlSink($sinkPath, registerShutdown: false);
        Telemetry::setSink($sink);

        Telemetry::record('kit.cli.command.invoked');
        $sink->flush();

        $this->assertFileDoesNotExist($sinkPath);
    }

    public function testCrossLangHarnessHook(): void
    {
        $harness = dirname(__DIR__, 4) . '/sdk/tests/cross-lang';
        if (!is_dir($harness)) {
            // Cross-language harness not yet shipped. When it lands,
            // drop sdk/tests/cross-lang/php/record.php and adjust this
            // test to invoke the harness driver against it.
            $this->markTestSkipped(
                'cross-language harness not yet present at ' . $harness,
            );
        }

        $this->fail('cross-language harness directory found; replace this stub with a real invocation');
    }

    private function restore(string $key, string|false $prev): void
    {
        if ($prev === false) {
            putenv($key);
        } else {
            putenv($key . '=' . $prev);
        }
    }

    private function rmrf(string $dir): void
    {
        if (!is_dir($dir)) {
            return;
        }
        $items = scandir($dir);
        if ($items === false) {
            return;
        }
        foreach ($items as $item) {
            if ($item === '.' || $item === '..') {
                continue;
            }
            $path = $dir . '/' . $item;
            if (is_dir($path) && !is_link($path)) {
                $this->rmrf($path);
            } else {
                @unlink($path);
            }
        }
        @rmdir($dir);
    }
}
