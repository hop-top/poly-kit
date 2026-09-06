<?php

declare(strict_types=1);

namespace HopTop\Kit\Output;

use HopTop\Kit\Output\Formatter\Builtin\CsvFormatter;
use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use HopTop\Kit\Output\Formatter\Builtin\TableFormatter;
use HopTop\Kit\Output\Formatter\Builtin\TextFormatter;
use HopTop\Kit\Output\Formatter\Builtin\YamlFormatter;

/**
 * Built-in formatter registration: table + json + yaml + csv + text.
 *
 * 'table' is the default --format and intentionally minimal: pipe-friendly
 * ASCII, no borders, no color. Adopters wanting richer tables (borders,
 * alignment, theming) override the 'table' key with their own Formatter.
 *
 * Registry::default() invokes Builtins::register() exactly once.
 */
final class Builtins
{
    public static function register(Registry $r): void
    {
        $r->register(new CsvFormatter());
        $r->register(new JsonFormatter());
        $r->register(new TableFormatter());
        $r->register(new TextFormatter());
        $r->register(new YamlFormatter());
    }
}
