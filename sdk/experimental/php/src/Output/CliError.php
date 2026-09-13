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
    public const string CODE_CONSENT_REFUSED = 'CONSENT_REFUSED'; // exit 7 — confirmation declined
    public const string CODE_PROVENANCE_MISSING = 'PROVENANCE_MISSING'; // exit 65 — Factor-12 refusal
    public const string CODE_RATE_LIMITED = 'RATE_LIMITED'; // exit 64 — Factor-10 budget exceeded
    public const string CODE_PREREQUISITE = 'PREREQUISITE'; // exit 70 — dependency unreachable

    /**
     * Spec-assigned exit code for the generic failure class: the
     * command failed and no narrower code applies. Pair it with
     * generic() rather than hand-rolling exit 1, so the envelope
     * carries a transience class.
     */
    public const int EXIT_GENERIC = 1;
    /**
     * Success. Present for completeness so a table of the full
     * taxonomy can be written without a bare 0.
     */
    public const int EXIT_OK = 0;
    /**
     * The caller's invocation being wrong: an unknown flag, a missing
     * argument, a value the command cannot parse. Permanent by
     * construction — the same argv fails identically.
     */
    public const int EXIT_USAGE = 2;
    /** A named resource the command could not locate. */
    public const int EXIT_NOT_FOUND = 3;
    /**
     * A request that cannot be satisfied against the current state: a
     * precondition failed, a write raced, an identifier is already
     * taken.
     */
    public const int EXIT_CONFLICT = 4;
    /**
     * An authentication or authorization refusal. Permanent: the
     * caller needs new credentials or a different policy, not a retry.
     * Distinct from EXIT_CONSENT_REFUSED, which a re-invocation with
     * --confirm=yes clears.
     */
    public const int EXIT_UNAUTHORIZED = 5;
    /**
     * Spec-assigned exit code for transient/retryable failures
     * (Factor 11). Agents branch on it before parsing stderr: exit 6
     * means a retry may clear the failure.
     */
    public const int EXIT_TRANSIENT = 6;
    /**
     * Exit code for a confirmation gate that declined to run a
     * destructive operation (--confirm=no, the non-TTY default, a
     * missing or mismatched --confirm-token, or N at the prompt).
     * Classified transient: re-invoking with --confirm=yes clears it.
     * Distinct from CODE_UNAUTHORIZED at exit 5, which is permanent
     * because no confirmation can clear a policy denial.
     */
    public const int EXIT_CONSENT_REFUSED = 7;
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
     * Exit code for a declared external dependency kit could not
     * contact: nothing listening on the configured endpoint,
     * connection refused, dial timeout. Separates a correct invocation
     * whose logic never ran from the uncharacterized failures on exit
     * 1. Classified transient, but deliberately not CODE_TRANSIENT: a
     * dependency that is not running never comes up on its own, so the
     * caller repairs the environment instead of backing off.
     */
    public const int EXIT_PREREQUISITE = 70;

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
            self::CODE_TRANSIENT,
            self::CODE_CONSENT_REFUSED,
            self::CODE_PREREQUISITE => self::TRANSIENCE_TRANSIENT,
            default => self::TRANSIENCE_UNKNOWN,
        };
    }

    /**
     * The class-symbol-to-exit-code relation, as data. Mirrors Go's
     * exitCodeForClass in go/console/output/envelope/exitcodes.go.
     *
     * Expressed as a table rather than as constants plus trailing
     * comments because the string-to-number relationship is the thing
     * consumers actually need, and a comment cannot be consumed — nor
     * can it be checked against the cross-language contract. Pinned
     * against contracts/exit-taxonomy-v1/taxonomy.json by
     * tests/Output/TaxonomyContractTest.php.
     *
     * Scope is the classes kit itself owns. The conformance band's
     * tool-specific slots (66 LEAK_DETECTED, 67 CONFIG, 68 GRADE_FAIL,
     * 69 GRADE_UNGRADABLE) are deliberately absent: they are declared
     * by the packages that own them.
     *
     * @return array<string, int>
     */
    private static function exitCodeTable(): array
    {
        return [
            self::CODE_OK => self::EXIT_OK,
            self::CODE_GENERIC => self::EXIT_GENERIC,
            self::CODE_USAGE => self::EXIT_USAGE,
            self::CODE_NOT_FOUND => self::EXIT_NOT_FOUND,
            self::CODE_CONFLICT => self::EXIT_CONFLICT,
            self::CODE_UNAUTHORIZED => self::EXIT_UNAUTHORIZED,
            self::CODE_TRANSIENT => self::EXIT_TRANSIENT,
            self::CODE_CONSENT_REFUSED => self::EXIT_CONSENT_REFUSED,
            self::CODE_RATE_LIMITED => self::EXIT_RATE_LIMITED,
            self::CODE_PROVENANCE_MISSING => self::EXIT_PROVENANCE_MISSING,
            self::CODE_PREREQUISITE => self::EXIT_PREREQUISITE,
        ];
    }

    /**
     * Resolves a standard class symbol to its numeric exit code, or
     * null for adopter-defined and tool-specific codes.
     *
     * Callers that must produce a number for an unknown class decide
     * their own fallback. This method does not pick one: a built-in
     * fallback turns "this class was added to kit and never copied
     * here" into an assertion against exit 1 that looks like a real
     * failure rather than a stale table.
     */
    public static function exitCodeForClass(string $code): ?int
    {
        return self::exitCodeTable()[$code] ?? null;
    }

    /**
     * The class symbols kit defines, in ascending exit-code order
     * (ties broken by symbol). Callers rendering the taxonomy iterate
     * this rather than hard-coding rows, so a class added above
     * appears without a second edit.
     *
     * @return list<string>
     */
    public static function exitClasses(): array
    {
        $table = self::exitCodeTable();
        $classes = array_keys($table);
        usort(
            $classes,
            static fn (string $a, string $b): int => $table[$a] <=> $table[$b] ?: strcmp($a, $b),
        );

        return $classes;
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

    /**
     * CODE_GENERIC envelope with exit code 1. The catch-all for
     * failures no narrower code describes; permanent because retrying
     * the same input in the same environment is not expected to help.
     * Wrapping an arbitrary error as CODE_GENERIC via wrap() still
     * defaults to TRANSIENCE_UNKNOWN.
     */
    public static function generic(string $message): self
    {
        return new self(
            code: self::CODE_GENERIC,
            message: $message,
            exitCode: self::EXIT_GENERIC,
            transience: self::TRANSIENCE_PERMANENT,
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

    /**
     * CODE_CONSENT_REFUSED envelope with exit code 7. Use it when a
     * confirmation gate declines to run a destructive operation:
     * --confirm=no, the non-TTY default, a missing or mismatched
     * --confirm-token, or N at the prompt.
     *
     * Classified transient: the caller clears it by re-invoking with
     * --confirm=yes (or the matching token). Do not use it for policy
     * denials, which no confirmation can clear — those stay
     * unauthorized() and permanent.
     */
    public static function consentRefused(string $message): self
    {
        return new self(
            code: self::CODE_CONSENT_REFUSED,
            message: $message,
            exitCode: self::EXIT_CONSENT_REFUSED,
            transience: self::TRANSIENCE_TRANSIENT,
        );
    }

    /**
     * CODE_PREREQUISITE envelope with exit code 70. Use it when a
     * declared external dependency could not be contacted: nothing
     * listening on the configured endpoint, connection refused, dial
     * timeout.
     *
     * Classified transient: the operator starts the dependency and the
     * same command succeeds. Do not use it for a dependency that
     * answered and then misbehaved — that is generic() — nor for one
     * that was never configured, which is usage().
     */
    public static function prerequisite(string $message): self
    {
        return new self(
            code: self::CODE_PREREQUISITE,
            message: $message,
            exitCode: self::EXIT_PREREQUISITE,
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
