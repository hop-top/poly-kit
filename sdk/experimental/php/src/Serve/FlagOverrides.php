<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The per-run overrides the `serve` flags apply on top of the resolved
 * `services.*` blocks. Pure: every method returns a new map and leaves
 * the caller's configuration untouched, so a second `serve` in one
 * process does not inherit the first run's flags.
 */
final class FlagOverrides
{
    /**
     * Applies `--enable` / `--disable`.
     *
     * An unconfigured service becomes configured the moment an operator
     * names it in `--enable`: the flag is the aggregate equivalent of
     * the selector's override rule. `--disable` on a configured service
     * clears its enablement so the supervisor form skips it silently;
     * on an unconfigured service it is a no-op. Enable wins over disable
     * for the same name — the affirmative act is the more specific one.
     *
     * @param array<string, ServiceConfig> $configs
     * @param list<string> $enable
     * @param list<string> $disable
     * @return array<string, ServiceConfig>
     */
    public static function applyEnableDisable(array $configs, array $enable, array $disable): array
    {
        $out = $configs;
        foreach ($disable as $name) {
            if (isset($out[$name])) {
                $out[$name] = self::withEnabled($out[$name], false);
            }
        }
        foreach ($enable as $name) {
            $out[$name] = isset($out[$name])
                ? self::withEnabled($out[$name], true)
                : new ServiceConfig(enabled: true);
        }
        return $out;
    }

    /**
     * Applies `--ready-timeout` / `--stop-timeout` across every resolved
     * service. The flags map onto the per-service keys, so a flag set on
     * the supervisor form applies the same budget to every member of
     * the set — which is what an operator tuning a whole process, rather
     * than one service, is asking for.
     *
     * @param array<string, ServiceConfig> $configs
     * @return array<string, ServiceConfig>
     */
    public static function applyTimeouts(array $configs, ?float $readyTimeout, ?float $stopTimeout): array
    {
        $ready = $readyTimeout !== null && $readyTimeout > 0;
        $stop = $stopTimeout !== null && $stopTimeout > 0;
        if (!$ready && !$stop) {
            return $configs;
        }
        $out = [];
        foreach ($configs as $name => $cfg) {
            $out[$name] = new ServiceConfig(
                enabled: $cfg->enabled,
                readyTimeout: $ready ? $readyTimeout : $cfg->readyTimeout,
                stopTimeout: $stop ? $stopTimeout : $cfg->stopTimeout,
            );
        }
        return $out;
    }

    private static function withEnabled(ServiceConfig $cfg, bool $enabled): ServiceConfig
    {
        return new ServiceConfig(
            enabled: $enabled,
            readyTimeout: $cfg->readyTimeout,
            stopTimeout: $cfg->stopTimeout,
        );
    }
}
