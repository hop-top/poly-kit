<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry;

use HopTop\Kit\Telemetry\Sink\JsonlSink;
use HopTop\Kit\Telemetry\Sink\NullSink;
use HopTop\Kit\Telemetry\Sink\SinkInterface;
use Throwable;

/**
 * Adopter-facing facade for telemetry recording.
 *
 * Resolution order at `record()`:
 *   1. If DO_NOT_TRACK is set, no-op.
 *   2. Resolve mode via {@see EnvResolver::resolveMode()}; Mode::Off → no-op.
 *   3. Consent::load() must report `allowed = true`; otherwise no-op.
 *      (The Go CLI owns the prompt + persisted consent file; PHP SDK
 *       is read-only here. Run `kit telemetry enable` to opt in.)
 *   4. Stamp the envelope (schema_version, sdk_lang, installation_id,
 *      mode, occurred_at, event, attrs?) and enqueue on the active sink.
 *
 * Sink selection (KIT_TELEMETRY_SINK):
 *   * unset / `jsonl` → JsonlSink (FPM-safe, default).
 *   * `none`          → no-op (envelope is dropped silently).
 *   * `https`         → not wired by the facade; adopters who need
 *                       HTTPS construct {@see Sink\HttpsSink} themselves
 *                       and pass it to {@see setSink()}. Setting the env
 *                       var to `https` reports via
 *                       {@see setSinkErrReporter()} and falls back to
 *                       JsonlSink.
 *   * anything else   → reported via {@see setSinkErrReporter()}, then
 *                       JsonlSink. An explicitly-set value is never
 *                       ignored without a diagnostic.
 *
 * Attributes are routed through {@see Redactor} before emission. In
 * Anonymous mode the attrs are dropped entirely (envelope still carries
 * event + installation_id + mode + occurred_at, matching the
 * cross-language contract).
 *
 * Every entry point is best-effort: nothing thrown by a sink, the
 * resolver, or the redactor is allowed to escape `record()`.
 */
final class Telemetry
{
    private static ?SinkInterface $sink = null;
    private static ?Redactor $redactor = null;

    /** @var (callable(string): void)|null */
    private static $sinkErrReporter = null;

    /**
     * Record an event. No-op when telemetry is off or consent is denied.
     *
     * @param array<string, mixed> $attrs Event attributes per
     *        sdk/docs/telemetry-event-schema.md.
     */
    public static function record(string $eventName, array $attrs = []): void
    {
        try {
            if (EnvResolver::isDoNotTrack()) {
                return;
            }

            $mode = EnvResolver::resolveMode();
            if ($mode === Mode::Off) {
                return;
            }

            $consent = Consent::load();
            if (!$consent->allowed) {
                return;
            }

            $sink = self::sink();
            $redactor = self::redactor();

            $envelope = [
                'schema_version' => '1',
                'sdk_lang' => 'php',
                'installation_id' => self::installIdSafe(),
                'mode' => $mode->value,
                'occurred_at' => gmdate('Y-m-d\TH:i:s\Z'),
                'event' => $eventName,
            ];

            if ($mode === Mode::Full && !empty($attrs)) {
                $envelope['attrs'] = $redactor->redactAttrs($attrs);
            }

            $sink->enqueue($envelope);
        } catch (Throwable) {
            // Telemetry MUST NOT surface failures into adopter code.
        }
    }

    /**
     * Inject a sink (test / advanced adopter use). Replaces any
     * previously installed sink.
     */
    public static function setSink(?SinkInterface $sink): void
    {
        self::$sink = $sink;
    }

    /**
     * Inject a redactor (test / advanced adopter use). Pass null to
     * restore the default.
     */
    public static function setRedactor(?Redactor $redactor): void
    {
        self::$redactor = $redactor;
    }

    /**
     * Install the receiver for sink-selection diagnostics. Pass null to
     * restore the no-op default.
     *
     * Process-wide by design: the sink is itself process-wide
     * configuration, so the reporter follows the same scope. Adopters
     * who want misconfiguration surfaced wire this to their logger:
     *
     * ```php
     * Telemetry::setSinkErrReporter(
     *     static fn (string $m) => error_log($m),
     * );
     * ```
     *
     * @param (callable(string): void)|null $reporter
     */
    public static function setSinkErrReporter(?callable $reporter): void
    {
        self::$sinkErrReporter = $reporter;
    }

    /**
     * @return callable(string): void
     */
    private static function noopReporter(): callable
    {
        return static function (string $message): void {
            // Quiet by default; see setSinkErrReporter().
        };
    }

    /**
     * Force the next call to `record()` to flush the active sink.
     * Adopters running long-lived CLIs may call this periodically.
     */
    public static function flush(): void
    {
        try {
            self::sink()->flush();
        } catch (Throwable) {
            // see record()
        }
    }

    /**
     * Reset cached singletons. Test-only.
     */
    public static function resetForTest(): void
    {
        self::$sink = null;
        self::$redactor = null;
        self::$sinkErrReporter = null;
    }

    private static function sink(): SinkInterface
    {
        if (self::$sink !== null) {
            return self::$sink;
        }

        $choice = strtolower(trim((string) getenv('KIT_TELEMETRY_SINK')));

        if ($choice === 'https') {
            self::warn(
                'KIT_TELEMETRY_SINK=https is not selectable via the '
                . 'environment; construct Sink\HttpsSink and pass it to '
                . 'Telemetry::setSink(). Falling back to the JSONL sink.',
            );
        } elseif ($choice !== '' && $choice !== 'jsonl' && $choice !== 'none') {
            self::warn(sprintf(
                'KIT_TELEMETRY_SINK=%s is not a supported value '
                . '(expected "jsonl" or "none"). Falling back to the '
                . 'JSONL sink.',
                $choice,
            ));
        }

        self::$sink = match ($choice) {
            'none' => new NullSink(),
            default => new JsonlSink(),
        };

        return self::$sink;
    }

    /**
     * Report a sink misconfiguration. Mirrors the Go bus env-sink
     * contract (go/runtime/bus/env_sink.go): diagnostics go to the
     * installed reporter, which defaults to a no-op so the SDK stays
     * stderr-quiet under php-fpm — a library must never write to a
     * stream that may be the live response body.
     *
     * Never throws: a reporter that raises is swallowed, because
     * telemetry stays best-effort even when misconfigured.
     */
    private static function warn(string $message): void
    {
        try {
            (self::$sinkErrReporter ?? self::noopReporter())(
                'kit telemetry: ' . $message,
            );
        } catch (Throwable) {
            // see record()
        }
    }

    private static function redactor(): Redactor
    {
        if (self::$redactor === null) {
            self::$redactor = new Redactor();
        }

        return self::$redactor;
    }

    private static function installIdSafe(): string
    {
        try {
            return InstallId::get();
        } catch (Throwable) {
            return '';
        }
    }
}
