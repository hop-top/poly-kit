<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry;

use HopTop\Kit\Telemetry\Sink\JsonlSink;
use HopTop\Kit\Telemetry\Sink\NullSink;
use HopTop\Kit\Telemetry\Telemetry;
use PHPUnit\Framework\TestCase;
use ReflectionMethod;

/**
 * Covers KIT_TELEMETRY_SINK resolution in {@see Telemetry::sink()}.
 *
 * The facade cannot construct an HttpsSink from the environment, so an
 * operator setting `KIT_TELEMETRY_SINK=https` used to get a JsonlSink
 * with no signal at all — indistinguishable from a typo, and from a
 * correctly-honoured request. Every explicitly-set value that is not
 * `jsonl` or `none` must now produce a diagnostic.
 */
class TelemetrySinkSelectionTest extends TestCase
{
    private string|false $prevSink;

    protected function setUp(): void
    {
        $this->prevSink = getenv('KIT_TELEMETRY_SINK');
        Telemetry::resetForTest();
    }

    protected function tearDown(): void
    {
        Telemetry::resetForTest();

        if ($this->prevSink === false) {
            putenv('KIT_TELEMETRY_SINK');
        } else {
            putenv('KIT_TELEMETRY_SINK=' . $this->prevSink);
        }
    }

    /**
     * Resolve the sink the way record() does, capturing any diagnostic.
     *
     * @return array{0: object, 1: list<string>}
     */
    private function resolve(?string $value): array
    {
        if ($value === null) {
            putenv('KIT_TELEMETRY_SINK');
        } else {
            putenv('KIT_TELEMETRY_SINK=' . $value);
        }

        $reported = [];
        Telemetry::setSinkErrReporter(
            static function (string $m) use (&$reported): void {
                $reported[] = $m;
            },
        );

        // sink() is private: it is an internal resolution step, but it
        // is the unit under test.
        $sink = (new ReflectionMethod(Telemetry::class, 'sink'))->invoke(null);

        return [$sink, $reported];
    }

    public function testHttpsReportsAndFallsBackToJsonl(): void
    {
        [$sink, $reported] = $this->resolve('https');

        $this->assertInstanceOf(JsonlSink::class, $sink);
        $this->assertCount(1, $reported);
        $this->assertStringContainsString('https', $reported[0]);
        // The message must name the escape hatch, not just complain.
        $this->assertStringContainsString('setSink', $reported[0]);
    }

    public function testUnknownValueReportsAndFallsBackToJsonl(): void
    {
        [$sink, $reported] = $this->resolve('parquet');

        $this->assertInstanceOf(JsonlSink::class, $sink);
        $this->assertCount(1, $reported);
        $this->assertStringContainsString('parquet', $reported[0]);
    }

    public function testHttpsMessageDiffersFromTypoMessage(): void
    {
        [, $https] = $this->resolve('https');
        Telemetry::resetForTest();
        [, $typo] = $this->resolve('htps');

        // Regression guard: the whole point of the fix is that "I asked
        // for HTTPS" and "I typoed" are no longer the same outcome.
        $this->assertNotSame($https[0], $typo[0]);
    }

    public function testNoneSelectsNullSinkSilently(): void
    {
        [$sink, $reported] = $this->resolve('none');

        $this->assertInstanceOf(NullSink::class, $sink);
        $this->assertSame([], $reported);
    }

    public function testJsonlSelectsJsonlSinkSilently(): void
    {
        [$sink, $reported] = $this->resolve('jsonl');

        $this->assertInstanceOf(JsonlSink::class, $sink);
        $this->assertSame([], $reported);
    }

    public function testUnsetSelectsJsonlSinkSilently(): void
    {
        [$sink, $reported] = $this->resolve(null);

        $this->assertInstanceOf(JsonlSink::class, $sink);
        $this->assertSame([], $reported);
    }

    public function testEmptyAndWhitespaceStaySilent(): void
    {
        foreach (['', '   '] as $value) {
            Telemetry::resetForTest();
            [$sink, $reported] = $this->resolve($value);

            $this->assertInstanceOf(JsonlSink::class, $sink);
            $this->assertSame([], $reported, "value: '{$value}'");
        }
    }

    public function testSelectionIsCaseAndWhitespaceInsensitive(): void
    {
        foreach ([' HTTPS ', 'Https'] as $value) {
            Telemetry::resetForTest();
            [$sink, $reported] = $this->resolve($value);

            $this->assertInstanceOf(JsonlSink::class, $sink);
            $this->assertCount(1, $reported, "value: '{$value}'");
            $this->assertStringContainsString('setSink', $reported[0]);
        }

        Telemetry::resetForTest();
        [$sink, $reported] = $this->resolve(' NONE ');
        $this->assertInstanceOf(NullSink::class, $sink);
        $this->assertSame([], $reported);
    }

    public function testExplicitSinkWins(): void
    {
        putenv('KIT_TELEMETRY_SINK=https');

        $reported = [];
        Telemetry::setSinkErrReporter(
            static function (string $m) use (&$reported): void {
                $reported[] = $m;
            },
        );

        $injected = new NullSink();
        Telemetry::setSink($injected);

        $sink = (new ReflectionMethod(Telemetry::class, 'sink'))->invoke(null);

        // An injected sink short-circuits env resolution entirely, so
        // there is nothing to warn about.
        $this->assertSame($injected, $sink);
        $this->assertSame([], $reported);
    }

    public function testReporterDefaultsToNoopAndRecordStaysSafe(): void
    {
        putenv('KIT_TELEMETRY_SINK=parquet');
        Telemetry::resetForTest();

        // No reporter installed: resolution must stay quiet and must
        // not raise. Telemetry never breaks the host app.
        $sink = (new ReflectionMethod(Telemetry::class, 'sink'))->invoke(null);
        $this->assertInstanceOf(JsonlSink::class, $sink);
    }

    public function testThrowingReporterDoesNotEscape(): void
    {
        putenv('KIT_TELEMETRY_SINK=parquet');
        Telemetry::setSinkErrReporter(
            static function (string $m): void {
                throw new \RuntimeException('reporter blew up');
            },
        );

        // A reporter that throws must be swallowed: diagnostics are
        // best-effort, exactly like the sinks themselves.
        $sink = (new ReflectionMethod(Telemetry::class, 'sink'))->invoke(null);
        $this->assertInstanceOf(JsonlSink::class, $sink);
    }
}
