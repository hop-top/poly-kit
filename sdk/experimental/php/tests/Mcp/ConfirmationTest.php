<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\ConfirmationBinding;
use HopTop\Kit\Mcp\ConfirmationState;
use HopTop\Kit\Mcp\ConfirmationStatus;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\Protocol;
use HopTop\Kit\Mcp\Response;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * The MRTR confirmation flow and its state token.
 *
 * The token is the whole security boundary here — it is what lets the
 * surface stay stateless — so the tests exercise tampering and replay,
 * not just the happy path.
 */
final class ConfirmationTest extends TestCase
{
    private const KEY = 'test-confirmation-key';

    private const CAPABLE_META = '"_meta":{"io.modelcontextprotocol/clientCapabilities":'
        .'{"elicitation":{"form":{}}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}';

    #[Test]
    public function aCapableClientIsPromptedWithAFormAndASignedState(): void
    {
        $response = $this->call('{"jsonrpc":"2.0","id":1,"method":"tools/call","params":'
            .'{"name":"deploy",'.self::CAPABLE_META.'}}');

        self::assertSame(200, $response->status);

        $result = $this->resultOf($response);

        self::assertSame(Protocol::RESULT_TYPE_INPUT_REQUIRED, $result['resultType']);
        self::assertSame('elicitation/create', $result['inputRequests']['confirm']['method']);
        self::assertSame('form', $result['inputRequests']['confirm']['params']['mode']);
        self::assertSame(
            'Approve execution of "deploy"?',
            $result['inputRequests']['confirm']['params']['message'],
        );
        self::assertStringStartsWith('v1.', $result['requestState']);
    }

    #[Test]
    public function anInterimResultCarriesNoCacheHints(): void
    {
        // tools/call is not a cacheable operation in either outcome.
        $result = $this->resultOf($this->call(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy",'.self::CAPABLE_META.'}}',
        ));

        self::assertArrayNotHasKey('ttlMs', $result);
        self::assertArrayNotHasKey('cacheScope', $result);
    }

    #[Test]
    public function acceptingTheFormRunsTheCommand(): void
    {
        $state = $this->resultOf($this->call(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy",'.self::CAPABLE_META.'}}',
        ))['requestState'];

        $response = $this->call(
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy",'
            .'"requestState":'.json_encode($state).','
            .'"inputResponses":{"confirm":{"action":"accept"}},'.self::CAPABLE_META.'}}',
        );

        $result = $this->resultOf($response);

        self::assertSame(200, $response->status);
        self::assertSame(Protocol::RESULT_TYPE_COMPLETE, $result['resultType']);
        self::assertFalse($result['isError']);
    }

    #[Test]
    public function decliningRefusesWithoutRunningTheCommand(): void
    {
        $state = $this->resultOf($this->call(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy",'.self::CAPABLE_META.'}}',
        ))['requestState'];

        foreach (['decline', 'cancel'] as $action) {
            $result = $this->resultOf($this->call(
                '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy",'
                .'"requestState":'.json_encode($state).','
                .'"inputResponses":{"confirm":{"action":"'.$action.'"}},'.self::CAPABLE_META.'}}',
            ));

            self::assertTrue($result['isError'], $action.' must refuse');
            self::assertSame('confirmation declined', $result['content'][0]['text']);
        }
    }

    #[Test]
    public function clientsWithoutFormElicitationKeepTheHeaderGate(): void
    {
        $response = $this->call(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy","_meta":'
            .'{"io.modelcontextprotocol/clientCapabilities":{},'
            .'"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}',
        );

        // Sending inputRequests to a client that cannot answer them would
        // deadlock the call, so the header gate applies instead.
        self::assertSame(428, $response->status);
        self::assertStringContainsString('confirmation required', $response->body);
    }

    #[Test]
    public function aStateMintedForAnotherToolIsRejected(): void
    {
        $state = new ConfirmationState(self::KEY);
        $binding = new ConfirmationBinding('widget delete', 'digest', '');
        $token = $state->mint($binding, time() + 300);

        $other = new ConfirmationBinding('deploy', 'digest', '');

        self::assertSame(ConfirmationStatus::Invalid, $state->verify($token, $other, time()));
    }

