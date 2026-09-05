<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Output\Flags;
use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use HopTop\Kit\Output\Registry;
use HopTop\Kit\Serve\PolicyGate;
use HopTop\Kit\Serve\PolicyVerdict;
use HopTop\Kit\Serve\ServeCommand;
use HopTop\Kit\Serve\Service;
use HopTop\Kit\Serve\ServiceConfig;
use HopTop\Kit\Serve\ServiceRegistry;
use HopTop\Kit\Serve\SupervisorConfig;
use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Tester\CommandTester;

/**
 * The command surface: the hierarchy, --list as a flag, and every exit
 * code in the contract's table reached through a real invocation.
 */
final class ServeCommandTest extends TestCase
{
    private static function registryOf(Service ...$svcs): ServiceRegistry
    {
        $r = new ServiceRegistry();
        foreach ($svcs as $s) {
            $r->register($s);
        }
        return $r;
    }

    /**
     * @param array<string, ServiceConfig> $configs
     * @return array{0: CommandTester, 1: ServeCommand}
     */
    private static function tester(
        ServiceRegistry $registry,
        array $configs = [],
        ?PolicyGate $policy = null,
        ?SupervisorConfig $supervisorConfig = null,
    ): array {
        $app = new Application('tool', '0.0.0-test');
        $app->setAutoExit(false);

        $formatters = new Registry();
        $formatters->register(new JsonFormatter());
        Flags::register($app);
        Flags::setRegistry($app, $formatters);

        $cmd = new ServeCommand(
            registry: $registry,
            configs: $configs,
            supervisorConfig: $supervisorConfig ?? new SupervisorConfig(),
            policy: $policy,
            logger: new RecordingLogger(),
        );
        $app->add($cmd);

        return [new CommandTester($cmd), $cmd];
    }

    /** @param list<string> $names @return array<string, ServiceConfig> */
    private static function enabled(array $names): array
    {
        $out = [];
        foreach ($names as $n) {
            $out[$n] = new ServiceConfig(enabled: true);
        }
        return $out;
    }

    // -- shape -------------------------------------------------------

    public function testMountsServeWithAnOptionalServiceOperand(): void
    {
        [, $cmd] = self::tester(self::registryOf(new FakeService('api')));

        $this->assertSame('serve', $cmd->getName());
        $this->assertSame('Run configured services under one lifecycle', $cmd->getDescription());
        $this->assertTrue($cmd->getDefinition()->hasArgument('service'));
        $this->assertFalse($cmd->getDefinition()->getArgument('service')->isRequired());
    }

    public function testExposesListAsAFlagNotAChild(): void
    {
        // `list` is reserved selector vocabulary, so a `serve list`
        // child would be indistinguishable from the selector form
        // naming a service called `list`.
        [, $cmd] = self::tester(self::registryOf(new FakeService('api')));
        $this->assertTrue($cmd->getDefinition()->hasOption('list'));
    }

    public function testListPrintsEveryServiceInRegistrationOrder(): void
    {
        [$tester] = self::tester(
            self::registryOf(new FakeService('zeta'), new FakeService('alpha')),
            ['zeta' => new ServiceConfig(enabled: true)],
        );
        $tester->execute(['--list' => true, '--format' => 'json']);

        $out = $tester->getDisplay();
        /** @var list<array<string, mixed>> $rows */
        $rows = json_decode($out, true, 512, JSON_THROW_ON_ERROR);

        $this->assertSame('zeta', $rows[0]['service']);
        $this->assertSame('alpha', $rows[1]['service']);
        $this->assertTrue($rows[0]['configured']);
        $this->assertTrue($rows[0]['enabled']);
        // alpha has no config block: not configured, not enabled.
        $this->assertFalse($rows[1]['configured']);
        $this->assertFalse($rows[1]['enabled']);
        $this->assertSame(0, $tester->getStatusCode());
    }

    // -- the exit-code table, through a real invocation ---------------

    public function testExitsZeroForACleanStop(): void
    {
        [$tester] = self::tester(
            self::registryOf(new FakeService('api', finishAfterTicks: 2)),
            self::enabled(['api']),
        );
        $tester->execute(['service' => ['api']]);
        $this->assertSame(0, $tester->getStatusCode());
    }

