<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use HopTop\Kit\Output\Formatter\Projection;
use RuntimeException;

/**
 * Plain ASCII table formatter. Default --format when no extension
 * inference applies, so bare `myapp list` works out of the box.
 *
 * Output: header line, padded-column body, columns space-separated.
 * No borders, no Unicode — keeps output pipe-friendly and grep-friendly.
 *
 * Column order: --cols, else the ColumnSpec list, else payload key order.
 * Zero rows emits nothing at all — the header row is suppressed along with
 * the body, because emptiness is a property of the row count and not of
 * whether a header source happened to be supplied.
 *
 * Options:
 *   - header (bool, default true) — set false to suppress the header row.
 *
 * For richer tables (borders, alignment, color), users can override the
 * 'table' key in the registry with their own Formatter implementation.
 */
final class TableFormatter implements Formatter
{
    public function key(): string
    {
        return 'table';
    }

    public function extensions(): array
    {
        return [];
    }

    public function options(): array
    {
        return [
            new OptionSpec(
                name: 'header',
                type: OptionType::Bool,
                usage: 'Emit a header row (default: true)',
                default: true,
            ),
        ];
    }

    /**
     * @param list<string>     $cols
     * @param list<ColumnSpec> $columns
     */
    public function render(mixed $writer, mixed $data, array $opts, array $cols, array $columns = []): void
    {
        $header = !array_key_exists('header', $opts) || $opts['header'] !== false;
        $rows = Projection::normalize($data);

        // Zero rows emits nothing — not even a bare header row. Guarded on
        // ROW count, never on header count: --cols and the ColumnSpec list
        // both supply headers for payloads that have no rows at all.
        if ($rows === []) {
            return;
        }

        $columns = Projection::resolveColumns($rows, $cols, $columns);

        // Pre-compute string cells + per-column widths.
        $cellRows = [];
        $widths = [];
        foreach ($columns as $c) {
            $widths[$c] = $header ? mb_strlen($c) : 0;
        }
        foreach ($rows as $row) {
            $cells = [];
            foreach ($columns as $c) {
                $val = is_array($row) && array_key_exists($c, $row) ? $row[$c] : null;
                $cells[$c] = self::stringify($val);
                $widths[$c] = max($widths[$c], mb_strlen($cells[$c]));
            }
            $cellRows[] = $cells;
        }

        // Emit.
        if ($header) {
            self::writeRow($writer, $columns, array_combine($columns, $columns), $widths);
        }
        foreach ($cellRows as $cells) {
            self::writeRow($writer, $columns, $cells, $widths);
        }
    }

    private static function stringify(mixed $val): string
    {
        if ($val === null) {
            return '';
        }
        if (is_bool($val)) {
            return $val ? 'true' : 'false';
        }
        if (is_scalar($val)) {
            return (string) $val;
        }
        // Arrays / objects: compact JSON keeps cells single-line.
        return (string) json_encode($val, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
    }

    /**
     * @param list<string>          $columns
     * @param array<string,string>  $cells
     * @param array<string,int>     $widths
     */
    private static function writeRow(mixed $writer, array $columns, array $cells, array $widths): void
    {
        $parts = [];
        $last = count($columns) - 1;
        foreach ($columns as $i => $c) {
            $cell = $cells[$c] ?? '';
            // Right-pad every column except the last (avoid trailing spaces).
            $parts[] = $i === $last ? $cell : str_pad($cell, $widths[$c]);
        }
        if (fwrite($writer, implode('  ', $parts) . "\n") === false) {
            throw new RuntimeException('table: write failed');
        }
    }
}
