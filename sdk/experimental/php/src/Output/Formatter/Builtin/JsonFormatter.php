<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use RuntimeException;

/**
 * JSON formatter. Mirrors py/ts/go json built-ins.
 *
 * Options:
 *   - indent (int, default 2) — number of spaces per indent level; 0 = compact
 *
 * Single-row payloads emit a JSON object; list payloads emit a JSON array.
 * --cols is applied as a key projection before encoding.
 */
final class JsonFormatter implements Formatter
{
    public function key(): string
    {
        return 'json';
    }

    public function extensions(): array
    {
        return ['.json'];
    }

    public function options(): array
    {
        return [
            new OptionSpec(
                name: 'indent',
                type: OptionType::Int,
                usage: 'Indent width in spaces (0 = compact)',
                default: 2,
            ),
        ];
    }

    public function render(mixed $writer, mixed $data, array $opts, array $cols): void
    {
        $indent = is_int($opts['indent'] ?? null) ? (int) $opts['indent'] : 2;
        $projected = self::project($data, $cols);

        $flags = JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR;
        if ($indent > 0) {
            $flags |= JSON_PRETTY_PRINT;
        }

        $json = json_encode($projected, $flags);

        // JSON_PRETTY_PRINT hard-codes 4-space indent; rewrite to honor opts.
        if ($indent > 0 && $indent !== 4) {
            $pad = str_repeat(' ', $indent);
            $json = preg_replace_callback(
                '/^( {4})+/m',
                static fn (array $m) => str_repeat($pad, (int) (strlen($m[0]) / 4)),
                $json,
            ) ?? $json;
        }

        if (fwrite($writer, $json . "\n") === false) {
            throw new RuntimeException('json: write failed');
        }
    }

    /**
     * Apply $cols projection. When $cols is empty, return data unchanged.
     * Single-row payloads project keys directly; list payloads project
     * each row.
     *
     * @param list<string> $cols
     */
    private static function project(mixed $data, array $cols): mixed
    {
        if ($cols === []) {
            return $data;
        }
        if (self::isList($data)) {
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

    private static function isList(mixed $data): bool
    {
        return is_array($data) && array_is_list($data);
    }
}
