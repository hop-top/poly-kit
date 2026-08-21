<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * One command-line flag, reflected into a JSON Schema property.
 *
 * Ports the fields of pflag's `Flag` that the surface actually reads.
 * Hidden and deprecated flags are declared here but skipped by
 * {@see ToolEnvelope::collectFlags()}, mirroring Go.
 */
final readonly class FlagSpec
{
    public function __construct(
        public string $name,
        public string $type,
        public string $usage = '',
        public bool $required = false,
        public bool $hidden = false,
        public bool $deprecated = false,
    ) {
    }

    /**
     * Maps a pflag type string to its JSON Schema primitive.
     *
     * Mirrors Go's `mcpJSONType`; unknown types fall back to "string".
     */
    public static function jsonType(string $pflagType): string
    {
        return match ($pflagType) {
            'bool' => 'boolean',
            'int', 'int8', 'int16', 'int32', 'int64',
            'uint', 'uint8', 'uint16', 'uint32', 'uint64',
            'count' => 'integer',
            'float32', 'float64' => 'number',
            'stringArray', 'stringSlice', 'intSlice', 'boolSlice' => 'array',
            default => 'string',
        };
    }

    /**
     * Renders this flag as a JSON Schema property.
     *
     * @return array<string, mixed>
     */
    public function toProperty(): array
    {
        $type = self::jsonType($this->type);
        $prop = ['type' => $type, 'description' => $this->usage];

        if ('array' === $type) {
            $prop['items'] = ['type' => 'string'];
        }

        return $prop;
    }
}
