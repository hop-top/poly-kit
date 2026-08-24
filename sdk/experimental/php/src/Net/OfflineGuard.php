<?php

declare(strict_types=1);

namespace HopTop\Kit\Net;

use GuzzleHttp\HandlerStack;
use GuzzleHttp\Promise\Create;
use GuzzleHttp\Promise\PromiseInterface;
use Psr\Http\Message\RequestInterface;
use Psr\Http\Message\UriInterface;

/**
 * Enforces the `--offline` marker inside the Guzzle handler stack.
 *
 * # Why the handler stack is the chokepoint
 *
 * Go guards `http.RoundTripper`, beneath every `http.Client`, so no
 * caller can route around the policy. The PHP analogue is the Guzzle
 * handler stack, for the same structural reason: Guzzle's four public
 * entry points — `request()`, `send()`, `requestAsync()`,
 * `sendAsync()` — are siblings, none delegating to another, but all four
 * funnel into `Client::transfer()`, which runs the stack.
 *
 * That rules out the obvious alternative. A decorator implementing only
 * PSR-18's `sendRequest()` would leave `request()` and `send()` — the
 * two methods kit's own egress actually calls — completely unguarded,
 * which is enforcement in name only. {@see OfflineGuardClient} still
 * exists for non-Guzzle PSR-18 clients, where `sendRequest()` is the
 * only seam there is.
 *
 * # Scope
 *
 * This middleware covers HTTP and HTTPS through Guzzle, which is every
 * network client in the PHP port today. It does NOT cover code that
 * opens a socket directly: `fsockopen`, `stream_socket_client`, PDO and
 * other database drivers, or `file_get_contents()` against an http://
 * URL. For those, `--offline` remains advisory and the call site must
 * consult {@see NetPolicy::isOffline()} itself. Closing that gap needs a
 * guarded stream-context wrapper threaded through each such client; it
 * is deliberately not attempted here, matching the Go scope note.
 */
final class OfflineGuard
{
    /**
     * Marks a stack that already carries the guard, so pushing twice
     * does not stack two copies of the middleware.
     */
    private const MIDDLEWARE_NAME = 'hop_top_kit_offline_guard';

    private function __construct()
    {
    }

    /**
     * Push the guard onto $stack. Idempotent: pushing an already guarded
     * stack replaces the existing entry rather than adding a second,
     * because HandlerStack keys middleware by name.
     */
    public static function push(HandlerStack $stack): void
    {
        $stack->remove(self::MIDDLEWARE_NAME);
        $stack->push(self::middleware(), self::MIDDLEWARE_NAME);
    }

    /**
     * Build a fresh handler stack with the guard already installed. This
     * is the construction site kit's own clients use, so a caller who
     * passes no client of their own inherits enforcement without asking.
     */
    public static function stack(): HandlerStack
    {
        $stack = HandlerStack::create();
        self::push($stack);

        return $stack;
    }

    /**
     * The middleware itself: refuse the request when the marker is set
     * and the destination is not loopback, otherwise pass it down.
     *
     * @return callable(callable): callable
     */
    public static function middleware(): callable
    {
        return static function (callable $handler): callable {
            return static function (
                RequestInterface $request,
                array $options,
            ) use ($handler): PromiseInterface {
                $uri = $request->getUri();

                if (NetPolicy::isOffline() && !NetPolicy::isLoopbackHost($uri->getHost())) {
                    // Rejection promise rather than a raw throw: the
                    // async entry points expect a promise, and `wait()`
                    // rethrows this for the synchronous ones. Throwing
                    // directly would escape sendAsync() uncaught.
                    return Create::rejectionFor(OfflineException::forRequest(
                        $request->getMethod(),
                        self::redact($uri),
                    ));
                }

                return $handler($request, $options);
            };
        };
    }

    /**
     * Render $uri with any userinfo password masked. The message is
     * printed and logged, so credentials must not ride along. Mirrors
     * Go's url.URL.Redacted().
     */
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