    public function testExitsTwoOnTwoOrMoreServiceOperands(): void
    {
        [$tester] = self::tester(
            self::registryOf(new FakeService('api'), new FakeService('socket')),
            self::enabled(['api', 'socket']),
        );
        $tester->execute(['service' => ['api', 'socket']]);
        $this->assertSame(2, $tester->getStatusCode());
    }

    public function testExitsTwoWhenTheSupervisorFormResolvesToNothing(): void
    {
        [$tester] = self::tester(
            self::registryOf(new FakeService('api')),
            ['api' => new ServiceConfig(enabled: false)],
        );
        $tester->execute([]);
        $this->assertSame(2, $tester->getStatusCode());
    }

    public function testExitsTwoOnAConfigValidationFailure(): void
    {
        [$tester] = self::tester(
            self::registryOf(new FakeService('api', configError: 'addr: missing')),
            self::enabled(['api']),
        );
        $tester->execute(['service' => ['api']]);
        $this->assertSame(2, $tester->getStatusCode());
    }

    public function testExitsThreeOnAnUnknownService(): void
    {
        [$tester] = self::tester(self::registryOf(new FakeService('api')), self::enabled(['api']));
        $tester->execute(['service' => ['ghost']]);
        $this->assertSame(3, $tester->getStatusCode());
    }

    public function testExitsFiveWhenThePolicyGateDeniesTheNamedService(): void
    {
        $gate = new class implements PolicyGate {
            public function allow(string $sideEffect, string $network): PolicyVerdict
            {
                return PolicyVerdict::deny('blocked');
            }
        };
        [$tester] = self::tester(
            self::registryOf(new FakeService('api', class: ['destructive', 'ingress'])),
            self::enabled(['api']),
            $gate,
        );
        $tester->execute(['service' => ['api']]);
        $this->assertSame(5, $tester->getStatusCode());
    }

    public function testExitsOneWhenTheSelectedServiceFailsToStart(): void
    {
        [$tester] = self::tester(
            self::registryOf(FakeService::failing('api', 'bind refused')),
            self::enabled(['api']),
        );
        $tester->execute(['service' => ['api']]);
        $this->assertSame(1, $tester->getStatusCode());
    }

    public function testExitsOneWhenAServiceCrashesAtRuntime(): void
    {
        [$tester] = self::tester(
            self::registryOf(new FakeService(
                'api',
                failTickAfter: new \RuntimeException('crashed'),
                failTickAfterTicks: 1,
            )),
            self::enabled(['api']),
        );
        $tester->execute(['service' => ['api']]);
        $this->assertSame(1, $tester->getStatusCode());
    }

    public function testExitsOneWhenTheShutdownBudgetIsExceeded(): void
    {
        [$tester] = self::tester(
            self::registryOf(new FakeService('api', finishAfterTicks: 2, stopDelay: 0.05)),
            self::enabled(['api']),
            null,
            new SupervisorConfig(shutdownTimeout: 0.001),
        );
        $tester->execute(['service' => ['api']]);
        $this->assertSame(1, $tester->getStatusCode());
    }

    // -- the override rule, end to end -------------------------------

    public function testRunsADisabledServiceThroughTheSelectorFormAndExitsZero(): void
    {
        $svc = new FakeService('api', finishAfterTicks: 2);
        [$tester] = self::tester(
            self::registryOf($svc),
            ['api' => new ServiceConfig(enabled: false)],
        );
        $tester->execute(['service' => ['api']]);

        $this->assertSame(1, $svc->startCount);
        $this->assertSame(0, $tester->getStatusCode());
    }

    public function testTheSupervisorFormDoesNotStartThatSameDisabledService(): void
    {
        $svc = new FakeService('api', finishAfterTicks: 2);
        [$tester] = self::tester(
            self::registryOf($svc),
            ['api' => new ServiceConfig(enabled: false)],
        );
        $tester->execute([]);

        $this->assertSame(0, $svc->startCount);
        $this->assertSame(2, $tester->getStatusCode());
    }

    public function testSupervisorFormStartsEveryEnabledService(): void
    {
        $a = new FakeService('api', finishAfterTicks: 2);
        $b = new FakeService('socket', finishAfterTicks: 2);
        [$tester] = self::tester(self::registryOf($a, $b), self::enabled(['api', 'socket']));
        $tester->execute([]);

        $this->assertSame(1, $a->startCount);
        $this->assertSame(1, $b->startCount);
        $this->assertSame(0, $tester->getStatusCode());
    }
}
