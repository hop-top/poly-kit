<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Leaf;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\Protocol;
use HopTop\Kit\Mcp\Response;
use HopTop\Kit\Mcp\Result;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * The `toolNameSeparator` mount option.
 *
 * Tool names join the leaf path with the separator in `tools/list`,
 * resolve back through it on `tools/call` in both eras, and a separator a
 * command name contains fails the mount.
 */
final class ToolNameTest extends TestCase
{
    private const MODERN_META = '"_meta":{"io.modelcontextprotocol/clientCapabilities":{},'
        .'"io.modelcontextprotocol/protocolVersion":"2026-07-28"}';

    private const CAPABLE_META = '"_meta":{"io.modelcontextprotocol/clientCapabilities":'
        .'{"elicitation":{"form":{}}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}';

    #[Test]
    public function toolNameAndPathFromToolNameDefaultToADot(): void
    {
        self::assertSame('widget.add', (new Leaf(['widget', 'add']))->toolName());
        self::assertSame(['widget', 'add'], Leaf::pathFromToolName('widget.add'));
    }

    /** @return iterable<string, array{string}> */
    public static function separators(): iterable
    {
        yield 'underscore' => ['_'];
        yield 'dash' => ['-'];
        yield 'double underscore' => ['__'];
    }

    #[Test]
    #[DataProvider('separators')]
    public function toolNameAndPathFromToolNameAreSymmetric(string $separator): void
    {
        $name = (new Leaf(['widget', 'add']))->toolName($separator);

        self::assertSame('widget'.$separator.'add', $name);
        self::assertSame(['widget', 'add'], Leaf::pathFromToolName($name, $separator));
        self::assertSame([], Leaf::pathFromToolName('', $separator));
    }

    #[Test]
    public function theDefaultListsDottedNames(): void
    {
        self::assertContains('widget.add', $this->listed(new Mount()));
    }

    #[Test]
    public function theModernEraListsRenamedTools(): void
    {
        $response = $this->modern(new Mount(toolNameSeparator: '_'), 'tools/list', null, '{}');
        $names = array_column($this->resultOf($response)['tools'], 'name');

        self::assertContains('widget_add', $names);
        self::assertNotContains('widget.add', $names);
    }

    /** @return iterable<string, array{string}> */
    public static function renamingSeparators(): iterable
    {
        yield 'underscore' => ['_'];
        yield 'dash' => ['-'];
    }

    #[Test]
    #[DataProvider('renamingSeparators')]
    public function aSeparatorRenamesListedTools(string $separator): void
    {
        $names = $this->listed(new Mount(toolNameSeparator: $separator));

        self::assertContains('widget'.$separator.'add', $names);
        self::assertContains('ping', $names);
        self::assertNotContains('widget.add', $names);
    }

    #[Test]
    #[DataProvider('renamingSeparators')]
    public function aSeparatorResolvesALegacyCall(string $separator): void
    {
        $mount = new Mount(toolNameSeparator: $separator);

        $result = $this->resultOf($this->legacyCall($mount, 'widget'.$separator.'add'));
        self::assertFalse($result['isError']);
        self::assertSame("added\n", $result['content'][0]['text']);

        $body = $this->decode($this->legacyCall($mount, 'widget.add'));
        self::assertSame('unknown tool: widget.add', $body['error']['message']);
    }

    #[Test]
    #[DataProvider('renamingSeparators')]
    public function aSeparatorResolvesAModernCall(string $separator): void
    {
        $name = 'widget'.$separator.'add';
        $response = $this->modern(
            new Mount(toolNameSeparator: $separator),
            'tools/call',
            $name,
            '{"name":"'.$name.'","arguments":{"name":"bolt"},'.self::MODERN_META.'}',
        );

        $body = $this->decode($response);
        self::assertArrayNotHasKey('error', $body);
        self::assertFalse($body['result']['isError']);
        self::assertSame("added\n", $body['result']['content'][0]['text']);
    }

    #[Test]
    public function aSeparatorNamesTheToolInTheConfirmationPrompt(): void
    {
        $response = $this->modern(
            new Mount(confirmationKey: 'k', toolNameSeparator: '_'),
            'tools/call',
            'ops_deploy',
            '{"name":"ops_deploy",'.self::CAPABLE_META.'}',
        );

        self::assertSame(
            'Approve execution of "ops_deploy"?',
            $this->resultOf($response)['inputRequests']['confirm']['params']['message'],
        );
    }

