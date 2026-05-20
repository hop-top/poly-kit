<?php

declare(strict_types=1);

namespace HopTop\Kit\Output;

use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use HopTop\Kit\Output\Formatter\Builtin\YamlFormatter;

/**
 * Built-in formatter registration. Phase-1 ships json + yaml; table/csv/text
 * land in Phase-3 once renderers are in place.
 *
 * Registry::default() invokes Builtins::register() exactly once.
 */
final class Builtins
{
    public static function register(Registry $r): void
    {
        $r->register(new JsonFormatter());
        $r->register(new YamlFormatter());
    }
}
