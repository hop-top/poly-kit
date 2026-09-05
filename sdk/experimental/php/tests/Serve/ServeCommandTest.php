<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Output\Flags;
use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use HopTop\Kit\Output\Registry;
use HopTop\Kit\Serve\FlagOverrides;
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

    // -- --enable / --disable ------------------------------------------

    public function testEnableIsRepeatableAndMakesAnUnconfiguredServiceConfiguredAndEnabled(): void
    {
        // Neither service has a config block: the flag is what
        // configures each one, and naming both starts both.
        $a = new FakeService('alpha', finishAfterTicks: 2);
        $b = new FakeService('beta', finishAfterTicks: 2);
        [$tester] = self::tester(self::registryOf($a, $b), []);
        $tester->execute(['--enable' => ['alpha', 'beta']]);

        $this->assertSame(1, $a->startCount);
        $this->assertSame(1, $b->startCount);
        $this->assertSame(0, $tester->getStatusCode());
    }

    public function testDisableSkipsAnEnabledServiceSilentlyUnderTheSupervisorForm(): void
    {
        $on = new FakeService('on', finishAfterTicks: 2);
        $off = new FakeService('off', finishAfterTicks: 2);
        [$tester] = self::tester(self::registryOf($on, $off), self::enabled(['on', 'off']));
        $tester->execute(['--disable' => ['off']]);

        $this->assertSame(1, $on->startCount);
        $this->assertSame(0, $off->startCount);
        $this->assertSame(0, $tester->getStatusCode());
    }

    public function testDisablingEveryEnabledServiceLeavesNothingToRunAndExitsTwo(): void
    {
        // finishAfterTicks keeps a regression from hanging the suite: a
        // service the flag failed to disable would otherwise wait for a
        // signal.
        [$tester] = self::tester(
            self::registryOf(new FakeService('api', finishAfterTicks: 2)),
            self::enabled(['api']),
        );
        $tester->execute(['--disable' => ['api']]);
        $this->assertSame(2, $tester->getStatusCode());
    }

    /** @return iterable<string, array{0: string}> */
    public static function enableDisableFlags(): iterable
    {
        yield '--enable' => ['--enable'];
        yield '--disable' => ['--disable'];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('enableDisableFlags')]
    public function testEnableAndDisableAreRefusedUnderTheSelectorForm(string $flag): void
    {
        $svc = new FakeService('api', finishAfterTicks: 2);
        [$tester] = self::tester(self::registryOf($svc), self::enabled(['api']));
        $tester->execute(['service' => ['api'], $flag => ['api']]);

        $this->assertSame(0, $svc->startCount);
        $this->assertSame(2, $tester->getStatusCode());
    }

    public function testFlagsDoNotLeakIntoALaterRunOnTheSameCommand(): void
    {
        // The command's configs are what a second serve in one process
        // reads; a run's flags must not survive it.
        $svc = new FakeService('api', finishAfterTicks: 2);
        [$tester] = self::tester(self::registryOf($svc), self::enabled(['api']));
        $tester->execute(['--disable' => ['api']]);
        $this->assertSame(2, $tester->getStatusCode());

        $tester->execute([]);
        $this->assertSame(1, $svc->startCount);
        $this->assertSame(0, $tester->getStatusCode());
    }

    // -- timeout flags ---------------------------------------------------

    public function testReadyTimeoutBoundsStartForEveryResolvedService(): void
    {
        // Without the flag the default 30s budget would outlive the
        // test; the flag is what turns a never-ready service into a
        // start failure, and the elapsed bound is what proves it.
        [$tester] = self::tester(
            self::registryOf(new FakeService('api', readyAfterTicks: 1_000_000)),
            self::enabled(['api']),
        );
        $started = microtime(true);
        $tester->execute(['--ready-timeout' => '50ms']);

        $this->assertSame(1, $tester->getStatusCode());
        $this->assertLessThan(5.0, microtime(true) - $started);
    }

    public function testShutdownTimeoutBoundsTheWholeStop(): void
    {
        [$tester] = self::tester(
            self::registryOf(new FakeService('api', finishAfterTicks: 2, stopDelay: 0.05)),
            self::enabled(['api']),
        );
        $tester->execute(['service' => ['api'], '--stop-timeout' => '10ms', '--shutdown-timeout' => '1ms']);
        $this->assertSame(1, $tester->getStatusCode());
    }

    public function testAnUnparseableDurationIsUsageExitTwo(): void
    {
        $svc = new FakeService('api', finishAfterTicks: 2);
        [$tester] = self::tester(self::registryOf($svc), self::enabled(['api']));
        $tester->execute(['--ready-timeout' => '30x']);

        $this->assertSame(0, $svc->startCount);
        $this->assertSame(2, $tester->getStatusCode());
    }

    // -- the pure overrides ----------------------------------------------

    public function testApplyEnableDisableMakesAnUnconfiguredServiceConfiguredAndEnabled(): void
    {
        $out = FlagOverrides::applyEnableDisable([], ['api'], []);
        $this->assertTrue($out['api']->enabled);
    }

    public function testApplyEnableDisableClearsEnablementAndKeepsBudgets(): void
    {
        $out = FlagOverrides::applyEnableDisable(
            ['api' => new ServiceConfig(enabled: true, readyTimeout: 5.0)],
            [],
            ['api'],
        );
        $this->assertFalse($out['api']->enabled);
        $this->assertSame(5.0, $out['api']->readyTimeout);
    }

    public function testApplyEnableDisableIgnoresAnUnconfiguredDisable(): void
    {
        $this->assertSame([], FlagOverrides::applyEnableDisable([], [], ['ghost']));
    }

    public function testApplyEnableDisableLetsEnableWinOverDisable(): void
    {
        $out = FlagOverrides::applyEnableDisable([], ['api'], ['api']);
        $this->assertTrue($out['api']->enabled);
    }

    public function testApplyTimeoutsAppliesOneBudgetToEveryService(): void
    {
        $out = FlagOverrides::applyTimeouts(
            ['a' => new ServiceConfig(enabled: true), 'b' => new ServiceConfig(stopTimeout: 1.0)],
            10.0,
            20.0,
        );
        $this->assertTrue($out['a']->enabled);
        $this->assertFalse($out['b']->enabled);
        foreach (['a', 'b'] as $name) {
            $this->assertSame(10.0, $out[$name]->readyTimeout);
            $this->assertSame(20.0, $out[$name]->stopTimeout);
        }
    }

    public function testApplyTimeoutsLeavesTheMapAloneWithoutFlags(): void
    {
        $src = ['a' => new ServiceConfig(readyTimeout: 7.0)];
        $this->assertSame($src, FlagOverrides::applyTimeouts($src, null, null));
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
