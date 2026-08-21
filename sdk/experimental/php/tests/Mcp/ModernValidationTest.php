<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Dispatcher;
use HopTop\Kit\Mcp\ErrorCodes;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\Protocol;
use HopTop\Kit\Mcp\Response;
use HopTop\Kit\Mcp\Sentinel;
use HopTop\Kit\Mcp\SpecVersion;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * Modern-era validation steps the shared fixtures do not reach.
 *
 * The fixtures pin the cases every port must agree on; these cover the
 * rest of the V1..V9 chain, where a divergence would still be a real
 * interoperability bug.
 */
final class ModernValidationTest extends TestCase
{
    private const META = '"_meta":{"io.modelcontextprotocol/clientCapabilities":{},'
        .'"io.modelcontextprotocol/protocolVersion":"2026-07-28"}';

    #[Test]
    public function notificationsAreAcknowledgedWithoutProcessing(): void
    {
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","method":"tools/list","params":{'.self::META.'}}',
            [Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'], Protocol::HEADER_METHOD => ['tools/list']],
        );

        self::assertSame(202, $response->status);
        self::assertSame('', $response->body, 'a notification must not produce a body');
    }

    #[Test]
    public function nullIdIsMalformedInTheModernEra(): void
    {
        // Base JSON-RPC permits a null id; this revision forbids it. The
        // legacy era still round-trips null, so the two must disagree.
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":null,"method":"tools/list","params":{'.self::META.'}}',
            [Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'], Protocol::HEADER_METHOD => ['tools/list']],
        );

        self::assertSame(400, $response->status);
        self::assertStringContainsString('"code":'.ErrorCodes::INVALID_REQUEST, $response->body);
        self::assertStringContainsString('must be a string or integer, got null', $response->body);
    }

    #[Test]
    public function fractionalIdsAreRejectedButWholeNumbersPass(): void
    {
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":1.5,"method":"tools/list","params":{'.self::META.'}}',
            [Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'], Protocol::HEADER_METHOD => ['tools/list']],
        );

        self::assertSame(400, $response->status);
        self::assertStringContainsString('got 1.5', $response->body);
    }

