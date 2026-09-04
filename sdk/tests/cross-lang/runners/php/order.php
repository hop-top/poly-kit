#!/usr/bin/env php
<?php

declare(strict_types=1);

/**
 * PHP runner for the cross-language column-ordering conformance harness.
 *
 * Reads fixtures/ordering.json, renders every case in every listed format
 * through the in-tree SDK, then RE-PARSES its own rendered bytes to observe
 * the column sequence the formatter actually serialized. Emits one JSON
 * object per case/format to KIT_CROSS_LANG_ORDER_OUT.
 *
 * Re-parsing rather than reporting the input is the point: it is the only way
 * to observe serialized key ORDER. Nothing here sorts keys.
 *
 * The PHP SDK ships table/json/yaml only — csv and text land in a follow-up
 * phase — so extended-format cases report "unsupported" rather than passing
 * silently.
 */

require __DIR__ . '/../../../../experimental/php/vendor/autoload.php';

use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Formatter\Projection;
use HopTop\Kit\Output\Registry;

/** @return array{sequence: list<string>, empty: bool} */
function seqFromTable(string $text): array
{
    $lines = array_values(array_filter(
        explode("\n", $text),
        static fn (string $l): bool => trim($l) !== '',
    ));
    if ($lines === []) {
        return ['sequence' => [], 'empty' => true];
    }
    $parts = preg_split('/\s+/', trim($lines[0])) ?: [];
    return ['sequence' => array_values(array_filter($parts, static fn ($s) => $s !== '')), 'empty' => false];
}

/** @return array{sequence: list<string>, empty: bool} */
function seqFromCsv(string $text): array
{
    $lines = array_values(array_filter(
        explode("\n", $text),
        static fn (string $l): bool => trim($l) !== '',
    ));
    if ($lines === []) {
        return ['sequence' => [], 'empty' => true];
    }
    return ['sequence' => array_map('trim', str_getcsv($lines[0], ',', '"', '\\')), 'empty' => false];
}

/** @return array{sequence: list<string>, empty: bool} */
function seqFromTextFmt(string $text): array
{
    $keys = [];
    foreach (explode("\n", $text) as $ln) {
        if (trim($ln) === '') {
            break;
        }
        $i = strpos($ln, '=');
        if ($i === false) {
            continue;
        }
        $keys[] = trim(substr($ln, 0, $i));
    }
    return ['sequence' => $keys, 'empty' => $keys === []];
}

/** @return array{sequence: list<string>, empty: bool} */
function seqFromJson(string $text): array
{
    if (trim($text) === '') {
        return ['sequence' => [], 'empty' => true];
    }
    // json_decode with assoc=true yields insertion-ordered PHP arrays, so
    // array_keys reflects the serialized key order faithfully.
    $doc = json_decode($text, true, 512, JSON_THROW_ON_ERROR);
    if (is_array($doc) && array_is_list($doc)) {
        if ($doc === []) {
            return ['sequence' => [], 'empty' => true];
        }
        return ['sequence' => array_map('strval', array_keys($doc[0])), 'empty' => false];
    }
    if (!is_array($doc)) {
        return ['sequence' => [], 'empty' => true];
    }
    return ['sequence' => array_map('strval', array_keys($doc)), 'empty' => false];
}

/**
 * Minimal ordered YAML key reader. We deliberately scrape the raw text rather
 * than round-tripping through a YAML parser: the only thing under test is the
 * ORDER of the first record's mapping keys, and reading the emitted bytes
 * keeps the observation closest to what the formatter actually wrote.
 *
 * PHP's YAML formatter emits the sequence dash on its own line where py/ts/rs
 * inline it; both shapes are handled.
 *
 * @return array{sequence: list<string>, empty: bool}
 */
function seqFromYaml(string $text): array
{
    $lines = array_values(array_filter(
        explode("\n", $text),
        static fn (string $l): bool => trim($l) !== '',
    ));
    if ($lines === []) {
        return ['sequence' => [], 'empty' => true];
    }
    if (count($lines) === 1 && trim($lines[0]) === '[]') {
        return ['sequence' => [], 'empty' => true];
    }
    $keys = [];
    $baseIndent = null;
    foreach ($lines as $raw) {
        $ln = $raw;
        $indent = strlen($ln) - strlen(ltrim($ln));
        $ln = ltrim($ln);
        if (str_starts_with($ln, '- ')) {
            if ($keys !== []) {
                break;
            }
            $indent += 2;
            $ln = substr($ln, 2);
        } elseif ($ln === '-') {
            if ($keys !== []) {
                break;
            }
            continue;
        }
        if (preg_match('/^([A-Za-z0-9_.-]+):/', $ln, $m) !== 1) {
            continue;
        }
        if ($baseIndent === null) {
            $baseIndent = $indent;
        }
        if ($indent !== $baseIndent) {
            continue; // nested mapping, not a column
        }
        $keys[] = $m[1];
    }
    return ['sequence' => $keys, 'empty' => $keys === []];
}

$extract = [
    'table' => 'seqFromTable',
    'json' => 'seqFromJson',
    'yaml' => 'seqFromYaml',
    'csv' => 'seqFromCsv',
    'text' => 'seqFromTextFmt',
];

$fixtures = __DIR__ . '/../../fixtures';
$doc = json_decode((string) file_get_contents($fixtures . '/ordering.json'), true, 512, JSON_THROW_ON_ERROR);
$outPath = getenv('KIT_CROSS_LANG_ORDER_OUT');
if ($outPath === false || $outPath === '') {
    fwrite(STDERR, "KIT_CROSS_LANG_ORDER_OUT unset\n");
    exit(1);
}

$registry = Registry::default();
$records = [];

foreach ($doc['cases'] as $case) {
    $formats = $case['formats'] === 'portable'
        ? $doc['portable_formats']
        : $doc['extended_formats'];
    $columns = $case['spec'] === null
        ? null
        : array_map(static fn (string $n): ColumnSpec => new ColumnSpec($n, $n), $case['spec']);

    foreach ($formats as $fmt) {
        $formatter = $registry->lookup($fmt);
        if ($formatter === null) {
            $records[] = ['case' => $case['name'], 'format' => $fmt, 'status' => 'unsupported'];
            continue;
        }
        $cols = Projection::resolveEffectiveCols($case['cols'], $columns);
        $handle = fopen('php://memory', 'r+');
        $formatter->render($handle, $case['rows'], [], $cols);
        rewind($handle);
        $rendered = (string) stream_get_contents($handle);
        fclose($handle);
        $obs = $extract[$fmt]($rendered);
        $records[] = [
            'case' => $case['name'],
            'format' => $fmt,
            'status' => 'ok',
            'sequence' => $obs['sequence'],
            'empty' => $obs['empty'],
        ];
    }
}

// Contract rule 3: a header != key ColumnSpec must not round-trip. PHP
// enforces in the ColumnSpec constructor.
$rejected = false;
try {
    new ColumnSpec('Name', 'name');
} catch (\Throwable) {
    $rejected = true;
}
$records[] = ['case' => 'header-key-enforced', 'format' => '-', 'status' => 'ok', 'rejected' => $rejected];

$lines = [];
foreach ($records as $rec) {
    ksort($rec);
    $lines[] = json_encode($rec, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
}
file_put_contents($outPath, implode("\n", $lines) . "\n");
