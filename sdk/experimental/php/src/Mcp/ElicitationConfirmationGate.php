<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The MRTR confirmation gate (2026-07-28).
 *
 * A confirmation-gated call from an elicitation-capable client answers
 * `resultType: "input_required"` with a single reserved `confirm` form
 * request and a signed `requestState`. The client re-sends the original
 * call carrying the user's decision plus that state, and this gate
 * re-derives the binding and checks it.
 *
 * Clients that do not advertise form elicitation keep the header gate:
 * the spec forbids sending `inputRequests` to a client that cannot answer
 * them, which would otherwise deadlock the call.
 */
final readonly class ElicitationConfirmationGate implements ConfirmationGate
{
    /** The single reserved key under `inputRequests`. */
    public const INPUT_REQUEST_KEY = 'confirm';

    public function __construct(
        private ConfirmationState $state,
        private HeaderConfirmationGate $fallback = new HeaderConfirmationGate(),
        private ?\Closure $clock = null,
    ) {
    }

    public function check(Leaf $leaf, ?array $params, Request $request): ?array
    {
        if (!$leaf->class->requiresConfirmation) {
            return null;
        }

        if (!self::supportsFormElicitation($params)) {
            return $this->fallback->check($leaf, $params, $request);
        }

        $binding = ConfirmationBinding::forCall($leaf, $params, $request->header('Authorization'));
        [$presented, $action] = self::parseRetry($params);

        if ('' === $presented) {
            return [$this->prompt($leaf, $binding), 200];
        }

        $now = null === $this->clock ? time() : ($this->clock)();

        // An unverifiable or stale token re-prompts rather than failing
        // the call: the user can still approve, and a silent retry loop is
        // preferable to surfacing a token error they cannot act on.
        if (ConfirmationStatus::Valid !== $this->state->verify($presented, $binding, $now)) {
            return [$this->prompt($leaf, $binding), 200];
        }

        return match ($action) {
            'accept' => null,
            'decline', 'cancel' => [CallResult::errorBlock('confirmation declined'), 200],
            default => [$this->prompt($leaf, $binding), 200],
        };
    }

    /**
     * The interim result: one elicitation form plus a fresh state token.
     *
     * @return array<string, mixed>
     */
    private function prompt(Leaf $leaf, ConfirmationBinding $binding): array
    {
        $now = null === $this->clock ? time() : ($this->clock)();

        return [
            'resultType' => Protocol::RESULT_TYPE_INPUT_REQUIRED,
            'inputRequests' => [
                self::INPUT_REQUEST_KEY => [
                    'method' => 'elicitation/create',
                    'params' => [
                        'mode' => 'form',
                        'message' => \sprintf('Approve execution of %s?', GoFormat::quote($leaf->toolName())),
                        'requestedSchema' => ['type' => 'object', 'properties' => new \stdClass()],
                    ],
                ],
            ],
            'requestState' => $this->state->mint($binding, $now + ConfirmationState::TTL_SECONDS),
        ];
    }

    /**
     * Reports whether the client advertised form-mode elicitation.
     *
     * An empty `elicitation` object declares support for every mode, so
     * `{}` counts; a populated one must name `form`.
     *
     * @param array<string, mixed>|null $params
     */
    private static function supportsFormElicitation(?array $params): bool
    {
        $capabilities = $params['_meta'][Protocol::META_CLIENT_CAPABILITIES] ?? null;

        if (!\is_array($capabilities)) {
            return false;
        }

        $modes = $capabilities['elicitation'] ?? null;

        if (!\is_array($modes)) {
            return false;
        }

        return [] === $modes || \array_key_exists('form', $modes);
    }

    /**
     * Extracts the echoed state and the user's decision.
     *
     * @param array<string, mixed>|null $params
     *
     * @return array{string, string}
     */
    private static function parseRetry(?array $params): array
    {
        $state = $params['requestState'] ?? null;
        $response = $params['inputResponses'][self::INPUT_REQUEST_KEY] ?? null;

        return [
            \is_string($state) ? $state : '',
            \is_array($response) && \is_string($response['action'] ?? null) ? $response['action'] : '',
        ];
    }
}
