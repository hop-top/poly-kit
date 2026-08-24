<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use HopTop\Kit\Output\Formatter\Projection;
use RuntimeException;

/**
 * JSON formatter. Mirrors py/ts/go json built-ins.
 *
 * Options:
 *   - indent (int, default 2) — number of spaces per indent level; 0 = compact
 *
 * Single-row payloads emit a JSON object; list payloads emit a JSON array.
 * Key order follows the resolved $cols (--cols, else the caller's ColumnSpec
 * order), else the payload's own key order — PHP arrays are insertion-
 * ordered, so the resolved order survives json_encode unchanged.
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

    /**
     * @param list<string> $cols resolved column projection
     */
    public function render(mixed $writer, mixed $data, array $opts, array $cols): void
    {
        $indent = is_int($opts['indent'] ?? null) ? (int) $opts['indent'] : 2;
        $projected = Projection::project($data, $cols);

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

}