    #[Test]
    public function conflictingDuplicateHeadersFailWithoutComparingEitherValue(): void
    {
        // Duplicated with differing values, this is the
        // multiple-sources-of-truth hazard the header checks exist to
        // close: a proxy trusting one occurrence while the server acts on
        // another.
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{'.self::META.'}}',
            [
                Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28', '2024-11-05'],
                Protocol::HEADER_METHOD => ['tools/list'],
            ],
        );

        self::assertSame(400, $response->status);
        self::assertStringContainsString('"code":'.ErrorCodes::HEADER_MISMATCH, $response->body);
        self::assertStringContainsString('conflicting duplicate values', $response->body);
    }

    #[Test]
    public function identicalDuplicateHeadersAreTolerated(): void
    {
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{'.self::META.'}}',
            [
                Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28', '2026-07-28'],
                Protocol::HEADER_METHOD => ['tools/list'],
            ],
        );

        self::assertSame(200, $response->status, 'benign proxy duplication must pass');
    }

    #[Test]
    public function unknownModernMethodIsNotFoundAtHttp404(): void
    {
        // The legacy era answers the same condition at HTTP 200; the
        // modern era must not.
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":1,"method":"nope","params":{'.self::META.'}}',
            [Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'], Protocol::HEADER_METHOD => ['nope']],
        );

        self::assertSame(404, $response->status);
        self::assertStringContainsString('"code":'.ErrorCodes::METHOD_NOT_FOUND, $response->body);
    }

    #[Test]
    public function missingMetaKeysAreReportedIndividually(): void
    {
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{}}}',
            [Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'], Protocol::HEADER_METHOD => ['tools/list']],
        );

        self::assertSame(400, $response->status);
        self::assertStringContainsString(
            'missing required _meta key: '.Protocol::META_PROTOCOL_VERSION,
            $response->body,
        );
    }

    #[Test]
    public function toolsCallRequiresTheNameHeaderBeforeAnyBodyCheck(): void
    {
        // V7 precedes the params decode: headers are the routing signal a
        // gateway trusts without reading the body.
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{'.self::META.'}}',
            [Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'], Protocol::HEADER_METHOD => ['tools/call']],
        );

        self::assertSame(400, $response->status);
        self::assertStringContainsString('missing '.Protocol::HEADER_NAME.' header', $response->body);
    }

    #[Test]
    public function base64SentinelNamesDecodeBeforeComparison(): void
    {
        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping",'.self::META.'}}',
            [
                Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'],
                Protocol::HEADER_METHOD => ['tools/call'],
                Protocol::HEADER_NAME => ['=?base64?'.base64_encode('ping').'?='],
            ],
        );

        self::assertSame(200, $response->status);
        self::assertStringContainsString('pong', $response->body);
    }

    #[Test]
    public function anEmptySentinelDecodesToEmptyAndIsRejected(): void
    {
        self::assertSame('', Sentinel::decode('=?base64??='));

        $response = $this->dispatch(
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping",'.self::META.'}}',
            [
                Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'],
                Protocol::HEADER_METHOD => ['tools/call'],
                Protocol::HEADER_NAME => ['=?base64??='],
            ],
        );

        self::assertSame(400, $response->status);
        self::assertStringContainsString('decodes to an empty value', $response->body);
    }

    #[Test]
    public function malformedSentinelPayloadsFailClosed(): void
    {
        // A value that looks like a sentinel is always treated as one —
        // there is no literal fallback to compare against.
        self::assertNull(Sentinel::decode('=?base64?not-base64!?='));
    }

    #[Test]
    public function modernOnlyMountsRejectTheLegacyHandshakeAndNameTheirVersions(): void
    {
        $bridge = new Bridge(LockTrees::modern(), Policy::default());
        $dispatcher = (new Mount(specVersions: [SpecVersion::Modern]))->dispatcher($bridge);

        $response = $dispatcher->dispatch('{"jsonrpc":"2.0","id":1,"method":"initialize"}', []);

        self::assertSame(400, $response->status);
        self::assertStringContainsString(
            'supported protocol versions: '.Protocol::MODERN_VERSION,
            $response->body,
            'a legacy client has no fall-forward mechanism, so errors must name the versions',
        );
    }

    #[Test]
    public function aLongLivedMountReportsCobrasLazyHelpFlagAfterALeafRuns(): void
    {
        // Cobra registers a leaf's implicit `--help` flag the first time it
        // executes, so a persistent mount reports one more property than it
        // did before. The wire fixtures build a fresh server per case and
        // never see this; the Go surface still does it, so a port that
        // skipped it would diverge in exactly the deployment shape adopters
        // actually run.
        $bridge = new Bridge(LockTrees::legacy(), Policy::default());
        $dispatcher = (new Mount())->dispatcher($bridge);

        $before = $dispatcher->dispatch('{"jsonrpc":"2.0","id":1,"method":"tools/list"}', []);
        self::assertStringNotContainsString('help for ping', $before->body);

        $dispatcher->dispatch(
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping"}}',
            [],
        );

        $after = $dispatcher->dispatch('{"jsonrpc":"2.0","id":3,"method":"tools/list"}', []);
        self::assertStringContainsString('help for ping', $after->body);
    }

    #[Test]
    public function aMountMustServeAtLeastOneSpecVersion(): void
    {
        $this->expectException(\InvalidArgumentException::class);

        new Mount(specVersions: []);
    }

    /** @param array<string, list<string>> $headers */
    private function dispatch(string $body, array $headers): Response
    {
        $bridge = new Bridge(LockTrees::modern(), Policy::default());

        return (new Mount())->dispatcher($bridge)->dispatch($body, $headers);
    }
}
