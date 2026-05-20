<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use RuntimeException;
use Symfony\Component\Yaml\Yaml;

/**
 * YAML formatter. Mirrors py/ts/go yaml built-ins. Uses symfony/yaml which
 * is already a kit-php dependency for telemetry config parsing.
 *
 * Options:
 *   - inline (int, default 4) — depth at which YAML switches from block
 *     to inline style. Higher = more block (more readable for nested).
 */
final class YamlFormatter implements Formatter
{
    public function key(): string
    {
        return 'yaml';
    }

    public function extensions(): array
    {
        return ['.yaml', '.yml'];
    }

    public function options(): array
    {
        return [
            new OptionSpec(
                name: 'inline',
                type: OptionType::Int,
                usage: 'Block→inline switch depth (higher = more block style)',
                default: 4,
            ),
        ];
    }

    public function render(mixed $writer, mixed $data, array $opts, array $cols): void
    {
        $inline = is_int($opts['inline'] ?? null) ? (int) $opts['inline'] : 4;
        $projected = self::project($data, $cols);
        $yaml = Yaml::dump($projected, $inline, 2);
        if (fwrite($writer, $yaml) === false) {
            throw new RuntimeException('yaml: write failed');
        }
    }

    /** @param list<string> $cols */
    private static function project(mixed $data, array $cols): mixed
    {
        if ($cols === []) {
            return $data;
        }
        if (is_array($data) && array_is_list($data)) {
            return array_map(static fn (mixed $row) => self::projectRow($row, $cols), $data);
        }
        return self::projectRow($data, $cols);
    }

    /** @param list<string> $cols */
    private static function projectRow(mixed $row, array $cols): mixed
    {
        if (!is_array($row)) {
            return $row;
        }
        $out = [];
        foreach ($cols as $c) {
            if (array_key_exists($c, $row)) {
                $out[$c] = $row[$c];
            }
        }
        return $out;
    }
}
