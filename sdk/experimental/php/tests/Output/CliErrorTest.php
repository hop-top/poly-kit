<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output;

use HopTop\Kit\Output\CliError;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use Symfony\Component\Yaml\Yaml;

/**
 * Mirrors go/console/output/error_test.go assertions.
 */
class CliErrorTest extends TestCase
{
    private function render(CliError $err, string $format): string
    {
        $w = fopen('php://memory', 'w+b');
        $err->renderTo($w, $format);
        rewind($w);
        return stream_get_contents($w) ?: '';
    }

    /** @return array<string, array{CliError, string, int, string}> */
    public static function constructorCases(): array
    {
        return [
            'NotFound' => [CliError::notFound('nope'), CliError::CODE_NOT_FOUND, 3, CliError::TRANSIENCE_PERMANENT],
            'Conflict' => [CliError::conflict('dup'), CliError::CODE_CONFLICT, 4, CliError::TRANSIENCE_PERMANENT],
            'Unauthorized' => [CliError::unauthorized('nope'), CliError::CODE_UNAUTHORIZED, 5, CliError::TRANSIENCE_PERMANENT],
            'Usage' => [CliError::usage('bad flag'), CliError::CODE_USAGE, 2, CliError::TRANSIENCE_PERMANENT],
            'RateLimited' => [CliError::rateLimited('budget'), CliError::CODE_RATE_LIMITED, 64, CliError::TRANSIENCE_TRANSIENT],
            'Transient' => [CliError::transient('upstream timeout'), CliError::CODE_TRANSIENT, 6, CliError::TRANSIENCE_TRANSIENT],
            'ProvenanceMissing' => [CliError::provenanceMissing('/email'), CliError::CODE_PROVENANCE_MISSING, 65, CliError::TRANSIENCE_PERMANENT],
        ];
    }

    #[DataProvider('constructorCases')]
    public function testConstructorsSetCodeExitTransience(
        CliError $got,
        string $wantCode,
        int $wantExit,
        string $wantTransience,
    ): void {
        $this->assertSame($wantCode, $got->code);
        $this->assertSame($wantExit, $got->exitCode);
        $this->assertSame($wantTransience, $got->transience);
    }

    public function testExitCodeTableIsUnique(): void
    {
        $exits = [];
        foreach (self::constructorCases() as [$err]) {
            $exits[$err->exitCode] = $err->code;
        }
        $this->assertCount(7, $exits);
        $this->assertSame(6, CliError::EXIT_TRANSIENT);
        $this->assertSame(64, CliError::EXIT_RATE_LIMITED);
        $this->assertSame(65, CliError::EXIT_PROVENANCE_MISSING);
        $this->assertSame(CliError::CODE_TRANSIENT, $exits[6]);
        $this->assertSame(CliError::CODE_PROVENANCE_MISSING, $exits[65]);
    }

    /** @return array<string, array{string, string}> */
    public static function transienceCases(): array
    {
        return [
            'usage' => [CliError::CODE_USAGE, CliError::TRANSIENCE_PERMANENT],
            'not-found' => [CliError::CODE_NOT_FOUND, CliError::TRANSIENCE_PERMANENT],
            'conflict' => [CliError::CODE_CONFLICT, CliError::TRANSIENCE_PERMANENT],
            'unauthorized' => [CliError::CODE_UNAUTHORIZED, CliError::TRANSIENCE_PERMANENT],
            'provenance-missing' => [CliError::CODE_PROVENANCE_MISSING, CliError::TRANSIENCE_PERMANENT],
            'rate-limited' => [CliError::CODE_RATE_LIMITED, CliError::TRANSIENCE_TRANSIENT],
            'transient' => [CliError::CODE_TRANSIENT, CliError::TRANSIENCE_TRANSIENT],
            'generic' => [CliError::CODE_GENERIC, CliError::TRANSIENCE_UNKNOWN],
            'adopter' => ['ADOPTER_SPECIFIC', CliError::TRANSIENCE_UNKNOWN],
            'empty' => ['', CliError::TRANSIENCE_UNKNOWN],
        ];
    }

    #[DataProvider('transienceCases')]
    public function testTransienceForCode(string $code, string $want): void
    {
        $this->assertSame($want, CliError::transienceForCode($code));
    }

