<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The default gate: a confirmation-gated leaf needs `X-Confirm-Token`.
 *
 * This is what a mount without a confirmation key uses, and what clients
 * that cannot elicit fall back to.
 */
final readonly class HeaderConfirmationGate implements ConfirmationGate
{
    public function check(Leaf $leaf, ?array $params, Request $request): ?array
    {
        if (!$leaf->class->requiresConfirmation) {
            return null;
        }

        if ('' !== $request->header('X-Confirm-Token')) {
            return null;
        }

        return [CallResult::errorBlock('confirmation required'), 428];
    }
}
