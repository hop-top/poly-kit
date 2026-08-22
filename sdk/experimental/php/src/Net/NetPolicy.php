<?php

declare(strict_types=1);

namespace HopTop\Kit\Net;

/**
 * Process-wide network policy marker for the `--offline` global.
 *
 * # Why process state, not a per-request context
 *
 * Go threads the marker through `context.Context`, because every Go
 * network call already carries one. PHP has no ambient context and no
 * request-scoped equivalent that reaches a Guzzle handler stack, so the
 * marker lives in process state instead.
 *
 * That is a fit rather than a compromise: `--offline` is a CLI-invocation
 * global, and a CLI invocation is exactly one process. Per-request state
 * would only matter under a long-lived server (php-fpm, Swoole) serving
 * several policies at once — which is not what a `--offline` flag
 * describes. Adopters embedding kit in such a server should wrap a
 * per-client {@see OfflineGuardClient} instead of setting this marker.
 *
 * The marker is advisory on its own. Enforcement is {@see OfflineGuard}
 * (Guzzle handler stack) and {@see OfflineGuardClient} (PSR-18), which
 * refuse the request beneath the caller so a caller who never consults
 * `isOffline()` is still held to the policy.
 */
final class NetPolicy
{
    private static bool $offline = false;

    /** Non-instantiable: this is process state, not an object. */
    private function __construct()
    {
    }

    /**
     * Set the offline marker. Call once during start-up, from the layer
     * that resolved the `--offline` flag.
     *
     * `--offline` is the highest-precedence override: per-command network
     * opt-ins must behave as if their opt-out flag had been passed. The
     * override only forces opt-outs ON — it never un-sets an explicitly
     * passed `--no-*` flag. That precedence rule belongs to the flag
     * layer that calls this; the marker itself just records the bit.
     */
    public static function setOffline(bool $offline): void
    {
        self::$offline = $offline;
    }

    /** Report whether network access is disabled. */
    public static function isOffline(): bool
    {
        return self::$offline;
    }

    /** Clear the marker. Primarily for tests. */
    public static function reset(): void
    {
        self::$offline = false;
    }

    /**
     * Report whether $host names a loopback destination, which stays
     * reachable under `--offline`.
     *
     * `--offline` means "do not talk to the network", not "do not talk to
     * myself": a local dev backend, a `kit serve` peer on 127.0.0.1 and
     * unix sockets stay usable so offline workflows still work.
     *
     * Hosts that are not literal loopback IPs are treated as remote —
     * DNS names included, even ones that would resolve to loopback,
     * because performing that resolution is itself network access.
     *
     * @param string $host Authority as it appears in a URI: bare host,
     *        `host:port`, or a bracketed IPv6 literal.
     */
    public static function isLoopbackHost(string $host): bool
    {
        // An empty authority means there is no remote to reach: a unix
        // socket or a relative URI. Nothing leaves the machine.
        if ($host === '') {
            return true;
        }

        $h = self::stripPort($host);

        if (strcasecmp($h, 'localhost') === 0) {
            return true;
        }

        // 127.0.0.0/8 in full, matching Go's net.IP.IsLoopback — not just
        // the canonical 127.0.0.1.
        if (filter_var($h, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false) {
            return str_starts_with($h, '127.');
        }

        if (filter_var($h, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) !== false) {
            return @inet_pton($h) === @inet_pton('::1');
        }

        // Not a literal IP: a DNS name, hence remote.
        return false;
    }

    /**
     * Reduce a URI authority to its bare host: unwrap a bracketed IPv6
     * literal and drop a trailing `:port`.
     */
    private static function stripPort(string $host): string
    {
        // Bracketed IPv6, optionally with a port: [::1] or [::1]:8080.
        if (str_starts_with($host, '[')) {
            $close = strpos($host, ']');
            if ($close !== false) {
                return substr($host, 1, $close - 1);
            }

            return $host;
        }

        // A bare IPv6 literal has several colons; only strip a port when
        // there is exactly one, which makes it host:port.
        if (substr_count($host, ':') === 1) {
            return substr($host, 0, (int) strpos($host, ':'));
        }

        return $host;
    }
}
