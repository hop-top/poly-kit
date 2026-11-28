<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Exception;
use Throwable;

/**
 * What stopped one schema's run, with what to roll back.
 *
 * @internal
 */
final class StepFailure extends Exception
{
    /**
     * @param string $stage up, verify, record, final or canceled
     * @param string $version the version being attempted
     * @param list<Migration> $undo
     * @param bool $upFailed the newest `$undo` entry is the step whose `up` failed: it counts as
     *                       applied, but its `down` is never called
     */
    public function __construct(
        public readonly string $stage,
        public readonly string $version,
        public readonly string $from,
        public readonly Throwable $cause,
        public readonly array $undo,
        public readonly bool $upFailed = false,
    ) {
        parent::__construct($cause->getMessage(), 0, $cause);
    }

    public function toException(): UpgradeException
    {
        $msg = $this->cause->getMessage();
        $v = $this->version;

        return match ($this->stage) {
            'verify' => new UpgradeException("verify $v failed: $msg", 0, new VerifyException($msg, 0, $this->cause)),
            'record' => self::record($v, $this->cause),
            'final' => new UpgradeException("final verify $v failed: $msg", 0, new VerifyException($msg, 0, $this->cause)),
            'canceled' => new UpgradeException("up $v canceled: $msg", 0, $this->cause),
            default => new UpgradeException("up $v failed: $msg", 0, $this->cause),
        };
    }

    /** A failed version record; an empty version (the store reads back unversioned) is quoted. */
    public static function record(string $version, Throwable $cause): UpgradeException
    {
        $v = $version === '' ? '""' : $version;

        return new UpgradeException("record $v failed: {$cause->getMessage()}", 0, $cause);
    }
}
