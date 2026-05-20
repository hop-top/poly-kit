<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter;

/**
 * Kinds of values an OptionSpec accepts. Mirrors py output.formatter.OptionType
 * and go console/output.OptionType.
 */
enum OptionType: string
{
    case String = 'string';
    case Int = 'int';
    case Bool = 'bool';
    case Enum = 'enum';
}
