<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * One invocable command leaf in the bridged command tree.
 *
 * Ports Go's `Leaf`. The path is the command's segments; the two wire
 * spellings differ and both matter:
 * {@see self::toolName()} joins with the mount's tool-name separator
 * (default ".", the MCP tool name) while {@see self::pathKey()} joins
 * with " " (used in the destructive-block message, which the fixtures
 * pin).
 */
final class Leaf
{
    /** The default string joining a leaf path into its MCP tool name. */
    public const DEFAULT_TOOL_NAME_SEPARATOR = '.';

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

    /**
     * MCP tool name, the path joined with $separator:
     * ["widget","add"] -> "widget.add" (or "widget_add" under "_").
     */
    public function toolName(string $separator = self::DEFAULT_TOOL_NAME_SEPARATOR): string
    {
        return implode($separator, $this->path);
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

    /**
     * Splits an MCP tool name back into a path on $separator.
     *
     * @return list<string>
     */
    public static function pathFromToolName(
        string $name,
        string $separator = self::DEFAULT_TOOL_NAME_SEPARATOR,
    ): array {
        if ('' === $name) {
            return [];
        }

        return explode($separator, $name);
    }
}
