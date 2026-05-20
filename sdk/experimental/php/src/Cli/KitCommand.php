<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli;

use HopTop\Kit\Output\Dispatcher;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Registry;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

/**
 * Base class for kit-powered Symfony Console commands. Mirrors the
 * Laravel/Artisan `handle()` convention so PHP devs don't have to learn
 * a new shape — and gives PHP the same "data-only" feel that py/ts get
 * via implicit ctx injection.
 *
 *   class ListCommand extends KitCommand {
 *       protected static $defaultName = 'list';
 *       protected function handle(): int {
 *           $this->render([['name' => 'alpha']]);
 *           return self::SUCCESS;
 *       }
 *   }
 *
 * Users who already extend their own base class can use KitOutput::for()
 * instead.
 */
abstract class KitCommand extends Command
{
    protected InputInterface $input;

    protected OutputInterface $output;

    /**
     * Subclasses implement `handle()` instead of `execute()`. The default
     * `execute()` stashes $input/$output and dispatches; subclasses can
     * still override execute() if they need the raw symfony shape.
     */
    abstract protected function handle(): int;

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $this->input = $input;
        $this->output = $output;
        return $this->handle();
    }

    /**
     * Render $data through the active formatter, honoring all output flags
     * wired by Flags::register().
     *
     * @param list<ColumnSpec>|null $columns Schema for --cols validation
     */
    protected function render(
        mixed $data,
        ?array $columns = null,
        ?Registry $registry = null,
    ): void {
        Dispatcher::dispatch(
            $this->input,
            $this->output,
            $data,
            $columns,
            $registry,
            $this->getApplication(),
        );
    }
}
