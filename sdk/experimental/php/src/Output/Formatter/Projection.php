<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter;

/**
 * Shared column-resolution and row-projection helpers for the built-in
 * formatters, so table/json/yaml cannot drift from each other.
 *
 * Precedence (identical in every SDK):
 *   1. --cols — user order wins, reordering as well as selecting.
 *   2. ColumnSpec list order.
 *   3. payload key order of the first object-shaped row.
 *
 * header == key, so a resolved column name is simultaneously the label and
 * the row lookup — there is no second mapping step.
 */
final class Projection
{
    /**
     * @param list<mixed>      $rows
     * @param list<string>     $cols    user --cols projection (may be empty)
     * @param list<ColumnSpec> $columns caller schema (may be empty)
     * @return list<string>
     */
    public static function resolveColumns(array $rows, array $cols, array $columns): array
    {
        if ($cols !== []) {
            return $cols;
        }
        if ($columns !== []) {
            return array_map(
                static fn (ColumnSpec $c): string => $c->header,
                $columns,
            );
        }
        return self::inferColumns($rows);
    }

    /**
     * Payload key order of the first object-shaped (non-list) row.
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
     * Apply column resolution + projection across a whole payload,
     * preserving the single-row vs list-of-rows shape of $data.
     *
     * Returns $data untouched when no column source applies at all, so a
     * bare scalar or an unschema'd payload serializes as-is.
     *
     * @param list<string>     $cols
     * @param list<ColumnSpec> $columns
     */
    public static function project(mixed $data, array $cols, array $columns): mixed
    {
        if ($cols === [] && $columns === []) {
            return $data;
        }
        $resolved = self::resolveColumns(self::normalize($data), $cols, $columns);
        if ($resolved === []) {
            return $data;
        }
        if (is_array($data) && array_is_list($data)) {
            return array_map(
                static fn (mixed $row): mixed => self::projectRow($row, $resolved),
                $data,
            );
        }
        return self::projectRow($data, $resolved);
    }
}
