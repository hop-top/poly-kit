<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry;

use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;

/**
 * In-memory representation of the persisted consent decision.
 *
 * Read from <XDG_CONFIG_HOME>/kit/telemetry.yaml. Per ADR-0038, the
 * loader is infallible: missing files, malformed YAML, missing keys,
 * and unknown states all collapse to the safe `denied` default.
 */
final readonly class Consent
{
    public function __construct(
        public bool $allowed,
        public int $promptVersion,
        public string $decisionSource,
        public ?string $decidedAt = null,
    ) {
    }

    /**
     * Safe default returned when no decision can be parsed.
     */
    public static function denied(): self
    {
        return new self(false, 0, 'config');
    }

    /**
     * Canonical on-disk path:
     *   $XDG_CONFIG_HOME/kit/telemetry.yaml
     * (defaults to $HOME/.config).
     */
    public static function path(): string
    {
        $configHome = getenv('XDG_CONFIG_HOME');
        if ($configHome === false || $configHome === '') {
            $home = (string) ($_SERVER['HOME'] ?? getenv('HOME') ?: '');
            $configHome = $home . '/.config';
        }

        return $configHome . '/kit/telemetry.yaml';
    }

    /**
     * Load the persisted consent decision. Never throws; collapses
     * every non-happy path to Consent::denied().
     */
    public static function load(): self
    {
        $p = self::path();
        if (!file_exists($p)) {
            return self::denied();
        }

        try {
            $data = Yaml::parseFile($p);
        } catch (ParseException) {
            return self::denied();
        }

        if (!is_array($data)) {
            return self::denied();
        }

        $block = $data['telemetry']['consent'] ?? null;
        if (!is_array($block)) {
            return self::denied();
        }

        $state = $block['state'] ?? '';
        if ($state !== 'granted' && $state !== 'denied') {
            return self::denied();
        }

        $decidedAt = $block['decided_at'] ?? null;
        if ($decidedAt !== null && !is_string($decidedAt)) {
            $decidedAt = null;
        }

        return new self(
            allowed: $state === 'granted',
            promptVersion: (int) ($block['prompt_version'] ?? 0),
            decisionSource: (string) ($block['decision_source'] ?? 'config'),
            decidedAt: $decidedAt,
        );
    }
}
