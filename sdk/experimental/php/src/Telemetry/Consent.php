<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry;

use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;

/**
 * In-memory representation of the persisted consent decision.
 *
 * Read from the kit AppConfig at
 * `<XDG_CONFIG_HOME>/kit/config.yaml` under the
 * `kit.telemetry.consent` partition. A pre-refactor layout at
 * `<XDG_CONFIG_HOME>/kit/telemetry.yaml` (bare `telemetry.consent`)
 * is read as a fallback for installs that have not yet been migrated.
 *
 * The loader is infallible: missing files, malformed YAML, missing
 * keys, and unknown states all collapse to the safe `denied` default.
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
     *   $XDG_CONFIG_HOME/kit/config.yaml
     * (defaults to $HOME/.config).
     */
    public static function path(): string
    {
        return self::configHome() . '/kit/config.yaml';
    }

    /**
     * Pre-refactor on-disk path:
     *   $XDG_CONFIG_HOME/kit/telemetry.yaml
     *
     * Read-only fallback consumed by load(); SDK callers should
     * prefer path().
     */
    public static function legacyPath(): string
    {
        return self::configHome() . '/kit/telemetry.yaml';
    }

    private static function configHome(): string
    {
        $configHome = getenv('XDG_CONFIG_HOME');
        if ($configHome === false || $configHome === '') {
            $home = (string) ($_SERVER['HOME'] ?? getenv('HOME') ?: '');
            $configHome = $home . '/.config';
        }

        return $configHome;
    }

    /**
     * Load the persisted consent decision. Never throws; collapses
     * every non-happy path to Consent::denied().
     *
     * Read order: canonical `config.yaml` (`kit.telemetry.consent`)
     * first; falls back to the legacy `telemetry.yaml`
     * (`telemetry.consent`) when the canonical file is absent or
     * lacks the consent block.
     */
    public static function load(): self
    {
        $canonical = self::loadFrom(self::path(), ['kit', 'telemetry', 'consent']);
        if ($canonical !== null) {
            return $canonical;
        }

        $legacy = self::loadFrom(self::legacyPath(), ['telemetry', 'consent']);
        if ($legacy !== null) {
            return $legacy;
        }

        return self::denied();
    }

    /**
     * loadFrom parses one YAML file and walks the supplied key path
     * down to the consent mapping. Returns null on any failure so the
     * caller can fall through to the next candidate.
     *
     * @param list<string> $keyPath
     */
    private static function loadFrom(string $file, array $keyPath): ?self
    {
        if (!file_exists($file)) {
            return null;
        }

        try {
            $data = Yaml::parseFile($file);
        } catch (ParseException) {
            return null;
        }

        if (!is_array($data)) {
            return null;
        }

        $cursor = $data;
        foreach ($keyPath as $key) {
            if (!is_array($cursor) || !array_key_exists($key, $cursor)) {
                return null;
            }
            $cursor = $cursor[$key];
        }

        if (!is_array($cursor)) {
            return null;
        }

        $state = $cursor['state'] ?? '';
        if ($state !== 'granted' && $state !== 'denied') {
            return null;
        }

        $decidedAt = $cursor['decided_at'] ?? null;
        if ($decidedAt !== null && !is_string($decidedAt)) {
            $decidedAt = null;
        }

        return new self(
            allowed: $state === 'granted',
            promptVersion: (int) ($cursor['prompt_version'] ?? 0),
            decisionSource: (string) ($cursor['decision_source'] ?? 'config'),
            decidedAt: $decidedAt,
        );
    }
}
