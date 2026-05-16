<?php

declare(strict_types=1);

namespace HopTop\Kit\Api;

use GuzzleHttp\Client;
use GuzzleHttp\ClientInterface;
use Psr\Http\Message\ResponseInterface;

class ApiClient
{
    private ClientInterface $httpClient;

    public function __construct(
        private readonly string $baseURL,
        private readonly ?string $authToken = null,
        ?ClientInterface $httpClient = null,
    ) {
        $this->httpClient = $httpClient ?? new Client();
    }

    /**
     * @param array<string, mixed> $data
     * @return array<string, mixed>
     */
    public function create(array $data): array
    {
        $response = $this->request('POST', '/', ['json' => $data]);

        return $this->decode($response);
    }

    /**
     * @return array<string, mixed>
     */
    public function get(string $id): array
    {
        $response = $this->request('GET', '/' . urlencode($id));

        return $this->decode($response);
    }

    /**
     * @param array<string, scalar> $query
     * @return array<int, array<string, mixed>>
     */
    public function list(array $query = []): array
    {
        $response = $this->request('GET', '/', ['query' => $query]);

        return $this->decode($response);
    }

    /**
     * @param array<string, mixed> $data
     * @return array<string, mixed>
     */
    public function update(string $id, array $data): array
    {
        $response = $this->request('PUT', '/' . urlencode($id), ['json' => $data]);

        return $this->decode($response);
    }

    public function delete(string $id): void
    {
        $this->request('DELETE', '/' . urlencode($id));
    }

    /**
     * @param array<string, mixed> $options
     */
    private function request(string $method, string $path, array $options = []): ResponseInterface
    {
        $url = rtrim($this->baseURL, '/') . $path;

        $options['headers'] = array_merge(
            $options['headers'] ?? [],
            ['Accept' => 'application/json'],
        );

        if ($this->authToken !== null) {
            $options['headers']['Authorization'] = 'Bearer ' . $this->authToken;
        }

        $options['http_errors'] = false;

        $response = $this->httpClient->request($method, $url, $options);
        $status = $response->getStatusCode();

        if ($status >= 400) {
            $body = json_decode((string) $response->getBody(), true);
            if (is_array($body)) {
                throw ApiException::fromResponse($body);
            }
            throw new ApiException(
                status: $status,
                errorCode: 'http_error',
                message: 'HTTP ' . $status,
            );
        }

        return $response;
    }

    private function decode(ResponseInterface $response): array
    {
        return json_decode((string) $response->getBody(), true, flags: JSON_THROW_ON_ERROR);
    }
}
