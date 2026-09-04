<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * One invocable command leaf in the bridged command tree.
 *
 * Ports Go's `Leaf`. The path is the command's segments; the two wire
 * spellings differ and both matter:
 * {@see self::toolName()} joins with "." (the MCP tool name) while
 * {@see self::pathKey()} joins with " " (used in the destructive-block
 * message, which the fixtures pin).
 */
final class Leaf
{
    /**
     * @param list<string>            $path        command segments, e.g. ["widget", "add"]
     * @param list<FlagSpec>          $flags       flags declared on this leaf
     * @param array<string, bool>     $enabled     surface value => enabled
     * @param (\Closure(array<string, mixed>): Result)|null $runner invoked to execute the leaf
     */
    public function __construct(
        public readonly array $path,
        public readonly string $description = '',
        public readonly array $flags = [],
        public readonly SafetyClass $class = new SafetyClass(),
        public array $enabled = [],
        public readonly ?\Closure $runner = null,
    ) {
    }

    /** Dotted MCP tool name: ["widget","add"] -> "widget.add". */
    public function toolName(): string
    {
        return implode('.', $this->path);
    }

    /** Space-joined command path: ["widget","add"] -> "widget add". */
    public function pathKey(): string
    {
        return implode(' ', $this->path);
    }

    public function enabledOn(Surface $surface): bool
    {
        return $this->enabled[$surface->value] ?? false;
    }

    /** Splits a dotted MCP tool name back into a path. */
    public static function pathFromToolName(string $name): array
    {
        if ('' === $name) {
            return [];
        }

        return explode('.', $name);
    }
}
