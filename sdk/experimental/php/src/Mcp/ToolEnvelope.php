<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Renders a leaf as an MCP tool descriptor.
 *
 * Ports Go's `buildToolEnvelope` / `collectFlags`. Both eras call this,
 * so a tool's schema cannot drift between 2024-11-05 and 2026-07-28.
 */
final class ToolEnvelope
{
    /**
     * @return array{name: string, description: string, inputSchema: array<string, mixed>}
     */
    public static function build(Leaf $leaf): array
    {
        [$properties, $required] = self::collectFlags($leaf);

        $schema = [
            'type' => 'object',
            // Always emitted, as `{}` when empty — Go marshals an empty
            // non-nil map, never omitting the member.
            'properties' => [] === $properties ? new \stdClass() : $properties,
        ];

        if ([] !== $required) {
            $schema['required'] = $required;
        }

        return [
            'name' => $leaf->toolName(),
            'description' => $leaf->description,
            'inputSchema' => $schema,
        ];
    }

    /**
     * Collects schema properties and the required-flag list.
     *
     * Hidden and deprecated flags are skipped; the first declaration of a
     * name wins. Cobra's implicit `help` flag is neither hidden nor
     * deprecated, so it is included once registered.
     *
     * @return array{array<string, mixed>, list<string>}
     */
    private static function collectFlags(Leaf $leaf): array
    {
        $properties = [];
        $required = [];

        foreach ($leaf->flags as $flag) {
            if ($flag->hidden || $flag->deprecated) {
                continue;
            }

            if (\array_key_exists($flag->name, $properties)) {
                continue;
            }

            $properties[$flag->name] = $flag->toProperty();

            if ($flag->required) {
                $required[] = $flag->name;
            }
        }

        sort($required, \SORT_STRING);

        return [$properties, $required];
    }
}
