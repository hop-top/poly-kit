<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The default gate: a confirmation-gated leaf needs `X-Confirm-Token`.
 *
 * This is what a mount without a confirmation key uses, and what clients
 * that cannot elicit fall back to. It serves the 2026-07-28 handler only;
 * the legacy handler keeps its own inline check.
 */
final readonly class HeaderConfirmationGate implements ConfirmationGate
{
    public function __construct(private Auditor $auditor = new Auditor())
    {
    }

    public function check(Leaf $leaf, ?array $params, Request $request): ?array
    {
        if (!$leaf->class->requiresConfirmation) {
            return null;
        }

        if ('' !== $request->header('X-Confirm-Token')) {
            return null;
        }

        $this->auditor->refused($leaf, SpecVersion::Modern, AuditRecord::ERR_CONFIRMATION_REQUIRED);

        return [CallResult::errorBlock('confirmation required'), 428];
    }
}
