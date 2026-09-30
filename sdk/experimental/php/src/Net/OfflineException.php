<?php

declare(strict_types=1);

namespace HopTop\Kit\Net;

use Psr\Http\Client\ClientExceptionInterface;
use RuntimeException;

/**
 * Thrown when a request is refused because `--offline` is in effect.
 *
 * The PHP analogue of Go's `netpolicy.ErrOffline`. A blocked request
 * always throws — never a silent skip — so a caller can tell "we did not
 * call the network because you asked us not to" apart from "we called it
 * and it returned nothing".
 *
 * Implements PSR-18's {@see ClientExceptionInterface} so that
 * {@see OfflineGuardClient} stays contract-conforming: PSR-18 requires
 * `sendRequest()` to throw nothing else, and a conforming caller's
 * `catch (ClientExceptionInterface)` must therefore see this.
 */
final class OfflineException extends RuntimeException implements ClientExceptionInterface
{
    /**
     * The message names $method and the destination of $url as scheme,
     * host (with port) and path only; see {@see destination()}.
     *
     * @param string $method HTTP method of the refused request.
     * @param string $url    URL of the refused request, as sent.
     */
    public static function forRequest(string $method, string $url): self
    {
        return new self(sprintf(
            '%s %s: network disabled by --offline',
            $method,
            self::destination($url),
        ));
    }

    /**
     * Render $url as scheme, host (with port) and path only: enough to say
     * where a refused request was going, nothing it carried. Query,
     * fragment and userinfo are dropped because they may hold credentials
     * (an API key param, basic-auth userinfo). An opaque URL (`mailto:`)
     * keeps its scheme alone, and a URL that does not parse cannot be
     * stripped reliably, so none of it is echoed. Mirrors Go's
     * `netpolicy` refusal.
     */
    private static function destination(string $url): string
    {
        $parts = parse_url($url);
        if ($parts === false) {
            return '<unparseable URL>';
        }

        $scheme = $parts['scheme'] ?? '';
        $authority = ($parts['host'] ?? '') . (isset($parts['port']) ? ':' . $parts['port'] : '');
        $path = $parts['path'] ?? '';

        if ($authority === '' && !str_starts_with($path, '/')) {
            return $scheme !== '' ? $scheme . ':' : '';
        }

        $prefix = $scheme !== '' ? $scheme . ':' : '';
        if ($authority !== '' || $scheme !== '') {
            $prefix .= '//';
        }

        return $prefix . $authority . $path;
    }
}