    public function testWrapDefaultsTransienceFromCodeAndRetainsSource(): void
    {
        $base = new RuntimeException('boom');
        $e = CliError::wrap($base, CliError::CODE_CONFLICT, 4);
        $this->assertSame(CliError::TRANSIENCE_PERMANENT, $e->transience);
        $this->assertSame('boom', $e->message);
        $this->assertSame($base, $e->source);

        $this->assertSame(
            CliError::TRANSIENCE_TRANSIENT,
            CliError::wrap($base, CliError::CODE_RATE_LIMITED, 64)->transience,
        );
        $this->assertSame(
            CliError::TRANSIENCE_UNKNOWN,
            CliError::wrap($base, CliError::CODE_GENERIC, 1)->transience,
        );
    }

    public function testWithTransienceCopiesAndSets(): void
    {
        $base = new RuntimeException('boom');
        $orig = CliError::wrap($base, 'SHARED', 9)->withTransience('');
        $got = $orig->withTransience(CliError::TRANSIENCE_TRANSIENT);
        $this->assertNotSame($orig, $got);
        $this->assertSame(CliError::TRANSIENCE_TRANSIENT, $got->transience);
        // Shared envelopes are never mutated in place.
        $this->assertSame('', $orig->transience);
        // Every other field carries over, including the retained source.
        $this->assertSame($orig->code, $got->code);
        $this->assertSame($orig->message, $got->message);
        $this->assertSame($orig->exitCode, $got->exitCode);
        $this->assertSame($base, $got->source);
    }

    public function testRenderStructuredAlwaysCarriesTransience(): void
    {
        $bare = new CliError(code: 'ADOPTER_SPECIFIC', message: 'm', exitCode: 9);

        $got = json_decode($this->render($bare, 'json'), true);
        $this->assertSame(CliError::TRANSIENCE_UNKNOWN, $got['transience']);

        $this->assertStringContainsString(
            'transience: unknown',
            $this->render($bare, 'yaml'),
        );

        // Input envelope is untouched (readonly makes mutation impossible).
        $this->assertSame('', $bare->transience);

        // An explicit class renders untouched.
        $got = json_decode($this->render(CliError::rateLimited('budget'), 'json'), true);
        $this->assertSame(CliError::TRANSIENCE_TRANSIENT, $got['transience']);
    }

    public function testRenderJsonWireRoundTrip(): void
    {
        $got = json_decode($this->render(CliError::provenanceMissing('/email'), 'json'), true);
        $this->assertSame(CliError::CODE_PROVENANCE_MISSING, $got['code']);
        $this->assertSame('/email', $got['cause']);
        $this->assertSame(65, $got['exit_code']);
        $this->assertSame(CliError::TRANSIENCE_PERMANENT, $got['transience']);

        // Empty optionals stay off the wire (omitempty parity), key
        // order mirrors the Go struct.
        $got = json_decode($this->render(CliError::transient('upstream timeout'), 'json'), true);
        $this->assertSame(
            ['code', 'message', 'exit_code', 'transience'],
            array_keys($got),
        );
        $this->assertSame(6, $got['exit_code']);
    }

    public function testRenderYamlWireRoundTrip(): void
    {
        $got = Yaml::parse($this->render(CliError::transient('upstream timeout'), 'yaml'));
        $this->assertSame(
            [
                'code' => CliError::CODE_TRANSIENT,
                'message' => 'upstream timeout',
                'exit_code' => 6,
                'transience' => CliError::TRANSIENCE_TRANSIENT,
            ],
            $got,
        );
    }

    public function testRenderPlainListsEachField(): void
    {
        $err = new CliError(
            code: 'NOT_FOUND',
            message: 'missing thing',
            cause: 'root',
            suggestedFix: 'try --all',
            alternatives: ['other'],
            exitCode: 3,
        );
        $this->assertSame(
            "NOT_FOUND: missing thing\nCause: root\nFix: try --all\nAlternative: other\n",
            $this->render($err, 'table'),
        );

        // Bare message renders without a code prefix.
        $bare = new CliError(message: 'just text', exitCode: 1);
        $this->assertSame("just text\n", $this->render($bare, ''));
    }

    public function testToStringMatchesGoErrorString(): void
    {
        $e = CliError::notFound('missing thing');
        $this->assertStringContainsString('NOT_FOUND', (string) $e);
        $this->assertStringContainsString('missing thing', (string) $e);
    }
}
