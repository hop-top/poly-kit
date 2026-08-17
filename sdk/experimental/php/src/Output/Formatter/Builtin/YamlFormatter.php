<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use HopTop\Kit\Output\Formatter\Projection;
use RuntimeException;
use Symfony\Component\Yaml\Yaml;

/**
 * YAML formatter. Mirrors py/ts/go yaml built-ins. Uses symfony/yaml which
 * is already a kit-php dependency for telemetry config parsing.
 *
 * Key order follows --cols, else the ColumnSpec list, else the payload's
 * own key order — Yaml::dump walks PHP's insertion-ordered arrays, so the
 * resolved order is emitted verbatim.
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

    /**
     * @param list<string>     $cols
     * @param list<ColumnSpec> $columns
     */
    public function render(mixed $writer, mixed $data, array $opts, array $cols, array $columns = []): void
    {
        $inline = is_int($opts['inline'] ?? null) ? (int) $opts['inline'] : 4;
        $projected = Projection::project($data, $cols, $columns);
        $yaml = Yaml::dump($projected, $inline, 2);
        if (fwrite($writer, $yaml) === false) {
            throw new RuntimeException('yaml: write failed');
        }
    }
}
