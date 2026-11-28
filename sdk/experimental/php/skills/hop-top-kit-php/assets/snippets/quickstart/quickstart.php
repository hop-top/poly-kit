<?php

declare(strict_types=1);

use HopTop\Kit\Cli\KitCommand;
use HopTop\Kit\Output\Flags;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\ArrayInput;

#[AsCommand(name: 'items')]
final class ItemsCommand extends KitCommand
{
    protected function handle(): int
    {
        $this->render(
            [['name' => 'alpha', 'count' => 1], ['name' => 'beta', 'count' => 2]],
            columns: [ColumnSpec::of('name', 'name'), ColumnSpec::of('count', 'count')],
        );
        return self::SUCCESS;
    }
}

$app = new Application('tool', 'dev');
$app->setAutoExit(false);
Flags::register($app);      // --format, --format-opt, --cols, --template, -o
$app->addCommand(new ItemsCommand());

// Same data, two views: the formatter is chosen by the flag, not the command.
$app->run(new ArrayInput(['command' => 'items']));
$app->run(new ArrayInput(['command' => 'items', '--format' => 'json']));
