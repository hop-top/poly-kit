<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli;

use HopTop\Kit\Output\CliError;

/**
 * Side-effect resolution shared by every gate. Ports Go's
 * `cmdmeta.InferredDestructive` and `gateSideEffect`.
 *
 * A leaf whose `kit/side-effect` annotation is absent or empty and whose
 * name is in {@see self::DESTRUCTIVE_NAMES} is destructive-shared. An
 * explicit annotation, valid or malformed, always wins and is never
 * inferred over. This is the only copy of the name table.
 */
final class SideEffect
{
    public const KEY = 'kit/side-effect';

    public const INFERRED = 'destructive-shared';

    public const DRY_RUN = 'kit/dry-run';

    public const DRY_RUN_RATIONALE = 'kit/dry-run-rationale';

    public const DESTRUCTIVE_TOKEN = 'kit/destructive-token';

    public const PERMISSIONS = 'kit/permissions';

    /** @var list<string> */
    private const DESTRUCTIVE_NAMES = ['delete', 'destroy', 'drop', 'purge', 'rm', 'remove'];

    /** @var array<string, string> tier => legacy class */
    private const CLASSES = [
        'read' => 'read',
        'interactive' => 'interactive',
        'write' => 'write',
        'write-local' => 'write',
        'write-shared' => 'write',
        'destructive' => 'destructive',
        'destructive-local' => 'destructive',
        'destructive-shared' => 'destructive',
    ];

    /** @var list<string> */
    private const WRITE_TIERS = ['write', 'write-local', 'write-shared'];

    /** @var list<string> */
    private const DESTRUCTIVE_TIERS = ['destructive', 'destructive-local', 'destructive-shared'];

    private function __construct()
    {
    }

    /**
     * @return list<string> the table, sorted
     */
    public static function destructiveNames(): array
    {
        return self::DESTRUCTIVE_NAMES;
    }

    public static function isDestructiveName(string $name): bool
    {
        return \in_array($name, self::DESTRUCTIVE_NAMES, true);
    }

    /**
     * @param array<string, string> $annotations
     */
    public static function inferredDestructive(string $name, array $annotations): bool
    {
        return ($annotations[self::KEY] ?? '') === '' && self::isDestructiveName($name);
    }

    /**
     * The tier gates act on: declared value, else the inferred tier, else
     * null when nothing is declared or inferable.
     *
     * @param array<string, string> $annotations
     */
    public static function resolve(string $name, array $annotations): ?string
    {
        if (self::inferredDestructive($name, $annotations)) {
            return self::INFERRED;
        }
        $declared = $annotations[self::KEY] ?? '';

        return '' === $declared ? null : $declared;
    }

    public static function isDestructiveTier(?string $tier): bool
    {
        return null !== $tier && \in_array($tier, self::DESTRUCTIVE_TIERS, true);
    }

    public static function isWriteTier(?string $tier): bool
    {
        return null !== $tier && \in_array($tier, self::WRITE_TIERS, true);
    }

    public static function isValidTier(string $tier): bool
    {
        return isset(self::CLASSES[$tier]);
    }

    /** Legacy class of a tier; null outside the vocabulary. */
    public static function classOf(string $tier): ?string
    {
        return self::CLASSES[$tier] ?? null;
    }

    /**
     * The declared `kit/side-effect` when non-empty and naming no tier.
     *
     * @param array<string, string> $annotations
     */
    public static function malformed(array $annotations): ?string
    {
        $declared = $annotations[self::KEY] ?? '';

        return '' !== $declared && !self::isValidTier($declared) ? $declared : null;
    }

    /**
     * What `--dry-run` does on a leaf: opted-out refuses, legacy `supported`
     * allows, write and destructive tiers allow, read is a silent no-op,
     * interactive refuses, untagged and malformed refuse. An inferred
     * destructive leaf resolves as its inferred tier (allow); the gate
     * previews it itself and never invokes its handler.
     *
     * @param array<string, string> $annotations
     */
    public static function resolveDryRun(string $name, array $annotations): DryRunPolicy
    {
        $flag = $annotations[self::DRY_RUN] ?? '';
        if ('opted-out' === $flag) {
            return DryRunPolicy::RejectOptOut;
        }
        if ('supported' === $flag) {
            return DryRunPolicy::Allow;
        }
        $tier = self::resolve($name, $annotations);
        if (null === $tier) {
            return DryRunPolicy::RejectUntagged;
        }
        if (self::isWriteTier($tier) || self::isDestructiveTier($tier)) {
            return DryRunPolicy::Allow;
        }

        return match ($tier) {
            'read' => DryRunPolicy::Noop,
            'interactive' => DryRunPolicy::RejectInteractive,
            default => DryRunPolicy::RejectMalformed,
        };
    }

    /**
     * The USAGE envelope refusing `--dry-run` under $policy, or null.
     *
     * @param array<string, string> $annotations
     */
    public static function dryRunRefusal(string $path, array $annotations, DryRunPolicy $policy): ?CliError
    {
        switch ($policy) {
            case DryRunPolicy::RejectInteractive:
                $msg = "\"{$path}\" does not accept --dry-run: interactive sessions have no batch boundary to scope the preview";
                $fix = 'run without --dry-run';
                break;
            case DryRunPolicy::RejectOptOut:
                $msg = "\"{$path}\" does not support --dry-run: the command explicitly opted out via kit/dry-run: opted-out";
                $why = $annotations[self::DRY_RUN_RATIONALE] ?? '';
                if ('' !== $why) {
                    $msg .= " ({$why})";
                }
                $fix = "run without --dry-run; see the command's documentation for the rationale";
                break;
            case DryRunPolicy::RejectUntagged:
                $msg = "\"{$path}\" cannot apply --dry-run: the command is missing the required kit/side-effect tag";
                $fix = 'adopter must declare the tier on the command annotations (kit/side-effect)';
                break;
            case DryRunPolicy::RejectMalformed:
                $msg = "\"{$path}\" cannot apply --dry-run: the command has a malformed kit/side-effect tag \""
                    . ($annotations[self::KEY] ?? '') . '\" that names no tier';
                $fix = 'adopter must declare one of the kit side-effect tiers';
                break;
            default:
                return null;
        }
        $e = CliError::usage($msg);

        return new CliError($e->code, $e->message, '', $fix, [], $e->exitCode, $e->transience);
    }
}
