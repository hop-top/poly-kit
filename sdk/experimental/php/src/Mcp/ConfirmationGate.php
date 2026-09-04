<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Decides whether a confirmation-gated leaf may run.
 *
 * Two strategies exist. Without a configured HMAC key the surface falls
 * back to the `X-Confirm-Token` header check. With one, and a client that
 * advertises form elicitation, the modern MRTR flow applies: the first
 * call answers `input_required` with a prompt and a signed
 * `requestState`, and the client retries carrying the user's decision.
 */
interface ConfirmationGate
{
    /**
     * Returns a refusal or prompt envelope with its HTTP status, or null
     * to let the call proceed.
     *
     * @param array<string, mixed>|null $params
     *
     * @return array{array<string, mixed>, int}|null
     */
    public function check(Leaf $leaf, ?array $params, Request $request): ?array;
}
