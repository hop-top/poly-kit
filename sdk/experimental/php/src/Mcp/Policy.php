<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Gates which {@see Surface} may invoke a leaf with a given {@see SafetyClass}.
 *
 * Ports Go's `Policy`. The default policy is permissive only on the
 * local-runtime surfaces: destructive commands are confined to `Cli` and
 * `Lib` unless {@see self::$allowDestructiveOn} names more.
 */
final readonly class Policy
{
    /**
     * @param list<Surface> $allowDestructiveOn surfaces on which destructive leaves MAY be invoked.
     *                                          `Cli` and `Lib` are always allowed regardless. An
     *                                          empty list blocks all remote destructive invocations.
     * @param list<Surface> $defaultEnabled     surfaces a leaf is exposed on when its per-command
     *                                          config omits the enabled field
     */
    public function __construct(
        public array $allowDestructiveOn = [],
        public array $defaultEnabled = [],
    ) {
    }

    /**
     * The conservative default: no remote surface may invoke a destructive
     * command; default enablement is CLI + Lib + MCP.
     */
    public static function default(): self
    {
        return new self(
            allowDestructiveOn: [],
            defaultEnabled: [Surface::Cli, Surface::Lib, Surface::Mcp],
        );
    }

    /**
     * Reports whether $class may be invoked via $surface under this policy.
     *
     *  1. `Cli` and `Lib` are always allowed (local runtime).
     *  2. Non-destructive commands are allowed on every other surface.
     *  3. Destructive commands are allowed only when $surface is listed in
     *     {@see self::$allowDestructiveOn}.
     *
     * Surface enablement (per-leaf opt-in) is gated separately; this only
     * enforces the destructive ceiling.
     */
    public function allowed(SafetyClass $class, Surface $surface): bool
    {
        if (Surface::Cli === $surface || Surface::Lib === $surface) {
            return true;
        }

        if (!$class->destructive) {
            return true;
        }

        return \in_array($surface, $this->allowDestructiveOn, true);
    }

    /**
     * Returns the configured defaults, or the package-wide fallback when unset.
     *
     * @return list<Surface>
     */
    public function resolvedDefaults(): array
    {
        if ([] !== $this->defaultEnabled) {
            return $this->defaultEnabled;
        }

        return [Surface::Cli, Surface::Lib, Surface::Mcp];
    }
}
