<?php

declare(strict_types=1);

namespace HopTop\Kit\Output;

use JsonSerializable;
use RuntimeException;
use Stringable;
use Symfony\Component\Yaml\Yaml;
use Throwable;

/**
 * Structured-error envelope. Mirrors go/console/output/error.go.
 *
 * When a command fails under --format json|yaml, the error is
 * materialized as a CliError and rendered to stderr by renderTo().
 * Plaintext mode (--format table or unset) prints
 * "Code: Message\nFix: ...\n".
 *
 * Wire keys are snake_case (code, message, cause, suggested_fix,
 * alternatives, exit_code, transience); empty optional fields are
 * omitted, mirroring Go's omitempty.
 *
 * $transience classifies the failure for retry decisions (Factor 4):
 * TRANSIENCE_TRANSIENT (retry-worthy), TRANSIENCE_PERMANENT (do not
 * retry), or TRANSIENCE_UNKNOWN. Constructors populate it; renderTo()
 * normalizes an unset value to TRANSIENCE_UNKNOWN so every structured
 * error carries a valid class on the wire.
 */
final class CliError implements JsonSerializable, Stringable
{
    /** Marks a failure a retry may clear (rate limit, timeout, upstream blip). */
    public const string TRANSIENCE_TRANSIENT = 'transient';
    /** Marks a failure retrying cannot clear without changing the input or the environment. */
    public const string TRANSIENCE_PERMANENT = 'permanent';
    /** Marks a failure kit cannot classify; retries are best-effort and bounded. */
    public const string TRANSIENCE_UNKNOWN = 'unknown';

    // Standard codes mapping the cross-tool exit codes (conventions §8.1).
    public const string CODE_OK = 'OK'; // exit 0
    public const string CODE_GENERIC = 'GENERIC'; // exit 1
    public const string CODE_USAGE = 'USAGE'; // exit 2
    public const string CODE_NOT_FOUND = 'NOT_FOUND'; // exit 3
    public const string CODE_CONFLICT = 'CONFLICT'; // exit 4
    public const string CODE_UNAUTHORIZED = 'UNAUTHORIZED'; // exit 5
    public const string CODE_TRANSIENT = 'TRANSIENT'; // exit 6 — Factor-11 transient/retryable
    public const string CODE_PROVENANCE_MISSING = 'PROVENANCE_MISSING'; // exit 65 — Factor-12 refusal
    public const string CODE_RATE_LIMITED = 'RATE_LIMITED'; // exit 64 — Factor-10 budget exceeded

    /**
     * Spec-assigned exit code for transient/retryable failures
     * (Factor 11). Agents branch on it before parsing stderr: exit 6
     * means a retry may clear the failure.
     */
    public const int EXIT_TRANSIENT = 6;
    /** Conventional exit code for Factor-10 rate-limit refusals. */
    public const int EXIT_RATE_LIMITED = 64;
    /**
     * Conventional exit code for Factor-12 strict-mode provenance
     * refusals. Lives at 65 in kit's extension band (alongside
     * RATE_LIMITED at 64): the spec reserves 0-6 for its core taxonomy
     * and leaves >6 to per-tool codes, and kit as a library stays out
     * of the low per-tool range.
     */
    public const int EXIT_PROVENANCE_MISSING = 65;

    /**
     * @param list<string> $alternatives
     * @param Throwable|null $source The error this envelope was built
     *        from (wrap()), kept off the wire — the PHP analogue of
     *        Go's unexported err field for cause inspection.
     */
    public function __construct(
        public readonly string $code = '',
        public readonly string $message = '',
        public readonly string $cause = '',
        public readonly string $suggestedFix = '',
        public readonly array $alternatives = [],
        public readonly int $exitCode = 0,
        public readonly string $transience = '',
        public readonly ?Throwable $source = null,
    ) {
    }

    /**
     * Default transience class for one of the standard codes.
     * Unrecognized (adopter-defined) codes map to TRANSIENCE_UNKNOWN;
     * adopters pass $transience (or use withTransience()) to classify
     * their own codes.
     */
    public static function transienceForCode(string $code): string
    {
        return match ($code) {
            self::CODE_USAGE,
            self::CODE_NOT_FOUND,
            self::CODE_CONFLICT,
            self::CODE_UNAUTHORIZED,
            self::CODE_PROVENANCE_MISSING => self::TRANSIENCE_PERMANENT,
            self::CODE_RATE_LIMITED,
            self::CODE_TRANSIENT => self::TRANSIENCE_TRANSIENT,
            default => self::TRANSIENCE_UNKNOWN,
        };
    }

    /**
     * Builds an envelope that retains $err as $source while rendering
     * as $code and message. Transience defaults from the code via
     * transienceForCode(); use withTransience() to override.
     */
    public static function wrap(Throwable $err, string $code, int $exitCode): self
    {
        return new self(
            code: $code,
            message: $err->getMessage(),
            exitCode: $exitCode,
            transience: self::transienceForCode($code),
            source: $err,
        );
    }

    /** CODE_NOT_FOUND envelope with exit code 3. */
    public static function notFound(string $message): self
    {
        return new self(
            code: self::CODE_NOT_FOUND,
            message: $message,
            exitCode: 3,
            transience: self::TRANSIENCE_PERMANENT,
        );
    }

    /** CODE_CONFLICT envelope with exit code 4. */
    public static function conflict(string $message): self
    {
        return new self(
            code: self::CODE_CONFLICT,
            message: $message,
            exitCode: 4,
            transience: self::TRANSIENCE_PERMANENT,
        );
    }

