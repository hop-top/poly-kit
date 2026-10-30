<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * One refusal the MCP surface made before adopter code ran.
 *
 * The subset of Go's `cmdsurface.Invocation` an audit sink sees for a
 * pre-flight refusal: the leaf, the surface, when the verdict was
 * reached, and the sentinel naming why. No caller, arguments or flags —
 * nothing a refused request claimed is trusted enough to record.
 *
 * `error` is the Go sentinel's message verbatim, so one sink can read
 * records from every port the same way. `extra` always carries
 * `mcp_spec_version`; a rejected confirmation state adds
 * `mcp_confirm_rejection`.
 */
final readonly class AuditRecord
{
    /** `cmdsurface.ErrAuthRefused`: an auth-required leaf with no established caller. */
    public const ERR_AUTH_REFUSED = 'cmdsurface: authentication refused';

    /** `mcpsdk.ErrConfirmationRequired`: nothing satisfied the confirmation gate. */
    public const ERR_CONFIRMATION_REQUIRED = 'mcpsdk: confirmation required';

    /** `mcpsdk.ErrConfirmationDeclined`: the client's user declined or dismissed the question. */
    public const ERR_CONFIRMATION_DECLINED = 'mcpsdk: confirmation declined';

    /** `mcpsdk.ErrConfirmationStateRejected`: a presented `requestState` failed verification. */
    public const ERR_CONFIRMATION_STATE_REJECTED = 'mcpsdk: confirmation requestState failed verification';

    /** `cmdsurface.ErrDestructiveBlocked`: the leaf exceeds the mount policy's destructive ceiling. */
    public const ERR_DESTRUCTIVE_BLOCKED = 'cmdsurface: destructive command blocked on this surface';

    /** The `extra` value a rejected `requestState` carries under `mcp_confirm_rejection`. */
    public const CONFIRM_REJECTION_STATE = 'request_state_verification_failed';

    /**
     * @param list<string>          $path        the resolved leaf's command segments
     * @param \DateTimeImmutable    $requestedAt when the refusal was decided, UTC
     * @param string                $specVersion `2024-11-05` or `2026-07-28`
     * @param string                $error       the Go sentinel message, verbatim
     * @param array<string, string> $extra       provenance; always holds `mcp_spec_version`
     */
    public function __construct(
        public array $path,
        public \DateTimeImmutable $requestedAt,
        public string $specVersion,
        public string $error,
        public array $extra,
        public string $surface = 'mcp',
    ) {
    }

    /**
     * The record for a refusal decided now.
     *
     * @param array<string, string> $extra keys beyond `mcp_spec_version`, which is always set
     */
    public static function refusal(Leaf $leaf, SpecVersion $version, string $error, array $extra = []): self
    {
        return new self(
            path: $leaf->path,
            requestedAt: new \DateTimeImmutable('now', new \DateTimeZone('UTC')),
            specVersion: $version->value,
            error: $error,
            extra: ['mcp_spec_version' => $version->value, ...$extra],
        );
    }
}
