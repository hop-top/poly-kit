<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

use Psr\Http\Message\ResponseFactoryInterface;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\ServerRequestInterface;
use Psr\Http\Message\StreamFactoryInterface;
use Psr\Http\Server\RequestHandlerInterface;

/**
 * PSR-15 binding for the MCP surface.
 *
 * This is a request handler, not a server: kit does not own the adopter's
 * HTTP stack, so binding it to php-fpm, a long-running worker or a
 * framework's middleware pipeline stays the adopter's decision. Being a
 * pure request-to-response function also means the wire behaviour is
 * testable against the fixtures with no socket involved.
 *
 * ```php
 * $handler = new RequestHandler(
 *     new Bridge($root, Policy::default()),
 *     new Mount(serverInfo: new ServerInfo('my-cli', '1.4.0')),
 *     $responseFactory,
 *     $streamFactory,
 * );
 *
 * $response = $handler->handle($serverRequest);
 * ```
 *
 * The PSR-17 factories are injected rather than discovered so the handler
 * works in a stack that has no discovery package installed.
 */
final readonly class RequestHandler implements RequestHandlerInterface
{
    private Dispatcher $dispatcher;

    public function __construct(
        Bridge $bridge,
        private Mount $mount,
        private ResponseFactoryInterface $responseFactory,
        private StreamFactoryInterface $streamFactory,
    ) {
        $this->dispatcher = $mount->dispatcher($bridge);
    }

    public function handle(ServerRequestInterface $request): ResponseInterface
    {
        // A post-session server answers the session verbs with 405. The
        // POST route is unaffected.
        if ('POST' !== strtoupper($request->getMethod())) {
            return $this->toPsr(new Response(
                405,
                Json::encodeEnvelope(
                    id: null,
                    error: ['code' => ErrorCodes::INVALID_REQUEST, 'message' => 'method not allowed'],
                    omitId: true,
                ),
            ));
        }

        return $this->toPsr($this->dispatcher->dispatch(
            (string) $request->getBody(),
            $request->getHeaders(),
        ));
    }

    /** The mount path an adopter should route to this handler. */
    public function path(): string
    {
        return $this->mount->path;
    }

    private function toPsr(Response $response): ResponseInterface
    {
        $psr = $this->responseFactory->createResponse($response->status);

        foreach ($response->headers as $name => $value) {
            $psr = $psr->withHeader($name, $value);
        }

        if ('' === $response->body) {
            return $psr;
        }

        return $psr->withBody($this->streamFactory->createStream($response->body));
    }
}
