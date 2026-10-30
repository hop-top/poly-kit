<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\AuditRecord;
use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\ConfirmationBinding;
use HopTop\Kit\Mcp\ConfirmationState;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\Protocol;
use HopTop\Kit\Mcp\Response;
use HopTop\Kit\Mcp\StdioServer;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * The mount-level audit hook: one record per refusal the surface makes
 * before adopter code runs, mirroring the Go bridge's sinks.
 *
 * Every case asserts two things: the record carries the Go sentinel and
 * provenance for the event, and the wire response is byte-identical to
 * the same call on a mount without the hook — auditing is observation,
 * never a second gate.
 */
final class AuditTest extends TestCase
{
    private const KEY = 'test-confirmation-key';

    private const MODERN_META = '"_meta":{"io.modelcontextprotocol/clientCapabilities":{},'
        .'"io.modelcontextprotocol/protocolVersion":"2026-07-28"}';

    private const CAPABLE_META = '"_meta":{"io.modelcontextprotocol/clientCapabilities":'
        .'{"elicitation":{"form":{}}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}';

    /** @var list<AuditRecord> */
    private array $records = [];

    #[Test]
    public function legacyAuthRefusedIsAudited(): void
    {
        $response = $this->legacy('{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}');

        self::assertSame(401, $response->status);
        $this->assertOneRecord(['secret'], '2024-11-05', AuditRecord::ERR_AUTH_REFUSED);
    }

    #[Test]
    public function modernAuthRefusedIsAudited(): void
    {
        $response = $this->modern(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret",'.self::MODERN_META.'}}',
            'secret',
        );

        self::assertSame(401, $response->status);
        $this->assertOneRecord(['secret'], '2026-07-28', AuditRecord::ERR_AUTH_REFUSED);
    }

    #[Test]
    public function legacyConfirmationRequiredIsAudited(): void
    {
        $response = $this->legacy('{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy"}}');

        self::assertSame(428, $response->status);
        $this->assertOneRecord(['deploy'], '2024-11-05', AuditRecord::ERR_CONFIRMATION_REQUIRED);
    }

    #[Test]
    public function modernHeaderGateConfirmationRequiredIsAudited(): void
    {
        $response = $this->modern(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy",'.self::MODERN_META.'}}',
            'deploy',
        );

        self::assertSame(428, $response->status);
        $this->assertOneRecord(['deploy'], '2026-07-28', AuditRecord::ERR_CONFIRMATION_REQUIRED);
    }

    #[Test]
    public function modernNoElicitationFallbackConfirmationRequiredIsAudited(): void
    {
        // A confirmation key is configured, but this client cannot answer
        // a form: the header gate applies, and the refusal is audited.
        $response = $this->modern(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy",'.self::MODERN_META.'}}',
            'deploy',
            ['confirmationKey' => self::KEY],
        );

        self::assertSame(428, $response->status);
        $this->assertOneRecord(['deploy'], '2026-07-28', AuditRecord::ERR_CONFIRMATION_REQUIRED);
    }

    #[Test]
    public function modernConfirmationDeclinedIsAudited(): void
    {
        foreach (['decline', 'cancel'] as $action) {
            $this->records = [];
            $response = $this->modern(
                '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy",'
                .'"requestState":'.json_encode($this->freshState()).','
                .'"inputResponses":{"confirm":{"action":"'.$action.'"}},'.self::CAPABLE_META.'}}',
                'deploy',
                ['confirmationKey' => self::KEY],
            );

            self::assertStringContainsString('confirmation declined', $response->body, $action);
            $this->assertOneRecord(['deploy'], '2026-07-28', AuditRecord::ERR_CONFIRMATION_DECLINED);
        }
    }

    #[Test]
    public function modernTamperedStateIsAuditedAsRejectedAndReprompted(): void
    {
        [, , $tag] = explode('.', $this->freshState());
        $forged = 'v1.'.(time() - 1).'.'.$tag;

        $response = $this->modern(
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy",'
            .'"requestState":'.json_encode($forged).','
            .'"inputResponses":{"confirm":{"action":"accept"}},'.self::CAPABLE_META.'}}',
            'deploy',
            ['confirmationKey' => self::KEY],
        );

        self::assertStringContainsString(Protocol::RESULT_TYPE_INPUT_REQUIRED, $response->body);
        $this->assertOneRecord(
            ['deploy'],
            '2026-07-28',
            AuditRecord::ERR_CONFIRMATION_STATE_REJECTED,
            ['mcp_confirm_rejection' => 'request_state_verification_failed'],
        );
    }

    #[Test]
    public function modernExpiredButAuthenticStateIsRepromptedWithoutARecord(): void
    {
        $expired = (new ConfirmationState(self::KEY))->mint(
            new ConfirmationBinding('deploy', hash('sha256', 'null'), ''),
            time() - 1,
        );

        $response = $this->modern(
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy",'
            .'"requestState":'.json_encode($expired).','
            .'"inputResponses":{"confirm":{"action":"accept"}},'.self::CAPABLE_META.'}}',
            'deploy',
            ['confirmationKey' => self::KEY],
        );

        self::assertStringContainsString(Protocol::RESULT_TYPE_INPUT_REQUIRED, $response->body);
        self::assertSame([], $this->records, 'an expired state is re-prompted silently');
    }