    #[Test]
    public function aStateMintedForAnotherCallerIsRejected(): void
    {
        $state = new ConfirmationState(self::KEY);
        $token = $state->mint(new ConfirmationBinding('deploy', 'digest', 'alice'), time() + 300);

        self::assertSame(
            ConfirmationStatus::Invalid,
            $state->verify($token, new ConfirmationBinding('deploy', 'digest', 'bob'), time()),
        );
    }

    #[Test]
    public function aTamperedExpiryCannotDowngradeAForgeryToMerelyExpired(): void
    {
        // Authenticity is checked before expiry precisely so this reads as
        // Invalid rather than Expired.
        $state = new ConfirmationState(self::KEY);
        $binding = new ConfirmationBinding('deploy', 'digest', '');
        $token = $state->mint($binding, time() + 300);

        [, , $tag] = explode('.', $token);
        $forged = 'v1.'.(time() - 1).'.'.$tag;

        self::assertSame(ConfirmationStatus::Invalid, $state->verify($forged, $binding, time()));
    }

    #[Test]
    public function anExpiredButAuthenticStateReportsExpired(): void
    {
        $state = new ConfirmationState(self::KEY);
        $binding = new ConfirmationBinding('deploy', 'digest', '');
        $token = $state->mint($binding, time() - 1);

        self::assertSame(ConfirmationStatus::Expired, $state->verify($token, $binding, time()));
    }

    #[Test]
    public function aStateSignedWithAnotherKeyIsRejected(): void
    {
        $binding = new ConfirmationBinding('deploy', 'digest', '');
        $token = (new ConfirmationState('other-key'))->mint($binding, time() + 300);

        self::assertSame(
            ConfirmationStatus::Invalid,
            (new ConfirmationState(self::KEY))->verify($token, $binding, time()),
        );
    }

    #[Test]
    public function malformedStatesAreRejectedRatherThanParsedLeniently(): void
    {
        $state = new ConfirmationState(self::KEY);
        $binding = new ConfirmationBinding('deploy', 'digest', '');

        foreach (['', 'v1', 'v1.abc.def', 'v2.1.aa', 'v1.1.!!!'] as $malformed) {
            self::assertSame(
                ConfirmationStatus::Invalid,
                $state->verify($malformed, $binding, time()),
                var_export($malformed, true).' must be rejected',
            );
        }
    }

    #[Test]
    public function bindingFieldsCannotBeShuffledAcrossTheirBoundaries(): void
    {
        // The MAC length-prefixes every field. Without that, adjacent
        // fields concatenate identically for different bindings — here a
        // tool named "deploy" with digest "x" would sign the same bytes as
        // a tool named "deploy" with digest "" and principal "x" — and a
        // token minted for one call would verify for another.
        $state = new ConfirmationState(self::KEY);

        $token = $state->mint(new ConfirmationBinding('deploy', 'ab', 'c'), time() + 300);

        self::assertSame(
            ConfirmationStatus::Invalid,
            $state->verify($token, new ConfirmationBinding('deploy', 'a', 'bc'), time()),
            'field boundaries must be signed, not just the concatenated bytes',
        );
    }

    #[Test]
    public function anEmptyConfirmationKeyIsRefusedAtConstruction(): void
    {
        $this->expectException(\InvalidArgumentException::class);

        new ConfirmationState('');
    }

    private function call(string $body): Response
    {
        $bridge = new Bridge(LockTrees::modern(), Policy::default());
        $mount = new Mount(confirmationKey: self::KEY);

        return $mount->dispatcher($bridge)->dispatch($body, [
            Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'],
            Protocol::HEADER_METHOD => ['tools/call'],
            Protocol::HEADER_NAME => ['deploy'],
        ]);
    }

    /** @return array<string, mixed> */
    private function resultOf(Response $response): array
    {
        /** @var array{result: array<string, mixed>} $decoded */
        $decoded = json_decode($response->body, true, 512, \JSON_THROW_ON_ERROR);

        return $decoded['result'];
    }
}
