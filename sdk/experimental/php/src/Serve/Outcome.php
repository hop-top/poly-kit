<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use HopTop\Kit\Output\CliError;

/**
 * The kinds of ending a serve run can have, and the contract's
 * exit-behavior table verbatim.
 *
 * Codes come from the shared taxonomy in Output\CliError; this module
 * allocates no new numbers. `START_FAILED` and `RUNTIME_CRASH` share
 * exit 1 deliberately: they differ in *when*, not in what an operator
 * does next, and the distinguishing detail belongs in the message and
 * the failed event rather than in a second numeric code.
 */
enum Outcome: string
{
    case CleanStop = 'clean-stop';
    case InvalidSelection = 'invalid-selection';
    case ConfigInvalid = 'config-invalid';
    case NoServices = 'no-services';
    case UnknownService = 'unknown-service';
    case PolicyDenied = 'policy-denied';
    case StartFailed = 'start-failed';
    case RuntimeCrash = 'runtime-crash';
    case ShutdownTimeout = 'shutdown-timeout';

    /** The CODE_* string for the rendered error envelope. */
    public function code(): string
    {
        return match ($this) {
            self::CleanStop => CliError::CODE_OK,
            self::InvalidSelection,
            self::ConfigInvalid,
            self::NoServices => CliError::CODE_USAGE,
            self::UnknownService => CliError::CODE_NOT_FOUND,
            self::PolicyDenied => CliError::CODE_UNAUTHORIZED,
            self::StartFailed,
            self::RuntimeCrash,
            self::ShutdownTimeout => CliError::CODE_GENERIC,
        };
    }

    /** The process exit code for this outcome. */
    public function exitCode(): int
    {
        return match ($this) {
            self::CleanStop => 0,
            self::InvalidSelection,
            self::ConfigInvalid,
            self::NoServices => 2,
            self::UnknownService => 3,
            self::PolicyDenied => 5,
            self::StartFailed,
            self::RuntimeCrash,
            self::ShutdownTimeout => CliError::EXIT_GENERIC,
        };
    }

    /** Whether this outcome exits non-zero. */
    public function isFailure(): bool
    {
        return $this->exitCode() !== 0;
    }

    /**
     * The outcome the process should exit on given everything
     * observed.
     *
     * "Worst" is severity, not exit-code magnitude: any failure beats
     * a clean stop, and among failures the first observed wins,
     * because it is the one that explains the rest. Under `isolate` a
     * process may survive several failures, and the exit code must
     * reflect the worst outcome across the whole run rather than the
     * last one.
     *
     * @param list<self> $observed
     */
    public static function worst(array $observed): self
    {
        $worst = self::CleanStop;
        foreach ($observed as $o) {
            if ($o->isFailure() && !$worst->isFailure()) {
                $worst = $o;
            }
        }
        return $worst;
    }
}
