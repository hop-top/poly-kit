<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use HopTop\Kit\Output\Formatter\Projection;
use RuntimeException;

/**
 * Plain-text formatter — kv / lines / paragraph styles.
 *
 * Mirrors go console/output/text.go byte-for-byte, which py and ts also
 * reproduce exactly. No library is involved or wanted: the three styles are
 * trivial renderers over an ordered column list, and matching the existing
 * implementations exactly is the entire job.
 *
 * - style=kv (default): "HEADER<sep>VALUE\n" per field, blank line BETWEEN
 *   records (never trailing).
 * - style=lines: values tab-joined, one record per line, no header.
 * - style=paragraph: "Record N:\n" then "  HEADER: VALUE\n" lines, blank
 *   line BETWEEN records. N is 1-indexed.
 *
 * Column order arrives pre-resolved in $cols; payload key order is the
 * fallback when it is empty. Zero rows emits nothing, guarded on ROW count.
 *
 * Options:
 *   - style (enum kv|lines|paragraph, default kv) — output style.
 *   - separator (string, default "=") — kv separator (kv style only).
 */
final class TextFormatter implements Formatter
{
    private const STYLE_KV = 'kv';
    private const STYLE_LINES = 'lines';
    private const STYLE_PARAGRAPH = 'paragraph';

    public function key(): string
    {
        return 'text';
    }

    public function extensions(): array
    {
        return ['.txt'];
    }

    public function options(): array
    {
        return [
            new OptionSpec(
                name: 'style',
                type: OptionType::Enum,
                usage: 'output style',
                default: self::STYLE_KV,
                enum: [self::STYLE_KV, self::STYLE_LINES, self::STYLE_PARAGRAPH],
            ),
            new OptionSpec(
                name: 'separator',
                type: OptionType::String,
                usage: 'kv separator (kv style only)',
                default: '=',
            ),
        ];
    }

    /**
     * @param array<string,mixed> $opts
     * @param list<string>        $cols resolved column projection
     */
    public function render(mixed $writer, mixed $data, array $opts, array $cols): void
    {
        $rows = Projection::normalize($data);

        // Zero rows emits nothing. Guarded on ROW count, never on column
        // count — $cols is populated for row-less payloads too.
        if ($rows === []) {
            return;
        }

        $columns = Projection::resolveColumns($rows, $cols);

        $style = is_string($opts['style'] ?? null) && $opts['style'] !== ''
            ? $opts['style']
            : self::STYLE_KV;

        switch ($style) {
            case self::STYLE_KV:
                $sep = is_string($opts['separator'] ?? null) && $opts['separator'] !== ''
                    ? $opts['separator']
                    : '=';
                self::renderKv($writer, $rows, $columns, $sep);
                return;
            case self::STYLE_LINES:
                self::renderLines($writer, $rows, $columns);
                return;
            case self::STYLE_PARAGRAPH:
                self::renderParagraph($writer, $rows, $columns);
                return;
            default:
                throw new RuntimeException(
                    sprintf('text formatter: unknown style "%s"', $style),
                );
        }
    }

    /**
     * @param list<mixed>  $rows
     * @param list<string> $columns
     */
    private static function renderKv(mixed $writer, array $rows, array $columns, string $sep): void
    {
        foreach ($rows as $i => $row) {
            if ($i > 0) {
                self::write($writer, "\n");
            }
            foreach ($columns as $c) {
                self::write($writer, $c . $sep . self::cell($row, $c) . "\n");
            }
        }
    }

    /**
     * @param list<mixed>  $rows
     * @param list<string> $columns
     */
    private static function renderLines(mixed $writer, array $rows, array $columns): void
    {
        foreach ($rows as $row) {
            $cells = [];
            foreach ($columns as $c) {
                $cells[] = self::cell($row, $c);
            }
            self::write($writer, implode("\t", $cells) . "\n");
        }
    }

    /**
     * @param list<mixed>  $rows
     * @param list<string> $columns
     */
    private static function renderParagraph(mixed $writer, array $rows, array $columns): void
    {
        foreach ($rows as $i => $row) {
            if ($i > 0) {
                self::write($writer, "\n");
            }
            self::write($writer, sprintf("Record %d:\n", $i + 1));
            foreach ($columns as $c) {
                self::write($writer, '  ' . $c . ': ' . self::cell($row, $c) . "\n");
            }
        }
    }

    private static function write(mixed $writer, string $s): void
    {
        if (fwrite($writer, $s) === false) {
            throw new RuntimeException('text: write failed');
        }
    }

    private static function cell(mixed $row, string $key): string
    {
        $val = is_array($row) && array_key_exists($key, $row) ? $row[$key] : null;
        return self::stringify($val);
    }

    /**
     * Absent and null both become the empty string, matching go's zero-value
     * rendering and py/ts's null handling.
     */
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
}
