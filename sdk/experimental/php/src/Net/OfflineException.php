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
     * @param string $method HTTP method of the refused request.
     * @param string $url    Redacted URL of the refused request.
     */
    public static function forRequest(string $method, string $url): self
    {
        return new self(sprintf(
            '%s %s: network disabled by --offline',
            $method,
            $url,
        ));
    }
}
