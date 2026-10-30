<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\RequestHandler;
use HopTop\Kit\Mcp\Result;
use HopTop\Kit\Mcp\SpecVersion;
use Nyholm\Psr7\Factory\Psr17Factory;
use Nyholm\Psr7\ServerRequest;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * Legacy-era notifications and ping.
 *
 * A message with no `id` member is a JSON-RPC notification, which must
 * never be answered: 202, empty body, not processed. `ping` (a
 * 2024-11-05 utility) is answered with an empty result. Both hold on the
 * dual-era mount and on a legacy-only mount.
 */
final class LegacyNotifyTest extends TestCase
{
    /** @return iterable<string, array{Mount}> */
    public static function mounts(): iterable
    {
        yield 'dual-era' => [new Mount()];
        yield 'legacy-only' => [new Mount(specVersions: [SpecVersion::Legacy])];
    }

    #[Test]
    #[DataProvider('mounts')]
    public function aNotificationIsNeverAnsweredNorProcessed(Mount $mount): void
    {
        $count = new ExecutionCounter();
        $root = new Command(name: 'root', description: 'root');
        $root->addCommand(
            new Command(
                name: 'ping',
                description: 'Ping',
                annotations: ['kit/side-effect' => 'read'],
                runner: static function () use ($count): Result {
                    ++$count->n;

                    return new Result(stdout: "pong\n");
                },
            ),
        );
        $handler = $this->handler($root, $mount);

        foreach ([
            '{"jsonrpc":"2.0","method":"notifications/initialized"}',
            '{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}',
            '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping"}}',
        ] as $body) {
            $response = $handler->handle($this->post($body));
            self::assertSame(202, $response->getStatusCode(), $body);
            self::assertSame('', (string) $response->getBody(), $body);
        }
        self::assertSame(0, $count->n, 'a notification must not run the leaf');
    }

    #[Test]
    #[DataProvider('mounts')]
    public function pingAnswersAnEmptyResult(Mount $mount): void
    {
        $handler = $this->handler(LockTrees::legacy(), $mount);
        foreach ([
            '{"jsonrpc":"2.0","id":7,"method":"ping"}' => '{"jsonrpc":"2.0","id":7,"result":{}}'."\n",
            '{"jsonrpc":"2.0","id":null,"method":"ping"}' => '{"jsonrpc":"2.0","id":null,"result":{}}'."\n",
        ] as $body => $want) {
            $response = $handler->handle($this->post($body));
            self::assertSame(200, $response->getStatusCode(), $body);
            self::assertSame($want, (string) $response->getBody(), $body);
        }
    }

    private function post(string $body): ServerRequest
    {
        return (new ServerRequest('POST', '/mcp'))
            ->withBody((new Psr17Factory())->createStream($body));
    }

    private function handler(Command $root, Mount $mount): RequestHandler
    {
        $factory = new Psr17Factory();

        return new RequestHandler(new Bridge($root, Policy::default()), $mount, $factory, $factory);
    }
}
