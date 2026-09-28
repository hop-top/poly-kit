<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Identity;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\Request;
use HopTop\Kit\Mcp\Response;
use HopTop\Kit\Mcp\Result;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * The kit/auth-required gate on both eras: only the mount's verifier
 * establishes a caller. An Authorization header is presence, not
 * verification, and a caller or scopes the request claims never become
 * identity.
 */
final class IdentityTest extends TestCase
{
    /** @return iterable<string, array{string}> */
    public static function eras(): iterable
    {
        yield 'legacy' => ['legacy'];
        yield 'modern' => ['modern'];
    }

    #[Test]
    #[DataProvider('eras')]
    public function bareAuthorizationHeaderIsRefused(string $era): void
    {
        $runs = new ExecutionCounter();
        $response = $this->call($era, 'secret', ['Bearer good'], null, $runs);

        self::assertSame(401, $response->status);
        self::assertSame('Bearer', $response->headers['WWW-Authenticate'] ?? null);
        self::assertStringContainsString('"text":"authentication required"', $response->body);
        self::assertStringContainsString('"isError":true', $response->body);
        self::assertSame(0, $runs->n, 'the leaf must not run');
    }

    #[Test]
    #[DataProvider('eras')]
    public function verifierRefusalIsRefused(string $era): void
    {
        $runs = new ExecutionCounter();
        $response = $this->call($era, 'secret', ['Bearer bad'], self::verifyGood(), $runs);

        self::assertSame(401, $response->status);
        self::assertSame('Bearer', $response->headers['WWW-Authenticate'] ?? null);
        self::assertSame(0, $runs->n);
    }

    #[Test]
    #[DataProvider('eras')]
    public function throwingOrNonIdentityVerifierIsRefused(string $era): void
    {
        $runs = new ExecutionCounter();
        $throws = static function (Request $r): ?Identity {
            throw new \RuntimeException('verifier down');
        };
        self::assertSame(401, $this->call($era, 'secret', ['Bearer good'], $throws, $runs)->status);

        $truthy = static fn (Request $r): bool => true;
        self::assertSame(401, $this->call($era, 'secret', ['Bearer good'], $truthy, $runs)->status);
        self::assertSame(0, $runs->n);
    }

    #[Test]
    #[DataProvider('eras')]
    public function verifiedCallerIsAdmitted(string $era): void
    {
        $runs = new ExecutionCounter();
        $response = $this->call($era, 'secret', ['Bearer good'], self::verifyGood(), $runs);

        self::assertSame(200, $response->status);
        self::assertArrayNotHasKey('WWW-Authenticate', $response->headers);
        self::assertStringContainsString('"isError":false', $response->body);
        self::assertSame(1, $runs->n);
    }

    #[Test]
    #[DataProvider('eras')]
    public function openLeafRunsWhenVerifierRefuses(string $era): void
    {
        $runs = new ExecutionCounter();
        $response = $this->call($era, 'ping', ['Bearer bad'], self::verifyGood(), $runs);

        self::assertSame(200, $response->status);
        self::assertSame(1, $runs->n);
    }

    /** Accepts exactly `Bearer good` as alice. */
    private static function verifyGood(): \Closure
    {
        return static fn (Request $r): ?Identity => 'Bearer good' === $r->header('Authorization')
            ? new Identity(caller: 'alice', tenant: 'acme', scopes: ['read'])
            : null;
    }

    /**
     * A tools/call for $name on $era whose body claims an identity.
     *
     * @param list<string> $authorization
     */
    private function call(
        string $era,
        string $name,
        array $authorization,
        ?\Closure $verifier,
        ExecutionCounter $runs,
    ): Response {
        $count = static function () use ($runs): Result {
            ++$runs->n;

            return new Result(stdout: "ran\n");
        };
        $root = (new Command(name: 'root'))->addCommand(
            new Command(name: 'ping', description: 'Ping', runner: $count),
            new Command(
                name: 'secret',
                description: 'Locked',
                annotations: ['kit/auth-required' => 'true'],
                runner: $count,
            ),
        );
        $dispatcher = (new Mount(verifier: $verifier))->dispatcher(new Bridge($root, Policy::default()));

        $claims = '"caller":"admin","scopes":["admin"]';
        $headers = ['Authorization' => $authorization];
        if ('legacy' === $era) {
            $body = '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"'.$name.'","_meta":{'.$claims.'}}}';

            return $dispatcher->dispatch($body, $headers);
        }

        $body = '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"'.$name.'","_meta":{'.$claims
            .',"io.modelcontextprotocol/clientCapabilities":{},'
            .'"io.modelcontextprotocol/clientInfo":{"name":"admin","version":"1"},'
            .'"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}';
        $headers['MCP-Protocol-Version'] = ['2026-07-28'];
        $headers['Mcp-Method'] = ['tools/call'];
        $headers['Mcp-Name'] = [$name];

        return $dispatcher->dispatch($body, $headers);
    }
}