    /** CODE_UNAUTHORIZED envelope with exit code 5. */
    public static function unauthorized(string $message): self
    {
        return new self(
            code: self::CODE_UNAUTHORIZED,
            message: $message,
            exitCode: 5,
            transience: self::TRANSIENCE_PERMANENT,
        );
    }

    /** CODE_USAGE envelope with exit code 2. */
    public static function usage(string $message): self
    {
        return new self(
            code: self::CODE_USAGE,
            message: $message,
            exitCode: 2,
            transience: self::TRANSIENCE_PERMANENT,
        );
    }

    /**
     * CODE_TRANSIENT envelope with exit code 6 (Factor 11). Use it for
     * failures a retry may clear: upstream timeouts, connection resets,
     * service-unavailable responses.
     */
    public static function transient(string $message): self
    {
        return new self(
            code: self::CODE_TRANSIENT,
            message: $message,
            exitCode: self::EXIT_TRANSIENT,
            transience: self::TRANSIENCE_TRANSIENT,
        );
    }

    /** CODE_RATE_LIMITED envelope with exit code 64 (Factor 10). */
    public static function rateLimited(string $message): self
    {
        return new self(
            code: self::CODE_RATE_LIMITED,
            message: $message,
            exitCode: self::EXIT_RATE_LIMITED,
            transience: self::TRANSIENCE_TRANSIENT,
        );
    }

    /**
     * CODE_PROVENANCE_MISSING envelope with exit code 65 (Factor 12).
     * $detail is a free-form string suitable for the cause slot
     * (typically the JSON-pointer list of offending fields).
     */
    public static function provenanceMissing(string $detail): self
    {
        return new self(
            code: self::CODE_PROVENANCE_MISSING,
            message: 'provenance not recorded for one or more output fields',
            cause: $detail,
            suggestedFix: 'record provenance for synthesized/cached fields before rendering',
            exitCode: self::EXIT_PROVENANCE_MISSING,
            transience: self::TRANSIENCE_PERMANENT,
        );
    }

    /**
     * Copy with $transience set, every other field (including the
     * retained $source) untouched. Copies rather than mutating —
     * readonly properties make in-place writes impossible by
     * construction, matching Go's copy-on-set WithTransience.
     */
    public function withTransience(string $transience): self
    {
        return new self(
            code: $this->code,
            message: $this->message,
            cause: $this->cause,
            suggestedFix: $this->suggestedFix,
            alternatives: $this->alternatives,
            exitCode: $this->exitCode,
            transience: $transience,
            source: $this->source,
        );
    }

    /**
     * Wire form: snake_case keys, empty optional fields omitted
     * (omitempty parity), key order mirroring the Go struct.
     *
     * @return array<string, mixed>
     */
    public function jsonSerialize(): array
    {
        $wire = ['code' => $this->code, 'message' => $this->message];
        if ($this->cause !== '') {
            $wire['cause'] = $this->cause;
        }
        if ($this->suggestedFix !== '') {
            $wire['suggested_fix'] = $this->suggestedFix;
        }
        if ($this->alternatives !== []) {
            $wire['alternatives'] = $this->alternatives;
        }
        $wire['exit_code'] = $this->exitCode;
        if ($this->transience !== '') {
            $wire['transience'] = $this->transience;
        }
        return $wire;
    }

    public function __toString(): string
    {
        if ($this->code === '') {
            return $this->message;
        }
        return "{$this->code}: {$this->message}";
    }

    /**
     * Writes the envelope to $writer (stream resource) in the requested
     * format. '' or 'table' renders human-readable plain text
     * ("Code: Message\nFix: ..."); 'json'/'yaml' render structurally.
     * An unset transience is normalized to TRANSIENCE_UNKNOWN on the
     * wire (Factor 4). Always returns; the caller decides the exit code
     * from $exitCode after rendering.
     *
     * @param resource $writer
     */
    public function renderTo(mixed $writer, string $format): void
    {
        $err = $this->transience === '' ? $this->withTransience(self::TRANSIENCE_UNKNOWN) : $this;

        $out = match ($format) {
            'json' => self::encodeJson($err->jsonSerialize()) . "\n",
            'yaml' => Yaml::dump($err->jsonSerialize()),
            default => $err->renderPlain(),
        };
        if (fwrite($writer, $out) === false) {
            throw new RuntimeException('cli error: write failed');
        }
    }

    /**
     * Human-readable form used by --format table (and the default empty
     * format). Each populated field appears on its own line so the
     * output is grep-friendly.
     */
    private function renderPlain(): string
    {
        $out = $this->code === ''
            ? "{$this->message}\n"
            : "{$this->code}: {$this->message}\n";
        if ($this->cause !== '') {
            $out .= "Cause: {$this->cause}\n";
        }
        if ($this->suggestedFix !== '') {
            $out .= "Fix: {$this->suggestedFix}\n";
        }
        foreach ($this->alternatives as $alt) {
            $out .= "Alternative: {$alt}\n";
        }
        return $out;
    }

    /**
     * Pretty JSON with 2-space indent (Go encoder parity).
     * JSON_PRETTY_PRINT hard-codes 4-space indent; rewrite to 2 — same
     * technique as JsonFormatter.
     *
     * @param array<string, mixed> $wire
     */
    private static function encodeJson(array $wire): string
    {
        $json = json_encode(
            $wire,
            JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR | JSON_PRETTY_PRINT,
        );
        return preg_replace_callback(
            '/^( {4})+/m',
            static fn (array $m) => str_repeat('  ', (int) (strlen($m[0]) / 4)),
            $json,
        ) ?? $json;
    }
}
