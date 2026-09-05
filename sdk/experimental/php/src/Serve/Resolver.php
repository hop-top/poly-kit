<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use HopTop\Kit\Output\CliError;

/**
 * Turns a `serve` invocation into a runnable set, applying the
 * hierarchy and the override rule. Pure: nothing is started, nothing
 * binds, nothing is written.
 *
 * This is the load-bearing part of the whole contract. An operator's
 * systemd unit, container entrypoint, or CI script is written against
 * the hierarchy and the override rule and against nothing else.
 */
final class Resolver
{
    /**
     * Resolves $args against $registry.
     *
     * Selector form runs the named service **even when
     * `services.<name>.enabled` is false**, provided all three gates
     * pass in order — registration, then configuration, then policy.
     * Enablement is not a gate there: an operator naming a service on
     * the command line has already made the decision the flag exists
     * to automate.
     *
     * Supervisor form runs every service that is both configured and
     * enabled, in registration order, skipping a disabled one
     * silently. Resolving to zero services is a usage error, not a
     * clean exit: a process that exits 0 without listening is
     * indistinguishable from a successful start to systemd or a
     * container runtime.
     *
     * @param list<string> $args Positional arguments after the `serve`
     *        word. Empty is the supervisor form, exactly one the
     *        selector form, two or more a usage error.
     * @param array<string, ServiceConfig> $configs Resolved
     *        `services.<name>` blocks. A service with no entry is not
     *        configured, and the supervisor form skips it.
     */
    public static function resolve(
        ServiceRegistry $registry,
        array $args,
        array $configs = [],
        ?PolicyGate $policy = null,
    ): Resolution {
        if (count($args) > 1) {
            return new Resolution(
                outcome: Outcome::InvalidSelection,
                error: CliError::usage(
                    'serve accepts at most one service name, got ' . count($args),
                ),
            );
        }
        if (count($args) === 1) {
            return self::explicit($registry, $args[0], $policy);
        }
        return self::aggregate($registry, $configs, $policy);
    }

    /** The selector form and its override rule. */
    private static function explicit(
        ServiceRegistry $registry,
        string $name,
        ?PolicyGate $policy,
    ): Resolution {
        // Gate 1: registration.
        $svc = $registry->lookup($name);
        if ($svc === null) {
            $known = $registry->names();
            $message = "unknown service \"{$name}\"; known: " . implode(', ', $known);
            $error = new CliError(
                code: CliError::CODE_NOT_FOUND,
                message: $message,
                suggestedFix: self::nearestFix($name, $known),
                exitCode: Outcome::UnknownService->exitCode(),
                transience: CliError::TRANSIENCE_PERMANENT,
            );
            return new Resolution(
                explicit: true,
                error: $error,
                outcome: Outcome::UnknownService,
            );
        }

        // Gate 2: configuration.
        $invalid = self::validateConfig($svc);
        if ($invalid !== null) {
            return new Resolution(
                explicit: true,
                error: CliError::usage("service \"{$name}\": {$invalid}"),
                outcome: Outcome::ConfigInvalid,
            );
        }

        // Gate 3: policy.
        $denied = self::checkPolicy($policy, $svc);
        if ($denied !== null) {
            return new Resolution(
                explicit: true,
                error: $denied,
                outcome: Outcome::PolicyDenied,
            );
        }

        // Enablement is deliberately not consulted here.
        return new Resolution(selected: [$name], explicit: true);
    }

    /** The supervisor form. */
    private static function aggregate(
        ServiceRegistry $registry,
        array $configs,
        ?PolicyGate $policy,
    ): Resolution {
        $selected = [];
        $skipped = [];

        foreach ($registry->names() as $name) {
            $cfg = $configs[$name] ?? null;
            if ($cfg === null) {
                continue; // not configured
            }
            if (!$cfg->enabled) {
                $skipped[] = $name;
                continue;
            }
            $svc = $registry->lookup($name);
            if ($svc === null) {
                continue;
            }

            $invalid = self::validateConfig($svc);
            if ($invalid !== null) {
                return new Resolution(
                    skipped: $skipped,
                    error: CliError::usage("service \"{$name}\": {$invalid}"),
                    outcome: Outcome::ConfigInvalid,
                );
            }
            $denied = self::checkPolicy($policy, $svc);
            if ($denied !== null) {
                return new Resolution(
                    skipped: $skipped,
                    error: $denied,
                    outcome: Outcome::PolicyDenied,
                );
            }
            $selected[] = $name;
        }

        if ($selected === []) {
            return new Resolution(
                skipped: $skipped,
                error: new CliError(
                    code: CliError::CODE_USAGE,
                    message: 'no services configured and enabled; enable one under '
                        . 'services.* or name one explicitly',
                    suggestedFix: 'set services.<name>.enabled: true, or run: serve <service>',
                    exitCode: Outcome::NoServices->exitCode(),
                    transience: CliError::TRANSIENCE_PERMANENT,
                ),
                outcome: Outcome::NoServices,
            );
        }

        return new Resolution(selected: $selected, skipped: $skipped);
    }

    private static function validateConfig(Service $svc): ?string
    {
        return $svc instanceof ValidatesConfig ? $svc->validateConfig() : null;
    }

    private static function checkPolicy(?PolicyGate $gate, Service $svc): ?CliError
    {
        if ($gate === null || !$svc instanceof DeclaresClass) {
            return null;
        }
        [$sideEffect, $network] = $svc->serviceClass();
        $verdict = $gate->allow($sideEffect, $network);
        if ($verdict->ok) {
            return null;
        }
        $msg = "service \"{$svc->name()}\" denied by policy "
            . "(side_effect={$sideEffect}, network={$network})";
        if ($verdict->reason !== '') {
            $msg .= ": {$verdict->reason}";
        }
        return CliError::unauthorized($msg);
    }

    /**
     * The suggestion appended to an unknown-service refusal.
     *
     * The refusal itself is contract; the contract lists the
     * nearest-name suggestion under "explicitly not required", so this
     * is a courtesy. It is kept because the TS port has it and the
     * message shape in the contract's own failure table shows it.
     *
     * @param list<string> $known
     */
    private static function nearestFix(string $want, array $known): string
    {
        $best = null;
        $bestDist = -1;
        // The threshold scales with the typed word's length so a short
        // name does not attract an unrelated suggestion.
        $limit = intdiv(strlen($want), 2) + 1;
        $sorted = $known;
        sort($sorted);
        foreach ($sorted as $k) {
            $d = levenshtein($want, $k);
            if ($d > $limit) {
                continue;
            }
            if ($bestDist === -1 || $d < $bestDist) {
                $best = $k;
                $bestDist = $d;
            }
        }
        return $best === null ? '' : "did you mean \"{$best}\"?";
    }
}
