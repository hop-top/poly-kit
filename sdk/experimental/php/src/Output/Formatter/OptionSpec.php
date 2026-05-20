<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter;

/**
 * Describes one option accepted by a Formatter via --format-opt key=value.
 * Immutable value object — mirrors py @dataclass(frozen=True) OptionSpec and
 * ts OptionSpec interface (readonly fields).
 *
 * @phpstan-type EnumValues list<string>
 */
final class OptionSpec
{
    /**
     * @param list<string> $enum allowed values when $type === OptionType::Enum
     */
    public function __construct(
        public readonly string $name,
        public readonly OptionType $type,
        public readonly string $usage = '',
        public readonly string|int|bool|null $default = null,
        public readonly array $enum = [],
    ) {
    }
}
