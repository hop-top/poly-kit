<?php

declare(strict_types=1);

namespace HopTop\KitEngine\Tests\Support;

use GuzzleHttp\Psr7\Response;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestInterface;
use Psr\Http\Message\ResponseInterface;

/** PSR-18 test double: records requests, answers via a responder. */
final class RecordingClient implements ClientInterface
{
    /** @var list<RequestInterface> */
    public array $requests = [];

    /** @var \Closure(RequestInterface): ResponseInterface */
    private \Closure $responder;

    /** @param null|callable(RequestInterface): ResponseInterface $responder */
    public function __construct(?callable $responder = null)
    {
        $this->responder = $responder !== null
            ? \Closure::fromCallable($responder)
            : static fn (): ResponseInterface => self::json(200, []);
    }

    public function sendRequest(RequestInterface $request): ResponseInterface
    {
        $this->requests[] = $request;

        return ($this->responder)($request);
    }

    public static function json(int $status, mixed $body): Response
    {
        return new Response($status, ['Content-Type' => 'application/json'], json_encode($body, JSON_THROW_ON_ERROR));
    }

    public function last(): RequestInterface
    {
        return $this->requests[array_key_last($this->requests)];
    }
}
