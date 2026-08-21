<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * A wire response: status, headers, and the exact body bytes.
 *
 * The body is pre-serialized so nothing re-encodes it on the way out —
 * byte-exactness is decided here, not by the hosting stack.
 */
final readonly class Response
{
    /**
     * @param array<string, string> $headers
     */
    public function __construct(
        public int $status,
        public string $body = '',
        public array $headers = ['Content-Type' => 'application/json'],
    ) {
    }

    /** A JSON-RPC result envelope. */
    public static function result(Request $request, mixed $result, int $status = 200): self
    {
        return new self($status, Json::encodeEnvelope(
            id: $request->id(),
            result: $result,
            omitId: !$request->hasId(),
        ));
    }

    /**
     * A JSON-RPC error envelope.
     *
     * $data is omitted from the wire when null, matching Go's `omitempty`.
     */
    public static function error(
        ?Request $request,
        int $code,
        string $message,
        int $status,
        mixed $data = null,
    ): self {
        return new self($status, Json::encodeEnvelope(
            id: $request?->id(),
            error: ['code' => $code, 'message' => $message, 'data' => $data],
            omitId: null === $request || !$request->hasId(),
        ));
    }

    /** The 202 notification acknowledgement: no body, no headers. */
    public static function accepted(): self
    {
        return new self(202, '', []);
    }
}
