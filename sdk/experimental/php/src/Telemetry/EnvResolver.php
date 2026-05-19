<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry;

/**
 * Environment-driven configuration resolver for the telemetry pipeline.
 *
 * Implements the precedence ladder defined in ADR-0035 + ADR-0038
 * (PHP addenda). Resolution is infallible; unknown / malformed input
 * collapses to Mode::Off.
 */
final class EnvResolver
{
    /**
     * Resolves the current telemetry mode per ADR-0035 + ADR-0038.
     *
     * Precedence (highest wins):
     *   1. <APP>_TELEMETRY_MODE (only when KIT_APP_PREFIX is set)
     *   2. KIT_TELEMETRY_MODE
     *   3. Mode::Off
     *
     * @param array<string, string>|null $env Injected env for testing.
     *                                        When null, defaults to getenv().
     */
    public static function resolveMode(?array $env = null): Mode
    {
        $get = self::getter($env);

        $appPrefix = strtoupper(trim($get('KIT_APP_PREFIX')));
        if ($appPrefix !== '') {
            $v = trim($get($appPrefix . '_TELEMETRY_MODE'));
            if ($v !== '') {
                return Mode::tryFromLoose($v);
            }
        }

        $v = trim($get('KIT_TELEMETRY_MODE'));
        if ($v !== '') {
            return Mode::tryFromLoose($v);
        }

        return Mode::Off;
    }

    /**
     * Honors the DO_NOT_TRACK convention (https://consoledonottrack.com).
     *
     * Per backlog #11, the trigger semantics are broadened: any value
     * that isn't the empty string, '0', or 'false' counts as opt-out.
     *
     * @param array<string, string>|null $env Injected env for testing.
     */
    public static function isDoNotTrack(?array $env = null): bool
    {
        $get = self::getter($env);
        $v = strtolower(trim($get('DO_NOT_TRACK')));

        return $v !== '' && $v !== '0' && $v !== 'false';
    }

    /**
     * @param array<string, string>|null $env
     * @return callable(string): string
     */
    private static function getter(?array $env): callable
    {
        if ($env !== null) {
            return static fn (string $k): string => (string) ($env[$k] ?? '');
        }

        return static function (string $k): string {
            $v = getenv($k);

            return $v === false ? '' : (string) $v;
        };
    }
}
