<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The default log sink: logfmt-style key=value lines on stderr.
 *
 * The contract's requirement for a port in exactly this position —
 * "neither a bus nor a structured logger" — is that it "emits the same
 * four transitions with the same field names through whatever it
 * writes to stderr; what it MUST NOT do is stay silent about a service
 * that started, became ready, failed, or stopped".
 *
 * Fields are emitted as discrete `key=value` pairs rather than
 * interpolated into the message, because that is what makes a startup
 * trace greppable across a fleet whose tools are not all the same
 * language: `grep 'service=api'` has to work against a PHP tool's
 * output the same way it works against a Go tool's.
 *
 * A value carrying a space or a quote is quoted so a field never
 * splits into two, which is the one way this format can lie.
 */
final class StderrLogger implements ServeLogger
{
    /** @var resource */
    private mixed $stream;

    /** @param resource|null $stream Defaults to STDERR. */
    public function __construct(mixed $stream = null)
    {
        $this->stream = $stream ?? fopen('php://stderr', 'w');
    }

    public function info(string $message, array $fields = []): void
    {
        $this->write('info', $message, $fields);
    }

    public function error(string $message, array $fields = []): void
    {
        $this->write('error', $message, $fields);
    }

    /** @param array<string, scalar|null> $fields */
    private function write(string $level, string $message, array $fields): void
    {
        $line = 'level=' . $level . ' msg=' . self::quote($message);
        foreach ($fields as $key => $value) {
            if ($value === null || $value === '') {
                continue;
            }
            $line .= ' ' . $key . '=' . self::quote(self::stringify($value));
        }
        fwrite($this->stream, $line . "\n");
    }

    private static function stringify(mixed $value): string
    {
        if (is_bool($value)) {
            return $value ? 'true' : 'false';
        }
        return (string) $value;
    }

    private static function quote(string $value): string
    {
        if ($value !== '' && preg_match('/[\s"=]/', $value) !== 1) {
            return $value;
        }
        return '"' . str_replace(['\\', '"'], ['\\\\', '\\"'], $value) . '"';
    }
}
