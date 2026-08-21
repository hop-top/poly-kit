<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * A parsed JSON-RPC request plus the headers the surface validates.
 *
 * Headers keep every occurrence so the duplicate-header rules can tell a
 * benign repeat from a conflicting one; a comma-joined value cannot.
 *
 * `id` is carried as raw JSON text so it round-trips verbatim — `1`,
 * `"abc"` and `null` all come back exactly as sent, and an absent id
 * stays absent rather than becoming null.
 */
final readonly class Request
{
    /**
     * @param array<string, list<string>> $headers header name => all values
     * @param array<string, mixed>|null   $params
     */
    public function __construct(
        public string $jsonrpc = '',
        public string $method = '',
        public ?array $params = null,
        public ?string $rawId = null,
        public array $headers = [],
    ) {
    }

    public function hasId(): bool
    {
        return null !== $this->rawId;
    }

    /** The decoded id value, for emitting into a response envelope. */
    public function id(): mixed
    {
        return null === $this->rawId ? null : Json::decode($this->rawId);
    }

    /**
     * All values sent for $name, matched case-insensitively per RFC 9110.
     *
     * @return list<string>
     */
    public function headerValues(string $name): array
    {
        $needle = strtolower($name);

        foreach ($this->headers as $key => $values) {
            if (strtolower($key) === $needle) {
                return $values;
            }
        }

        return [];
    }

    /**
     * Reduces a header to a single value for body comparison.
     *
     * A header sent once, or repeated with byte-identical values (benign
     * proxy duplication), resolves to that value. Repeated with differing
     * values it is a validation failure in its own right — the
     * multiple-sources-of-truth hazard the header checks exist to close —
     * so `ok` is false and the caller rejects without comparing a value
     * that was never singular.
     *
     * @return array{string, bool} [value, ok]
     */
    public function singleHeaderValue(string $name): array
    {
        $values = $this->headerValues($name);

        if ([] === $values) {
            return ['', true];
        }

        foreach ($values as $value) {
            if ($value !== $values[0]) {
                return ['', false];
            }
        }

        return [$values[0], true];
    }

    /** First value only — the era detector's view, which ignores duplicates. */
    public function header(string $name): string
    {
        return $this->headerValues($name)[0] ?? '';
    }
}
