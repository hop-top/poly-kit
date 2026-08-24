<?php

declare(strict_types=1);

namespace HopTop\Kit\Net;

use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestInterface;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\UriInterface;

/**
 * PSR-18 decorator enforcing the `--offline` marker.
 *
 * The companion to {@see OfflineGuard} for adopters who inject a PSR-18
 * client that is not Guzzle, and so has no handler stack to guard.
 * `sendRequest()` is the whole of the PSR-18 surface, which makes
 * wrapping it complete here in a way it would not be for Guzzle, whose
 * `request()` / `send()` bypass it entirely.
 *
 * Prefer {@see OfflineGuard} when the client is Guzzle: guarding the
 * stack covers all four of its entry points rather than the one PSR-18
 * names.
 */
final class OfflineGuardClient implements ClientInterface
{
    public function __construct(private readonly ClientInterface $inner)
    {
    }

    /**
     * Wrap $client unless it is already guarded. Idempotent, so a client
     * handed through several layers picks up exactly one guard.
     */
    public static function wrap(ClientInterface $client): self
    {
        if ($client instanceof self) {
            return $client;
        }

        return new self($client);
    }

    /**
     * @throws OfflineException when the marker is set and the request
     *         targets a non-loopback host. Satisfies PSR-18's
     *         ClientExceptionInterface, so conforming callers catch it.
     */
    public function sendRequest(RequestInterface $request): ResponseInterface
    {
        $uri = $request->getUri();

        if (NetPolicy::isOffline() && !NetPolicy::isLoopbackHost($uri->getHost())) {
            throw OfflineException::forRequest(
                $request->getMethod(),
                self::redact($uri),
            );
        }

        return $this->inner->sendRequest($request);
    }

    /** Mask any userinfo password before the URL reaches a log. */
    private static function redact(UriInterface $uri): string
    {
        $userInfo = $uri->getUserInfo();
        if ($userInfo !== '' && str_contains($userInfo, ':')) {
            [$user] = explode(':', $userInfo, 2);
            $uri = $uri->withUserInfo($user, 'xxxxx');
        }

        return (string) $uri;
    }
}
