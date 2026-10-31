<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli;

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

    /** @var list<string> */
    private const DESTRUCTIVE_NAMES = ['delete', 'destroy', 'drop', 'purge', 'rm', 'remove'];

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
}
