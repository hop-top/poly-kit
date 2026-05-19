<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry;

/**
 * Telemetry mode enum, mirroring the Go ground truth in
 * go/runtime/telemetry/mode.go.
 *
 * Mode is the privacy posture for a telemetry pipeline:
 *  - Off:  no events recorded.
 *  - Anon: minimal pseudonymous metadata only.
 *  - Full: full event attributes per the canonical schema.
 *
 * Every adopter-facing entry point must be infallible; tryFromLoose()
 * never throws and defaults to Off.
 */
enum Mode: string
{
    case Off = 'off';
    case Anon = 'anon';
    case Full = 'full';

    /**
     * Parse a raw env-style string into a Mode.
     *
     * Lowercases, trims, and returns Mode::Off for null, empty, or
     * unknown values. Never throws.
     */
    public static function tryFromLoose(?string $raw): self
    {
        if ($raw === null) {
            return self::Off;
        }
        $low = strtolower(trim($raw));
        if ($low === '') {
            return self::Off;
        }

        return self::tryFrom($low) ?? self::Off;
    }
}
