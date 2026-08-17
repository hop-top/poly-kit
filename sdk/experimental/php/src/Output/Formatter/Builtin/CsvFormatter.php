<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use HopTop\Kit\Output\Formatter\Projection;
use RuntimeException;

/**
 * CSV formatter.
 *
 * Encoding is hand-rolled rather than delegated to fputcsv or league/csv,
 * and that is deliberate. The three runtimes that already ship a csv
 * formatter do NOT agree on quoting once fields get awkward: go's
 * encoding/csv, python's stdlib csv and ts's csv-stringify are byte-identical
 * on the LF path, while php's own fputcsv quotes trailing spaces and tabs
 * that the other three leave bare. league/csv wraps fputcsv for writing and
 * therefore inherits exactly that divergence — using it would bake php's
 * disagreement into the very port meant to close the portability gap.
 *
 * Go is the reference runtime. Its rule, reproduced exactly below: quote a
 * field iff it contains the active delimiter, a double quote, LF, or CR,
 * **or begins with a space**. Trailing whitespace and tabs are not quoted.
 * Internal quotes are doubled per RFC 4180.
 *
 * CRLF mode also follows go: an embedded LF is rewritten to CRLF inside the
 * quoted field, and a lone CR is DROPPED. ts disagrees on both counts; go
 * wins because go is the stated reference.
 *
 * Column order arrives pre-resolved in $cols (--cols, else the caller's
 * ColumnSpec order); payload key order is the fallback when it is empty.
 * Zero rows emits nothing at all, header row included, because emptiness is
 * a property of the row count and not of whether a header source was
 * supplied.
 *
 * Options:
 *   - delimiter (string, default ",") — single-character field delimiter.
 *   - no-header (bool, default false) — omit the header row.
 *   - quote-all (bool, default false) — wrap every field in quotes.
 *   - crlf (bool, default false) — CRLF line endings instead of LF.
 */
final class CsvFormatter implements Formatter
{
    public function key(): string
    {
        return 'csv';
    }

    public function extensions(): array
    {
        return ['.csv'];
    }

    public function options(): array
    {
        return [
            new OptionSpec(
                name: 'delimiter',
                type: OptionType::String,
                usage: 'field delimiter',
                default: ',',
            ),
            new OptionSpec(
                name: 'no-header',
                type: OptionType::Bool,
                usage: 'omit header row',
                default: false,
            ),
            new OptionSpec(
                name: 'quote-all',
                type: OptionType::Bool,
                usage: 'quote every field, not just those needing it',
                default: false,
            ),
            new OptionSpec(
                name: 'crlf',
                type: OptionType::Bool,
                usage: 'use CRLF line endings (default LF)',
                default: false,
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

        // Zero rows emits nothing — not even a bare header row. Guarded on
        // ROW count, never on column count: $cols is populated for row-less
        // payloads too, and a column-count guard would emit a lone header.
        if ($rows === []) {
            return;
        }

        $delimiter = is_string($opts['delimiter'] ?? null) ? $opts['delimiter'] : ',';
        if (mb_strlen($delimiter) !== 1) {
            throw new RuntimeException(
                "option 'delimiter': delimiter must be exactly one character",
            );
        }

        $noHeader = ($opts['no-header'] ?? false) === true;
        $quoteAll = ($opts['quote-all'] ?? false) === true;
        $crlf = ($opts['crlf'] ?? false) === true;
        $eol = $crlf ? "\r\n" : "\n";

        $columns = Projection::resolveColumns($rows, $cols);

        if (!$noHeader) {
            self::writeRow($writer, $columns, $delimiter, $eol, $crlf, $quoteAll);
        }
        foreach ($rows as $row) {
            $cells = [];
            foreach ($columns as $c) {
                $val = is_array($row) && array_key_exists($c, $row) ? $row[$c] : null;
                $cells[] = self::stringify($val);
            }
            self::writeRow($writer, $cells, $delimiter, $eol, $crlf, $quoteAll);
        }
    }

    /**
     * @param list<string> $cells
     */
    private static function writeRow(
        mixed $writer,
        array $cells,
        string $delimiter,
        string $eol,
        bool $crlf,
        bool $quoteAll,
    ): void {
        $parts = [];
        foreach ($cells as $cell) {
            $parts[] = self::encodeField($cell, $delimiter, $crlf, $quoteAll);
        }
        if (fwrite($writer, implode($delimiter, $parts) . $eol) === false) {
            throw new RuntimeException('csv: write failed');
        }
    }

    /**
     * Encode one field per go's encoding/csv rules.
     */
    private static function encodeField(
        string $field,
        string $delimiter,
        bool $crlf,
        bool $quoteAll,
    ): string {
        if (!$quoteAll && !self::needsQuotes($field, $delimiter)) {
            return $field;
        }
        $out = '"';
        $len = strlen($field);
        for ($i = 0; $i < $len; $i++) {
            $ch = $field[$i];
            if ($ch === '"') {
                // RFC 4180: an embedded quote is doubled.
                $out .= '""';
                continue;
            }
            if ($crlf && $ch === "\n") {
                // go rewrites an embedded LF to the active line ending, so a
                // CRLF document stays internally consistent.
                $out .= "\r\n";
                continue;
            }
            if ($crlf && $ch === "\r") {
                // go DROPS a lone CR in CRLF mode: writing it would otherwise
                // manufacture a spurious record separator on re-read.
                continue;
            }
            $out .= $ch;
        }
        return $out . '"';
    }

    /**
     * Go quotes a field iff it contains the delimiter, a quote, LF or CR, or
     * begins with a space. Note the asymmetry: a LEADING space forces
     * quoting, a trailing one does not, and a tab never does.
     */
    private static function needsQuotes(string $field, string $delimiter): bool
    {
        if (str_starts_with($field, ' ')) {
            return true;
        }
        return str_contains($field, $delimiter)
            || str_contains($field, '"')
            || str_contains($field, "\n")
            || str_contains($field, "\r");
    }

    /**
     * Render one cell. Absent and null both become the empty string,
     * matching go's zero-value rendering and py/ts's null handling.
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
