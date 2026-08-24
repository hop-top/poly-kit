<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Net;

use GuzzleHttp\Psr7\Response;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestInterface;
use Psr\Http\Message\ResponseInterface;

/**
 * PSR-18 stub that records whether it was reached, so a guard test can
 * assert the request stopped short of the inner client rather than only
 * that an exception surfaced. Never touches the network.
 *
 * A named class rather than an anonymous one so the `$called` flag stays
 * visible to static analysis through the declared return type.
 */
final class RecordingClient implements ClientInterface
{
    public bool $called = false;

    public function sendRequest(RequestInterface $request): ResponseInterface
    {
        $this->called = true;

        return new Response(200, [], 'reached the network');
    }
}
