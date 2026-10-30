<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The stdio binding: newline-delimited JSON-RPC over a pair of streams
 * (by default the process's STDIN/STDOUT), driving the same era dispatch
 * the PSR-15 handler uses.
 *
 * Wire rules (2024-11-05 and 2026-07-28 alike):
 *
 *  - one JSON-RPC message per line; output lines carry no embedded
 *    newline, and the output stream carries protocol messages only —
 *    diagnostics go to the log stream;
 *  - a notification (no `id` member) is never answered;
 *  - a line that is not JSON is answered -32700 with `id: null`;
 *  - a JSON array (a batch) or a non-object is refused -32600 with
 *    `id: null`; an object with an `id` but no `method`, -32600 with
 *    its id; a client response (`result` / `error`, no `method`) is
 *    ignored and logged;
 *  - requests are served one at a time, so replies come in request
 *    order.
 *
 * stdio has no header layer: era detection uses the body markers only
 * (`params._meta` protocolVersion, `server/discover`), and the
 * 2026-07-28 header checks (V4, V6, V7) do not apply. Gates that read
 * headers see none: the verifier gets a header-less request, and the
 * `X-Confirm-Token` gate cannot be satisfied.
 *
 * ```php
 * StdioServer::serve(new Bridge($root, Policy::default()));
 * // returns once the host closes stdin
 * ```
 */
final class StdioServer
{
    /**
     * Serves MCP over stdio until $input reaches end of file.
     *
     * $target is a bridge, served with $mount (default `new Mount()`), or
     * a {@see RequestHandler}, whose own bridge and mount are reused.
     *
     * @param resource|null $input  where requests are read from; default STDIN
     * @param resource|null $output where replies are written; default STDOUT
     * @param resource|null $log    where diagnostics are written; default STDERR
     *
     * @throws \InvalidArgumentException when $mount is passed with a RequestHandler, or the mount refuses the bridge
     */
    public static function serve(
        Bridge|RequestHandler $target,
        ?Mount $mount = null,
        mixed $input = null,
        mixed $output = null,
        mixed $log = null,
    ): void {
        $dispatcher = self::dispatcherFor($target, $mount);
        $input = self::stream($input ?? STDIN, 'input');
        $output = self::stream($output ?? STDOUT, 'output');
        $log = self::stream($log ?? STDERR, 'log');

        while (false !== ($line = fgets($input))) {
            $reply = self::handleLine($line, $dispatcher, $log);

            if (null === $reply) {
                continue;
            }

            // A broken output stream ends the session instead of
            // spinning on writes nobody reads.
            if (false === @fwrite($output, str_ends_with($reply, "\n") ? $reply : $reply."\n")) {
                self::warn($log, 'output error: write failed');

                return;
            }

            fflush($output);
        }
    }

    private static function dispatcherFor(Bridge|RequestHandler $target, ?Mount $mount): Dispatcher
    {
        if ($target instanceof RequestHandler) {
            if (null !== $mount) {
                throw new \InvalidArgumentException(
                    'StdioServer::serve: mount options apply to a bridge; a RequestHandler carries its own',
                );
            }

            return $target->dispatcher(Transport::Stdio);
        }

        return ($mount ?? new Mount())->dispatcher($target, Transport::Stdio);
    }

    /**
     * Frames, parses and dispatches one input line. Returns the reply
     * line, or null when the line gets none.
     *
     * @param resource $log
     */
    private static function handleLine(string $line, Dispatcher $dispatcher, mixed $log): ?string
    {
        $text = trim($line);
        if ('' === $text) {
            return null;
        }

        $decoded = Json::decode($text);

        // json_decode yields null for both bad JSON and a literal null.
        if (null === $decoded && 'null' !== $text) {
            return self::errorLine(ErrorCodes::PARSE, 'parse error: '.JsonSyntaxError::describe($text));
        }

        // Valid JSON, so the first byte names the value's type — the only
        // way to tell {} from [], which both decode to an empty array.
        if ('[' === $text[0]) {
            return self::errorLine(ErrorCodes::INVALID_REQUEST, 'invalid request: batch requests are not supported');
        }

        if ('{' !== $text[0] || !\is_array($decoded)) {
            return self::errorLine(ErrorCodes::INVALID_REQUEST, 'invalid request: not a JSON-RPC object');
        }

        $hasId = \array_key_exists('id', $decoded);

        if (!\is_string($decoded['method'] ?? null)) {
            // A response: this server sends no requests, so there is
            // nothing to correlate it with.
            if (\array_key_exists('result', $decoded) || \array_key_exists('error', $decoded)) {
                self::warn($log, 'ignored a JSON-RPC response from the client');

                return null;
            }

            if (!$hasId) {
                return null;
            }

            return Json::encodeEnvelope(
                id: $decoded['id'],
                error: ['code' => ErrorCodes::INVALID_REQUEST, 'message' => 'invalid request: missing method'],
            );
        }

        $method = $decoded['method'];

        try {
            $response = $dispatcher->dispatch($text, []);
        } catch (\Throwable $e) {
            self::warn($log, $method.': '.$e->getMessage());

            return $hasId
                ? Json::encodeEnvelope(
                    id: $decoded['id'],
                    error: ['code' => ErrorCodes::INTERNAL, 'message' => 'internal error'],
                )
                : null;
        }

        // A notification is never answered, whatever the handler said.
        if (!$hasId || '' === $response->body) {
            return null;
        }

        return $response->body;
    }

    /** One JSON-RPC error line with `"id":null`. */
    private static function errorLine(int $code, string $message): string
    {
        return Json::encodeEnvelope(id: null, error: ['code' => $code, 'message' => $message]);
    }

    /** @param resource $log */
    private static function warn(mixed $log, string $message): void
    {
        @fwrite($log, 'mcp stdio: '.$message."\n");
    }

    /** @return resource */
    private static function stream(mixed $stream, string $name): mixed
    {
        if (!\is_resource($stream)) {
            throw new \InvalidArgumentException(\sprintf('StdioServer::serve: %s must be a stream resource', $name));
        }

        return $stream;
    }
}