    #[Test]
    public function anEmptySeparatorIsRefused(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        $this->expectExceptionMessage('cmdsurface: WithToolNameSeparator: empty separator');

        new Mount(toolNameSeparator: '');
    }

    #[Test]
    public function aSeparatorInsideACommandNameIsRefused(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        $this->expectExceptionMessage(
            'cmdsurface: WithToolNameSeparator: command "data_sync" in "data_sync" contains separator "_"',
        );

        (new Mount(toolNameSeparator: '_'))->dispatcher(new Bridge(self::collidingTree()));
    }

    #[Test]
    public function aSeparatorNoCommandNameContainsIsAccepted(): void
    {
        (new Mount(toolNameSeparator: '-'))->dispatcher(new Bridge(self::collidingTree()));

        $this->addToAssertionCount(1);
    }

    #[Test]
    public function theDefaultSeparatorIsNotCheckedAgainstCommandNames(): void
    {
        $tree = (new Command(name: 'root'))->addCommand(
            new Command(name: 'v1.2', runner: static fn (): Result => new Result()),
        );

        (new Mount())->dispatcher(new Bridge($tree));

        $this->addToAssertionCount(1);
    }

    private static function tree(): Command
    {
        $add = new Command(
            name: 'add',
            description: 'Add a widget',
            annotations: ['kit/side-effect' => 'write'],
            runner: static fn (): Result => new Result(stdout: "added\n"),
        );

        $deploy = new Command(
            name: 'deploy',
            description: 'Deploy',
            annotations: ['kit/requires-confirmation' => 'true'],
            runner: static fn (): Result => new Result(),
        );

        $ping = new Command(
            name: 'ping',
            description: 'Ping the server',
            annotations: ['kit/side-effect' => 'read'],
            runner: static fn (): Result => new Result(stdout: "pong\n"),
        );

        return (new Command(name: 'root'))->addCommand(
            (new Command(name: 'widget'))->addCommand($add),
            (new Command(name: 'ops'))->addCommand($deploy),
            $ping,
        );
    }

    /** `data_sync` and `data sync` read alike under `_`. */
    private static function collidingTree(): Command
    {
        $run = static fn (): Result => new Result();

        return (new Command(name: 'root'))->addCommand(
            new Command(name: 'data_sync', runner: $run),
            (new Command(name: 'data'))->addCommand(new Command(name: 'sync', runner: $run)),
        );
    }

    private function dispatch(Mount $mount, string $body, array $headers = []): Response
    {
        return $mount->dispatcher(new Bridge(self::tree(), Policy::default()))->dispatch($body, $headers);
    }

    /** @return list<string> */
    private function listed(Mount $mount): array
    {
        $response = $this->dispatch($mount, '{"jsonrpc":"2.0","id":1,"method":"tools/list"}');

        return array_column($this->resultOf($response)['tools'], 'name');
    }

    private function legacyCall(Mount $mount, string $name): Response
    {
        return $this->dispatch(
            $mount,
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":'
            .'{"name":"'.$name.'","arguments":{"name":"bolt"}}}',
        );
    }

    private function modern(Mount $mount, string $method, ?string $name, string $params): Response
    {
        if ('{}' === $params) {
            $params = '{'.self::MODERN_META.'}';
        }

        $headers = [
            Protocol::HEADER_PROTOCOL_VERSION => ['2026-07-28'],
            Protocol::HEADER_METHOD => [$method],
        ];

        if (null !== $name) {
            $headers[Protocol::HEADER_NAME] = [$name];
        }

        return $this->dispatch(
            $mount,
            '{"jsonrpc":"2.0","id":3,"method":"'.$method.'","params":'.$params.'}',
            $headers,
        );
    }

    /** @return array<string, mixed> */
    private function decode(Response $response): array
    {
        /** @var array<string, mixed> $decoded */
        $decoded = json_decode($response->body, true, 512, \JSON_THROW_ON_ERROR);

        return $decoded;
    }

    /** @return array<string, mixed> */
    private function resultOf(Response $response): array
    {
        $result = $this->decode($response)['result'];
        self::assertIsArray($result);

        return $result;
    }
}
