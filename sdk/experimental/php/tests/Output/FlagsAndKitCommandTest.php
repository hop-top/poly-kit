<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output;

use HopTop\Kit\Cli\KitCommand;
use HopTop\Kit\Output\Flags;
use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\KitOutput;
use HopTop\Kit\Output\Registry;
use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Tester\CommandTester;

/**
 * End-to-end: Flags::register + KitCommand + Application = data-only
 * `handle()` body that renders json/yaml via --format.
 */
class FlagsAndKitCommandTest extends TestCase
{
    public function testKitCommandRendersJsonViaFormatFlag(): void
    {
        $app = new Application('test', '0.0.0-test');
        $app->setAutoExit(false);

        $registry = new Registry();
        $registry->register(new JsonFormatter());
        Flags::register($app);
        Flags::setRegistry($app, $registry);

        $cmd = new class('demo') extends KitCommand {
            protected function handle(): int {
                $this->render(
                    [['name' => 'alpha', 'count' => 1]],
                    columns: [
                        ColumnSpec::of('name', 'name'),
                        ColumnSpec::of('count', 'count'),
                    ],
                );
                return self::SUCCESS;
            }
        };
        $app->add($cmd);

        $tester = new CommandTester($cmd);
        $tester->execute(['--format' => 'json']);

        $this->assertJsonStringEqualsJsonString(
            '[{"name":"alpha","count":1}]',
            $tester->getDisplay(),
        );
        $this->assertSame(0, $tester->getStatusCode());
    }

    public function testKitOutputFacadeFromExecute(): void
    {
        $app = new Application('test', '0.0.0-test');
        $app->setAutoExit(false);

        $registry = new Registry();
        $registry->register(new JsonFormatter());
        Flags::register($app);
        Flags::setRegistry($app, $registry);

        $cmd = new class('demo2') extends \Symfony\Component\Console\Command\Command {
            protected function execute(
                \Symfony\Component\Console\Input\InputInterface $input,
                \Symfony\Component\Console\Output\OutputInterface $output,
            ): int {
                KitOutput::for($input, $output)
                    ->application($this->getApplication())
                    ->render(['name' => 'alpha']);
                return self::SUCCESS;
            }
        };
        $app->add($cmd);

        $tester = new CommandTester($cmd);
        $tester->execute(['--format' => 'json']);

        $this->assertJsonStringEqualsJsonString('{"name":"alpha"}', $tester->getDisplay());
    }

    public function testFlagsRegisterIsIdempotent(): void
    {
        $app = new Application('test', '0.0.0-test');
        Flags::register($app);
        Flags::register($app);
        $this->assertTrue(true); // no exception = pass
    }
}
