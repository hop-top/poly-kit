<?php

declare(strict_types=1);

namespace HopTop\Kit\Api;

class ApiException extends \RuntimeException
{
    public readonly int $status;
    public readonly string $errorCode;

    public function __construct(
        int $status,
        string $errorCode,
        string $message,
        ?\Throwable $previous = null,
    ) {
        $this->status = $status;
        $this->errorCode = $errorCode;
        parent::__construct($message, $status, $previous);
    }

    /**
     * Build from decoded API error response body.
     *
     * @param array{status: int, code: string, message: string} $body
     */
    public static function fromResponse(array $body): self
    {
        return new self(
            status: $body['status'] ?? 500,
            errorCode: $body['code'] ?? 'unknown',
            message: $body['message'] ?? 'unknown error',
        );
    }
}
