<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

use GuzzleHttp\Client;
use GuzzleHttp\Psr7\Request;
use HopTop\KitEngine\Exception\HttpException;
use HopTop\KitEngine\Exception\KitEngineException;
use Psr\Http\Client\ClientExceptionInterface;
use Psr\Http\Client\ClientInterface;

/**
 * Thin JSON-over-PSR-18 transport shared by every module.
 *
 * Sends the bearer token on every request when one is set (the engine
 * only enforces it on non-GET/HEAD; sending it on reads is harmless).
 *
 * @internal
 */
final class Http
{
    public function __construct(
        private readonly ClientInterface $client,
        private readonly string $baseUrl,
        private readonly ?string $token = null,
    ) {
    }

    public static function defaultClient(): ClientInterface
    {
        return new Client(['timeout' => 10, 'http_errors' => false]);
    }

    /**
     * @param array<string,scalar> $query
     * @return mixed decoded JSON; null on empty body
     */
    public function request(
        string $method,
        string $path,
        mixed $body = null,
        array $query = [],
        ?string $token = null,
        callable|null $mapError = null,
    ): mixed {
        $uri = $this->baseUrl . $path;
        if ($query !== []) {
            $uri .= '?' . http_build_query($query, '', '&', PHP_QUERY_RFC3986);
        }
        $headers = ['Accept' => 'application/json'];
        $payload = null;
        if ($body !== null) {
            $headers['Content-Type'] = 'application/json';
            $payload = json_encode($body, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
        }
        $tok = $token ?? $this->token;
        if ($tok !== null && $tok !== '') {
            $headers['Authorization'] = 'Bearer ' . $tok;
        }

        try {
            $res = $this->client->sendRequest(new Request($method, $uri, $headers, $payload));
        } catch (ClientExceptionInterface $e) {
            throw new KitEngineException("{$method} {$path}: {$e->getMessage()}", 0, $e);
        }

        $status = $res->getStatusCode();
        $raw = (string) $res->getBody();
        if ($status < 200 || $status >= 300) {
            $err = $mapError !== null ? $mapError($status, $method, $path, $raw) : null;
            throw $err ?? new HttpException($method, $path, $status, $raw);
        }
        if ($status === 204 || $raw === '') {
            return null;
        }
        try {
            return json_decode($raw, true, 512, JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            throw new KitEngineException("{$method} {$path}: invalid JSON response", 0, $e);
        }
    }
}
