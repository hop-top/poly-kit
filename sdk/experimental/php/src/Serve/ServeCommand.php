<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use HopTop\Kit\Cli\KitCommand;
use HopTop\Kit\Output\CliError;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputOption;

/**
 * The kit-owned `serve` command.
 *
 * With no positional argument it is the supervisor over every
 * configured and enabled service; with exactly one it is the selector,
 * which overrides aggregate enablement. Two or more is a usage error
 * at exit 2 — the arity refusal is owned here rather than left to
 * Symfony, whose own too-many-arguments error exits 1.
 *
 * The inspection form is the `--list` flag, not a `list` child:
 * `list` is reserved selector vocabulary, so a `serve list` child
 * would be indistinguishable from the selector form naming a service
 * called `list`. Symfony makes that trap especially easy to fall into,
 * since it already ships a top-level `list` command.
 */
#[AsCommand(name: 'serve', description: 'Run configured services under one lifecycle')]
final class ServeCommand extends KitCommand
{
    /**
     * @param array<string, ServiceConfig> $configs Resolved
     *        `services.<name>` blocks, keyed by identifier.
     */
    public function __construct(
        private readonly ServiceRegistry $registry,
        private readonly array $configs = [],
        private readonly SupervisorConfig $supervisorConfig = new SupervisorConfig(),
        private readonly ?PolicyGate $policy = null,
        private readonly ?ServeLogger $logger = null,
    ) {
        parent::__construct();
    }

    /**
     * Builds a command from a resolved `services` block, reading the
     * contract's key names out of whatever the tool's config layer
     * produced.
     *
     * @param array<string, mixed> $services
     */
    public static function fromConfig(
        ServiceRegistry $registry,
        array $services,
        ?PolicyGate $policy = null,
        ?ServeLogger $logger = null,
    ): self {
        return new self(
            registry: $registry,
            configs: ServiceConfig::mapFromArray($services),
            supervisorConfig: SupervisorConfig::fromArray($services),
            policy: $policy,
            logger: $logger,
        );
    }

    protected function configure(): void
    {
        $this
            // IS_ARRAY rather than a single optional argument so the
            // arity refusal is this command's own, at exit 2, instead
            // of Symfony's generic one at exit 1.
            ->addArgument(
                'service',
                InputArgument::IS_ARRAY | InputArgument::OPTIONAL,
                'Service to run. Omit to supervise every configured and enabled service.',
            )
            ->addOption(
                'list',
                null,
                InputOption::VALUE_NONE,
                'List registered services and their state',
            )
            ->addOption(
                'enable',
                null,
                InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY,
                'Enable a service for this run (repeatable, supervisor form only)',
            )
            ->addOption(
                'disable',
                null,
                InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY,
                'Disable a service for this run (repeatable, supervisor form only)',
            )
            ->addOption(
                'ready-timeout',
                null,
                InputOption::VALUE_REQUIRED,
                'Per-service budget from start to ready (default 30s)',
            )
            ->addOption(
                'stop-timeout',
                null,
                InputOption::VALUE_REQUIRED,
                'Per-service budget for one stop (default 30s)',
            )
            ->addOption(
                'shutdown-timeout',
                null,
                InputOption::VALUE_REQUIRED,
                'Total shutdown budget across all services (default 60s)',
            );
    }

    protected function handle(): int
    {
        if ($this->input->getOption('list') === true) {
            return $this->renderList();
        }

        /** @var list<string> $args */
        $args = $this->input->getArgument('service');
        /** @var list<string> $enable */
        $enable = $this->input->getOption('enable');
        /** @var list<string> $disable */
        $disable = $this->input->getOption('disable');

        if (count($args) === 1 && ($enable !== [] || $disable !== [])) {
            // Under the selector form the override rule already decides
            // enablement; accepting the flags too would let one
            // invocation say two contradictory things.
            return $this->refuse(CliError::usage(
                '--enable/--disable apply to the supervisor form; drop the service name or drop the flags',
            ));
        }

        $durations = [];
        foreach (['ready-timeout', 'stop-timeout', 'shutdown-timeout'] as $flag) {
            $raw = $this->input->getOption($flag);
            if ($raw === null) {
                continue;
            }
            $seconds = Duration::parse((string) $raw);
            if ($seconds === null) {
                return $this->refuse(CliError::usage(
                    sprintf('--%s: invalid duration "%s"', $flag, (string) $raw),
                ));
            }
            $durations[$flag] = $seconds;
        }

        $configs = FlagOverrides::applyTimeouts(
            FlagOverrides::applyEnableDisable($this->configs, $enable, $disable),
            $durations['ready-timeout'] ?? null,
            $durations['stop-timeout'] ?? null,
        );
        $supervisorConfig = $this->supervisorConfig;
        if (($durations['shutdown-timeout'] ?? 0.0) > 0) {
            $supervisorConfig = new SupervisorConfig(
                failurePolicy: $supervisorConfig->failurePolicy,
                shutdownTimeout: $durations['shutdown-timeout'],
            );
        }

        $resolution = Resolver::resolve($this->registry, $args, $configs, $this->policy);
        if ($resolution->error !== null) {
            return $this->refuse($resolution->error);
        }

        $signals = Signals::install();
        try {
            $supervisor = new Supervisor(
                registry: $this->registry,
                config: $supervisorConfig,
                logger: $this->logger ?? new StderrLogger(),
                escalation: $signals->escalation(),
            );
            $result = $supervisor->run(
                $signals->drain(),
                $resolution->selected,
                $configs,
                $signals,
            );
        } finally {
            // Or the handlers outlive the run, and a second serve in
            // one process would see the first run's disposition.
            $signals->stop();
        }

        if ($result->error !== null) {
            return $this->refuse($result->error);
        }
        return $result->exitCode();
    }

    /**
     * Prints the registered services with their configured, enabled
     * and ready state, in registration order so the listing mirrors
     * the adopter's wiring.
     *
     * The columns are not contract — a port renders them through its
     * own output layer, which here is the kit formatter stack — but
     * the ordering is.
     */
    private function renderList(): int
    {
        $rows = [];
        foreach ($this->registry->list() as $svc) {
            $cfg = $this->configs[$svc->name()] ?? null;
            $rows[] = [
                'service' => $svc->name(),
                'configured' => $cfg !== null,
                'enabled' => $cfg !== null && $cfg->enabled,
                'ready' => $svc->ready(),
            ];
        }

        $this->render($rows, columns: [
            ColumnSpec::of('service', 'service'),
            ColumnSpec::of('configured', 'configured'),
            ColumnSpec::of('enabled', 'enabled'),
            ColumnSpec::of('ready', 'ready'),
        ]);
        return self::SUCCESS;
    }

    /** Renders a refusal through the shared envelope and returns its exit code. */
    private function refuse(CliError $error): int
    {
        $format = $this->input->hasOption('format')
            ? (string) ($this->input->getOption('format') ?? '')
            : '';

        $stderr = fopen('php://stderr', 'w');
        if ($stderr !== false) {
            $error->renderTo($stderr, $format);
        }
        return $error->exitCode;
    }
}
