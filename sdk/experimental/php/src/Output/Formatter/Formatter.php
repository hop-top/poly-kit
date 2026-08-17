<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter;

/**
 * A Formatter encodes structured data to a writable stream.
 *
 * Implementations declare their key, file extensions, and the option keys
 * they accept. The Dispatcher validates --format-opt input against
 * options() before invoking render(), so render() may trust $opts to
 * contain only declared keys with values coerced to declared types.
 *
 * Mirrors py output.Formatter Protocol, ts Formatter<T> interface, and go
 * console/output.Formatter interface.
 */
interface Formatter
{
    /** Unique format identifier exposed via --format <key>. */
    public function key(): string;

    /**
     * File extensions (with leading dot, e.g. ".csv") that map to this
     * formatter for --output extension inference. May be empty.
     *
     * @return list<string>
     */
    public function extensions(): array;

    /**
     * Option specs accepted by this formatter via --format-opt key=value.
     *
     * @return list<OptionSpec>
     */
    public function options(): array;

    /**
     * Render $data to $writer.
     *
     * $cols arrives already resolved: the Dispatcher has collapsed the
     * user's --cols and the caller's ColumnSpec list into one ordered list
     * of column names, so a formatter never sees that precedence. Each name
     * is both the header label and the row lookup — header == key, so there
     * is no second mapping step. An empty $cols means "no column source
     * applied"; fall back to the payload's own key order.
     *
     * Zero rows emits nothing, not even a bare header row — emptiness is
     * decided by row count, never by column count, because $cols is
     * populated for row-less payloads too.
     *
     * @param resource              $writer fopen-style stream handle
     * @param mixed                 $data   single row or list of rows
     * @param array<string,mixed>   $opts   validated options
     * @param list<string>          $cols   resolved column projection ([] = "infer from payload")
     */
    public function render(mixed $writer, mixed $data, array $opts, array $cols): void;
}
