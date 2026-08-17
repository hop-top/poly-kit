<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter;

/**
 * Column-resolution and row-projection helpers shared by the Dispatcher and
 * the built-in formatters, so no two of them can drift apart.
 *
 * Precedence (identical in every SDK):
 *   1. --cols — user order wins, reordering as well as selecting.
 *   2. ColumnSpec list order.
 *   3. payload key order of the first object-shaped row.
 *
 * Steps 1 and 2 need no payload, so the Dispatcher collapses them into a
 * single ordered list of names via resolveEffectiveCols() and hands that to
 * the formatter through the existing $cols parameter. Formatters only apply
 * step 3, and only when they receive an empty list. That keeps the
 * precedence rule in one place and keeps Formatter::render() — public API —
 * at its original arity, so third-party formatters pick up correct ordering
 * without changing their signature.
 *
 * This collapse is sound only because header == key: an ordered list of
 * header strings carries everything a formatter needs, since each name is
 * simultaneously the label and the row lookup. A header/key split would
 * have forced the ColumnSpec objects themselves through to the formatters.
 */
final class Projection
{
    /**
     * Collapse the user's --cols and the caller's ColumnSpec list into one
     * ordered list of column names, applying precedence rules 1 and 2.
     *
     * Returns [] when neither source applies, which formatters read as
     * "fall back to payload key order".
     *
     * @param list<string>          $cols    user --cols projection (may be empty)
     * @param list<ColumnSpec>|null $columns caller schema (may be null/empty)
     * @return list<string>
     */
    public static function resolveEffectiveCols(array $cols, ?array $columns): array
    {
        // Rule 2: --cols reorders as well as selects; user order always wins.
        if ($cols !== []) {
            return $cols;
        }
        // Rule 1: ColumnSpec list order is the default. header == key, so
        // the header IS the row lookup.
        if ($columns !== null && $columns !== []) {
            return array_map(
                static fn (ColumnSpec $c): string => $c->header,
                $columns,
            );
        }
        return [];
    }

    /**
     * Payload key order of the first object-shaped (non-list) row — the
     * last-resort fallback when no --cols and no ColumnSpec applied.
     *
     * @param list<mixed> $rows
     * @return list<string>
     */
    public static function inferColumns(array $rows): array
    {
        foreach ($rows as $row) {
            if (is_array($row) && !array_is_list($row)) {
                return array_map(static fn ($k): string => (string) $k, array_keys($row));
            }
        }
        return [];
    }

    /**
     * Effective columns for a formatter: the already-resolved $cols when
     * non-empty, else inferred from the payload.
     *
     * @param list<mixed>  $rows
     * @param list<string> $cols
     * @return list<string>
     */
    public static function resolveColumns(array $rows, array $cols): array
    {
        return $cols !== [] ? $cols : self::inferColumns($rows);
    }

    /**
     * Normalize a payload into a list of rows. A single map becomes a
     * one-row list.
     *
     * @return list<mixed>
     */
    public static function normalize(mixed $data): array
    {
        if (is_array($data) && array_is_list($data)) {
            return $data;
        }
        return [$data];
    }

    /**
     * Reshape $row to exactly $columns, in $columns order. Keys absent from
     * the row are omitted (formatters that need a placeholder cell supply
     * their own).
     *
     * Non-array rows pass through untouched — scalars have no keys to
     * project.
     *
     * @param list<string> $columns
     */
    public static function projectRow(mixed $row, array $columns): mixed
    {
        if (!is_array($row)) {
            return $row;
        }
        $out = [];
        foreach ($columns as $c) {
            if (array_key_exists($c, $row)) {
                $out[$c] = $row[$c];
            }
        }
        return $out;
    }

    /**
     * Apply the resolved column projection across a whole payload,
     * preserving the single-row vs list-of-rows shape of $data.
     *
     * Returns $data untouched when $cols is empty, so an unprojected
     * payload serializes with its own key order.
     *
     * @param list<string> $cols already-resolved column names
     */
    public static function project(mixed $data, array $cols): mixed
    {
        if ($cols === []) {
            return $data;
        }
        if (is_array($data) && array_is_list($data)) {
            return array_map(
                static fn (mixed $row): mixed => self::projectRow($row, $cols),
                $data,
            );
        }
        return self::projectRow($data, $cols);
    }
}
