<?php

declare(strict_types=1);

namespace HopTop\Kit\Output;

use HopTop\Kit\Output\Formatter\ColumnSpec;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

/**
 * Fluent escape hatch for `Dispatcher::dispatch()` — use when you can't (or
 * don't want to) extend KitCommand. Mirrors the "option D" facade discussed
 * in the design notes.
 *
 *   KitOutput::for($input, $output)
 *       ->columns([ColumnSpec::of('name', 'name', priority: 9)])
 *       ->render($items);
 */
final class KitOutput
{
    /** @var list<ColumnSpec>|null */
    private ?array $columns = null;

    private ?Registry $registry = null;

    private ?Application $application = null;

    private function __construct(
        private readonly InputInterface $input,
        private readonly OutputInterface $output,
    ) {
    }

    public static function for(InputInterface $input, OutputInterface $output): self
    {
        return new self($input, $output);
    }

    /**
     * @param list<ColumnSpec> $columns
     */
    public function columns(array $columns): self
    {
        $this->columns = $columns;
        return $this;
    }

    public function registry(Registry $registry): self
    {
        $this->registry = $registry;
        return $this;
    }

    public function application(Application $application): self
    {
        $this->application = $application;
        return $this;
    }

    public function render(mixed $data): void
    {
        Dispatcher::dispatch(
            $this->input,
            $this->output,
            $data,
            $this->columns,
            $this->registry,
            $this->application,
        );
    }
}
