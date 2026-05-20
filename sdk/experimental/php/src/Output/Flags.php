<?php

declare(strict_types=1);

namespace HopTop\Kit\Output;

use Symfony\Component\Console\Application;
use Symfony\Component\Console\ConsoleEvents;
use Symfony\Component\Console\Event\ConsoleCommandEvent;
use Symfony\Component\Console\Input\InputDefinition;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\EventDispatcher\EventDispatcher;

/**
 * Registers the cross-language output flag suite on a Symfony Console
 * Application via a ConsoleEvents::COMMAND listener. Every Command picks
 * up --format, --format-opt, --format-help, --cols/--columns, --template,
 * and -o/--output without per-Command boilerplate.
 *
 * Mirrors py register_output_flags(app) (Typer) and ts registerOutputFlags
 * (Commander). The listener attaches once; calling register() twice is a
 * no-op (sentinel on the Application).
 *
 * Active registry is stashed on the Application so Dispatcher can look it
 * up without a global handoff. Pass a custom registry via setRegistry().
 */
final class Flags
{
    private const SENTINEL = '__hop_top_kit_output_flags_wired__';
    /** @var \WeakMap<Application,Registry>|null */
    private static ?\WeakMap $registries = null;
    /** @var \WeakMap<Application,EventDispatcher>|null */
    private static ?\WeakMap $dispatchers = null;

    public static function register(Application $app): void
    {
        if ($app->has(self::SENTINEL)) {
            return;
        }

        // Hidden no-op command serves as the idempotency sentinel — cheaper
        // than reflection and survives getApplication() round-trips.
        $app->add(self::buildSentinel());

        // Inject into the Application's default definition so every
        // Command gets the flags via mergeApplicationDefinition() at
        // resolve time. This also ensures CommandTester sees them, since
        // it walks the definition before ConsoleEvents::COMMAND fires.
        self::inject($app->getDefinition());

        // Existing commands already had their definitions merged at add()
        // time, so we also re-inject into each so the runtime definition
        // picks the new options up.
        foreach ($app->all() as $cmd) {
            self::inject($cmd->getDefinition());
        }

        // Wire a COMMAND-event listener too, for commands added *after*
        // register() runs. The injection is idempotent (hasOption guard).
        $dispatcher = new EventDispatcher();
        self::dispatchers()->offsetSet($app, $dispatcher);
        $app->setDispatcher($dispatcher);
        $dispatcher->addListener(
            ConsoleEvents::COMMAND,
            static function (ConsoleCommandEvent $event): void {
                $cmd = $event->getCommand();
                if ($cmd === null) {
                    return;
                }
                self::inject($cmd->getDefinition());
            },
        );
    }

    /**
     * Attach a non-default Registry to $app. Dispatcher reads this via
     * registryFor(). Without this call, Registry::default() is used.
     */
    public static function setRegistry(Application $app, Registry $registry): void
    {
        self::registries()->offsetSet($app, $registry);
    }

    /**
     * Resolve the Registry attached to $app, falling back to the process
     * default. Public because Dispatcher uses it.
     */
    public static function registryFor(?Application $app): Registry
    {
        if ($app !== null && self::registries()->offsetExists($app)) {
            return self::registries()->offsetGet($app);
        }
        return Registry::default();
    }

    private static function registries(): \WeakMap
    {
        if (self::$registries === null) {
            self::$registries = new \WeakMap();
        }
        return self::$registries;
    }

    private static function dispatchers(): \WeakMap
    {
        if (self::$dispatchers === null) {
            self::$dispatchers = new \WeakMap();
        }
        return self::$dispatchers;
    }

    private static function inject(InputDefinition $def): void
    {
        if (!$def->hasOption('format')) {
            $def->addOption(new InputOption(
                'format',
                null,
                InputOption::VALUE_REQUIRED,
                'Output format',
                'table',
            ));
        }
        if (!$def->hasOption('format-opt')) {
            $def->addOption(new InputOption(
                'format-opt',
                null,
                InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY,
                'Per-format option as key=value (repeatable; bool keys may omit =value)',
            ));
        }
        if (!$def->hasOption('format-help')) {
            $def->addOption(new InputOption(
                'format-help',
                null,
                InputOption::VALUE_OPTIONAL,
                'Show available formats and their options (use --format-help <key> for one)',
                false,
            ));
        }
        if (!$def->hasOption('cols')) {
            $def->addOption(new InputOption(
                'cols',
                null,
                InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY,
                'Restrict columns to this comma-separated list (repeatable)',
            ));
        }
        if (!$def->hasOption('columns')) {
            $def->addOption(new InputOption(
                'columns',
                null,
                InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY,
                'Alias for --cols',
            ));
        }
        if (!$def->hasOption('template')) {
            $def->addOption(new InputOption(
                'template',
                null,
                InputOption::VALUE_REQUIRED,
                'Template applied to results (mutually exclusive with --cols)',
            ));
        }
        if (!$def->hasOption('output')) {
            $def->addOption(new InputOption(
                'output',
                'o',
                InputOption::VALUE_REQUIRED,
                'Write output to path (use - or empty for stdout)',
                '',
            ));
        }
    }

    private static function buildSentinel(): \Symfony\Component\Console\Command\Command
    {
        $cmd = new \Symfony\Component\Console\Command\Command(self::SENTINEL);
        $cmd->setHidden(true);
        $cmd->setDescription('(internal kit sentinel — do not invoke)');
        return $cmd;
    }
}
