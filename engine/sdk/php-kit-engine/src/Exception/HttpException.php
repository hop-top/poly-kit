<?php

declare(strict_types=1);

namespace HopTop\KitEngine\Exception;

/** Non-2xx response from the engine. */
class HttpException extends KitEngineException
{
    public function __construct(
        public readonly string $method,
        public readonly string $path,
        public readonly int $status,
        public readonly string $body = '',
    ) {
        parent::__construct("{$method} {$path}: {$status}", $status);
    }
}
