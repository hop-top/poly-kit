<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Delivers {@see AuditRecord}s to the mount's `audit` hook.
 *
 * Best-effort by contract, as Go's sinks are: the hook is called
 * synchronously once the refusal is decided and before the response is
 * returned, and whatever it does — including throw — never reaches the
 * wire. A missing hook makes every emission a no-op.
 */
final readonly class Auditor
{
    /** @param (\Closure(AuditRecord): void)|null $hook */
    public function __construct(private ?\Closure $hook = null)
    {
    }

    /**
     * Records a refusal of $leaf.
     *
     * @param array<string, string> $extra keys beyond `mcp_spec_version`
     */
    public function refused(Leaf $leaf, SpecVersion $version, string $error, array $extra = []): void
    {
        if (null === $this->hook) {
            return;
        }

        try {
            ($this->hook)(AuditRecord::refusal($leaf, $version, $error, $extra));
        } catch (\Throwable) {
            // An audit sink must never turn a refusal into a different
            // refusal, nor into a success.
        }
    }
}
