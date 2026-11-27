<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

/**
 * Loads and checks the shared corpus in contracts/upgrade-v1. Strict:
 * unknown fields fail, `version` must be `v1`.
 */
trait Corpus
{
    private const HEADER = ['$schema', '$id', 'version', 'description'];

    private static function dir(): string
    {
        $dir = __DIR__;
        while (!is_dir($dir . '/contracts/upgrade-v1')) {
            $parent = dirname($dir);
            if ($parent === $dir) {
                throw new \RuntimeException('contracts/upgrade-v1 not found');
            }
            $dir = $parent;
        }

        return $dir . '/contracts/upgrade-v1';
    }

    /**
     * @param list<string> $allowed
     * @param array<mixed> $obj
     */
    private static function strict(array $obj, array $allowed, string $where): void
    {
        self::assertSame([], array_values(array_diff(array_map('strval', array_keys($obj)), $allowed)), "$where: unknown field");
    }

    /** @return array{0: array<string, mixed>, 1: \stdClass} decoded as arrays, and as objects */
    private static function load(string $file): array
    {
        $text = (string) file_get_contents(self::dir() . '/' . $file);
        /** @var array<string, mixed> $arr */
        $arr = json_decode($text, true, 512, JSON_THROW_ON_ERROR);
        /** @var \stdClass $obj */
        $obj = json_decode($text, false, 512, JSON_THROW_ON_ERROR);
        self::assertSame('v1', $arr['version'] ?? null, "$file: version");
        foreach (['description', '$schema', '$id'] as $k) {
            self::assertIsString($arr[$k] ?? null, "$file: $k");
        }

        return [$arr, $obj];
    }
}
