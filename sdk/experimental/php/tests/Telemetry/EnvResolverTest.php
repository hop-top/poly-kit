<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry;

use HopTop\Kit\Telemetry\EnvResolver;
use HopTop\Kit\Telemetry\Mode;
use PHPUnit\Framework\TestCase;

class EnvResolverTest extends TestCase
{
    public function testEmptyEnvDefaultsToOff(): void
    {
        $this->assertSame(Mode::Off, EnvResolver::resolveMode([]));
    }

    public function testKitTelemetryModeFull(): void
    {
        $this->assertSame(
            Mode::Full,
            EnvResolver::resolveMode(['KIT_TELEMETRY_MODE' => 'full'])
        );
    }

    public function testKitTelemetryModeAnon(): void
    {
        $this->assertSame(
            Mode::Anon,
            EnvResolver::resolveMode(['KIT_TELEMETRY_MODE' => 'anon'])
        );
    }

    public function testKitTelemetryModeGarbageFallsToOff(): void
    {
        $this->assertSame(
            Mode::Off,
            EnvResolver::resolveMode(['KIT_TELEMETRY_MODE' => 'noisy'])
        );
    }

    public function testAppPrefixOverridesKitDefault(): void
    {
        $env = [
            'KIT_APP_PREFIX' => 'CTXT',
            'KIT_TELEMETRY_MODE' => 'anon',
            'CTXT_TELEMETRY_MODE' => 'full',
        ];
        $this->assertSame(Mode::Full, EnvResolver::resolveMode($env));
    }

    public function testAppPrefixCaseInsensitive(): void
    {
        $env = [
            'KIT_APP_PREFIX' => 'ctxt',
            'CTXT_TELEMETRY_MODE' => 'anon',
        ];
        $this->assertSame(Mode::Anon, EnvResolver::resolveMode($env));
    }

    public function testAppPrefixSetButEmptyFallsThrough(): void
    {
        $env = [
            'KIT_APP_PREFIX' => 'CTXT',
            'CTXT_TELEMETRY_MODE' => '',
            'KIT_TELEMETRY_MODE' => 'full',
        ];
        $this->assertSame(Mode::Full, EnvResolver::resolveMode($env));
    }

    public function testAppPrefixUnsetMissingAppVar(): void
    {
        $env = [
            'KIT_APP_PREFIX' => 'CTXT',
            'KIT_TELEMETRY_MODE' => 'anon',
        ];
        // CTXT_TELEMETRY_MODE absent -> fall through to KIT_TELEMETRY_MODE.
        $this->assertSame(Mode::Anon, EnvResolver::resolveMode($env));
    }

    public function testAppPrefixWhitespaceFallsThrough(): void
    {
        $env = [
            'KIT_APP_PREFIX' => '   ',
            'KIT_TELEMETRY_MODE' => 'full',
        ];
        $this->assertSame(Mode::Full, EnvResolver::resolveMode($env));
    }

    public function testDoNotTrackTrue(): void
    {
        $this->assertTrue(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => '1']));
        $this->assertTrue(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => 'true']));
        $this->assertTrue(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => 'TRUE']));
        $this->assertTrue(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => 'yes']));
        $this->assertTrue(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => 'on']));
    }

    public function testDoNotTrackFalse(): void
    {
        $this->assertFalse(EnvResolver::isDoNotTrack([]));
        $this->assertFalse(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => '']));
        $this->assertFalse(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => '   ']));
        $this->assertFalse(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => '0']));
        $this->assertFalse(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => 'false']));
        $this->assertFalse(EnvResolver::isDoNotTrack(['DO_NOT_TRACK' => 'FALSE']));
    }
}