    #[Test]
    public function legacyDestructiveBlockedIsAudited(): void
    {
        $response = $this->legacy(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.delete"}}',
        );

        self::assertStringContainsString('destructive command blocked', $response->body);
        $this->assertOneRecord(['widget', 'delete'], '2024-11-05', AuditRecord::ERR_DESTRUCTIVE_BLOCKED);
    }

    #[Test]
    public function modernDestructiveBlockedIsAudited(): void
    {
        $response = $this->modern(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.delete",'.self::MODERN_META.'}}',
            'widget.delete',
        );

        self::assertStringContainsString('destructive command blocked', $response->body);
        $this->assertOneRecord(['widget', 'delete'], '2026-07-28', AuditRecord::ERR_DESTRUCTIVE_BLOCKED);
    }

    #[Test]
    public function aSuccessfulCallIsNotAudited(): void
    {
        $this->legacy('{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}');
        $this->modern(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping",'.self::MODERN_META.'}}',
            'ping',
        );

        self::assertSame([], $this->records);
    }

    #[Test]
    public function aThrowingHookLeavesTheResponseUnchanged(): void
    {
        $hook = static fn (AuditRecord $r) => throw new \RuntimeException('sink down');

        $body = '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}';
        $bare = $this->dispatch(new Mount(), $body, []);
        $audited = $this->dispatch(new Mount(audit: $hook), $body, []);

        self::assertEquals($bare, $audited);
        self::assertSame(401, $audited->status);
    }

    #[Test]
    public function stateRejectedIsAuditedOverStdio(): void
    {
        [, , $tag] = explode('.', $this->freshState());
        $forged = 'v1.'.(time() - 1).'.'.$tag;

        $input = fopen('php://memory', 'w+');
        $output = fopen('php://memory', 'w+');
        $log = fopen('php://memory', 'w+');
        self::assertIsResource($input);
        self::assertIsResource($output);
        self::assertIsResource($log);
        fwrite($input, '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy",'
            .'"requestState":'.json_encode($forged).','
            .'"inputResponses":{"confirm":{"action":"accept"}},'.self::CAPABLE_META."}}\n");
        rewind($input);

        StdioServer::serve(
            new Bridge(LockTrees::legacy(), Policy::default()),
            new Mount(confirmationKey: self::KEY, audit: $this->hook()),
            $input,
            $output,
            $log,
        );

        rewind($output);
        self::assertStringContainsString(Protocol::RESULT_TYPE_INPUT_REQUIRED, (string) stream_get_contents($output));
        $this->assertOneRecord(
            ['deploy'],
            '2026-07-28',
            AuditRecord::ERR_CONFIRMATION_STATE_REJECTED,
            ['mcp_confirm_rejection' => 'request_state_verification_failed'],
        );
    }

    /**
     * Asserts exactly one record was captured, with the given provenance.
     *
     * @param list<string>          $path
     * @param array<string, string> $extra keys beyond the always-present spec version
     */
    private function assertOneRecord(array $path, string $specVersion, string $error, array $extra = []): void
    {
        self::assertCount(1, $this->records);
        $record = $this->records[0];

        self::assertSame($path, $record->path);
        self::assertSame('mcp', $record->surface);
        self::assertSame($specVersion, $record->specVersion);
        self::assertSame($error, $record->error);
        self::assertSame(['mcp_spec_version' => $specVersion, ...$extra], $record->extra);
        self::assertSame('UTC', $record->requestedAt->getTimezone()->getName());
        self::assertEqualsWithDelta(time(), $record->requestedAt->getTimestamp(), 5);
    }

    /**
     * Dispatches a legacy-era call on an audited mount and asserts the
     * response equals the unaudited one.
     */
    private function legacy(string $body): Response
    {
        return $this->audited([], $body, []);
    }

    /**
     * Dispatches a modern-era call on an audited mount and asserts the
     * response equals the unaudited one.
     *
     * @param array<string, mixed> $options extra named Mount arguments
     */
    private function modern(string $body, string $tool, array $options = []): Response
    {
        return $this->audited($options, $body, [
            Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'],
            Protocol::HEADER_METHOD => ['tools/call'],
            Protocol::HEADER_NAME => [$tool],
        ]);
    }

    /**
     * @param array<string, mixed>        $options
     * @param array<string, list<string>> $headers
     */
    private function audited(array $options, string $body, array $headers): Response
    {
        $bare = $this->dispatch(new Mount(...$options), $body, $headers);
        $audited = $this->dispatch(new Mount(...$options, audit: $this->hook()), $body, $headers);

        self::assertEquals($bare, $audited, 'the hook must not change the wire response');

        return $audited;
    }

    /** @param array<string, list<string>> $headers */
    private function dispatch(Mount $mount, string $body, array $headers): Response
    {
        return $mount
            ->dispatcher(new Bridge(LockTrees::legacy(), Policy::default()))
            ->dispatch($body, $headers);
    }

    private function hook(): \Closure
    {
        return function (AuditRecord $record): void {
            $this->records[] = $record;
        };
    }

    /** A state the mount would mint for `deploy` with no arguments. */
    private function freshState(): string
    {
        return (new ConfirmationState(self::KEY))->mint(
            new ConfirmationBinding('deploy', hash('sha256', 'null'), ''),
            time() + 300,
        );
    }
}
