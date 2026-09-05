<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Output\CliError;
use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Serve\Outcome;
use HopTop\Kit\Serve\Service;
use HopTop\Kit\Serve\ServiceConfig;
use HopTop\Kit\Serve\ServiceRegistry;
use HopTop\Kit\Serve\StartOrder;
use HopTop\Kit\Serve\Supervisor;
use HopTop\Kit\Serve\SupervisorConfig;
use HopTop\Kit\Serve\Topics;
use HopTop\Kit\Serve\TransientServiceException;
use PHPUnit\Framework\TestCase;
use RuntimeException;

/**
 * Readiness, the six transitions, ordered shutdown, the failure
 * policy, and the exit-code taxonomy.
 */
final class SupervisorTest extends TestCase
{
    private static function registryOf(Service ...$svcs): ServiceRegistry
    {
        $r = new ServiceRegistry();
        foreach ($svcs as $s) {
            $r->register($s);
        }
        return $r;
    }

    /** @param list<string> $names @return array<string, ServiceConfig> */
    private static function configs(array $names, float $ready = 30.0, float $stop = 30.0): array
    {
        $out = [];
        foreach ($names as $n) {
            $out[$n] = new ServiceConfig(enabled: true, readyTimeout: $ready, stopTimeout: $stop);
        }
        return $out;
    }

    // -- readiness and the transitions ------------------------------

    public function testStartsReportsReadyAndStopsCleanly(): void
    {
        $svc = new FakeService('api', finishAfterTicks: 2, addr: '127.0.0.1:8080');
        $log = new RecordingLogger();
        $sup = new Supervisor(self::registryOf($svc), logger: $log);

        $res = $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $this->assertSame(Outcome::CleanStop, $res->outcome);
        $this->assertSame(0, $res->exitCode());
        $this->assertSame(['api'], $res->started);
        $this->assertSame(['api'], $res->ready);
        $this->assertSame(1, $svc->startCount);
        $this->assertSame(1, $svc->stopCount);
    }

    public function testSurfacesExactlyTheSixContractTopicStrings(): void
    {
        // The strings are contract: a subscriber is written against
        // them and does not know which language published.
        $this->assertSame([
            'service.started' => 'kit.serve.service.started',
            'service.ready_reported' => 'kit.serve.service.ready_reported',
            'service.failed' => 'kit.serve.service.failed',
            'service.stopped' => 'kit.serve.service.stopped',
            'supervisor.ready_reported' => 'kit.serve.supervisor.ready_reported',
            'supervisor.stopped' => 'kit.serve.supervisor.stopped',
        ], Topics::all());
    }

    public function testNeverEmitsABareReadyAction(): void
    {
        // A bare `ready` fails the past-tense validation in
        // event-topics.md, so a port emitting it would publish a topic
        // Go subscribers reject.
        foreach (Topics::all() as $topic) {
            $this->assertStringEndsNotWith('.ready', $topic);
        }
        $this->assertSame('ready_reported', Topics::ACTION_READY_REPORTED);
    }

    public function testSurfacesEveryTransitionThroughTheLogWhenThereIsNoBus(): void
    {
        $svc = new FakeService('api', finishAfterTicks: 2, addr: '127.0.0.1:8080');
        $log = new RecordingLogger();
        $sup = new Supervisor(self::registryOf($svc), logger: $log);
        $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $topics = $log->topics();
        // Silence about a service that started, became ready or
        // stopped is the one thing the contract forbids outright.
        $this->assertContains('kit.serve.service.started', $topics);
        $this->assertContains('kit.serve.service.ready_reported', $topics);
        $this->assertContains('kit.serve.service.stopped', $topics);
        $this->assertContains('kit.serve.supervisor.ready_reported', $topics);
        $this->assertContains('kit.serve.supervisor.stopped', $topics);
    }

    public function testCarriesTheServiceIdentifierInThePayloadNeverInTheTopic(): void
    {
        $log = new RecordingLogger();
        $sup = new Supervisor(
            self::registryOf(new FakeService('api', finishAfterTicks: 2)),
            logger: $log,
        );
        $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $started = $log->withTopic('kit.serve.service.started');
        $this->assertNotSame([], $started);
        $this->assertSame('api', $started[0]['fields']['service']);
        // The identifier must not travel in the topic, or a subscriber
        // is forced to re-bind when a tool gains a service.
        foreach ($log->topics() as $topic) {
            $this->assertStringNotContainsString('api', $topic);
        }
    }

    public function testCarriesTheResolvedAddressOnReadyReportedAndNowhereElse(): void
    {
        $log = new RecordingLogger();
        $sup = new Supervisor(
            self::registryOf(new FakeService('api', finishAfterTicks: 2, addr: '127.0.0.1:8080')),
            logger: $log,
        );
        $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $ready = $log->withTopic('kit.serve.service.ready_reported');
        $this->assertSame('127.0.0.1:8080', $ready[0]['fields']['address']);

        foreach ($log->withTopic('kit.serve.service.started') as $r) {
            $this->assertArrayNotHasKey('address', array_filter($r['fields'], static fn ($v) => $v !== null));
        }
    }

    public function testOmitsTheAddressForAServiceThatDeclaresNone(): void
    {
        $log = new RecordingLogger();
        $sup = new Supervisor(self::registryOf(new BareService('api')), logger: $log);
        $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $ready = $log->withTopic('kit.serve.service.ready_reported');
        $this->assertNull($ready[0]['fields']['address'] ?? null);
    }

    public function testLogsAFailureAtErrorLevelNotInfo(): void
    {
        $log = new RecordingLogger();
        $sup = new Supervisor(
            self::registryOf(FakeService::failing('api', 'bind refused')),
            logger: $log,
        );
        $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $failed = $log->withTopic('kit.serve.service.failed');
        $this->assertNotSame([], $failed);
        $this->assertSame('error', $failed[0]['level']);
        $this->assertSame('bind refused', $failed[0]['fields']['error']);
        $this->assertSame('api', $failed[0]['fields']['service']);
    }

    public function testReportsTheAggregateReadyOnlyWhenEveryServiceIsReady(): void
    {
        $log = new RecordingLogger();
        $sup = new Supervisor(
            self::registryOf(
                new FakeService('api', finishAfterTicks: 3),
                FakeService::failing('socket', 'nope'),
            ),
            logger: $log,
        );
        $sup->run(new Cancellation(), ['api', 'socket'], self::configs(['api', 'socket']));

        // socket never became ready, so the aggregate never did.
        $this->assertSame([], $log->withTopic('kit.serve.supervisor.ready_reported'));
    }

    // -- ordered shutdown -------------------------------------------

    public function testStopsInTheExactReverseOfStartOrder(): void
    {
        $rec = new Recorder();
        $a = new FakeService('alpha', finishAfterTicks: 2);
        $b = new FakeService('beta', finishAfterTicks: 2);
        $c = new FakeService('gamma', finishAfterTicks: 2);
        foreach ([$a, $b, $c] as $s) {
            $s->recorder = $rec;
        }

        $sup = new Supervisor(self::registryOf($a, $b, $c), logger: new RecordingLogger());
        $sup->run(
            new Cancellation(),
            ['alpha', 'beta', 'gamma'],
            self::configs(['alpha', 'beta', 'gamma']),
        );

        $this->assertSame(['alpha', 'beta', 'gamma'], $rec->order('start'));
        // A dependent is always fully stopped before its dependency.
        $this->assertSame(['gamma', 'beta', 'alpha'], $rec->order('stop'));
    }

    public function testStartsADependencyBeforeItsDependent(): void
    {
        $r = self::registryOf(
            new FakeService('web', dependsOn: ['db']),
            new FakeService('db'),
        );
        $this->assertSame(['db', 'web'], StartOrder::of($r, ['web', 'db']));
    }

    public function testIsRegistrationOrderWithNoDeclarations(): void
    {
        $r = self::registryOf(new BareService('zeta'), new BareService('alpha'));
        $this->assertSame(['zeta', 'alpha'], StartOrder::of($r, ['zeta', 'alpha']));
    }

    public function testIgnoresADependencyOutsideTheSelectedSet(): void
    {
        // Under the selector form exactly one service runs, and its
        // dependencies are the operator's business.
        $r = self::registryOf(new FakeService('web', dependsOn: ['db']), new FakeService('db'));
        $this->assertSame(['web'], StartOrder::of($r, ['web']));
    }

    public function testThrowsOnADependencyCycle(): void
    {
        $r = self::registryOf(
            new FakeService('a', dependsOn: ['b']),
            new FakeService('b', dependsOn: ['a']),
        );
        $this->expectExceptionMessageMatches('/dependency cycle/');
        StartOrder::of($r, ['a', 'b']);
    }

    public function testCancellingTheDrainWhileServingStopsEverythingAndExitsZero(): void
    {
        // A signal-initiated stop is a clean stop: answering SIGTERM
        // non-zero makes every rolling restart look like a crash.
        //
        // The cancel has to land while the service is actually serving,
        // which is what a signal does. CancellingService trips the
        // latch from inside its own tick, standing in for the handler.
        $cancel = new Cancellation();
        $svc = new CancellingService('api', $cancel, afterTicks: 2);

        $sup = new Supervisor(self::registryOf($svc), logger: new RecordingLogger());
        $res = $sup->run($cancel, ['api'], self::configs(['api']));

        $this->assertSame(Outcome::CleanStop, $res->outcome);
        $this->assertSame(0, $res->exitCode());
        // It served before it was asked to stop, and it was stopped.
        $this->assertGreaterThanOrEqual(2, $svc->tickCount);
        $this->assertSame(1, $svc->stopCount);
    }

    public function testACancellationBeforeStartStopsOnlyWhatActuallyStarted(): void
    {
        // Cancelled before anything ran: nothing started, so nothing is
        // stopped, and the run is still a clean stop.
        $svc = new FakeService('api');
        $cancel = new Cancellation();
        $cancel->cancel();

        $sup = new Supervisor(self::registryOf($svc), logger: new RecordingLogger());
        $res = $sup->run($cancel, ['api'], self::configs(['api']));

        $this->assertSame(0, $svc->startCount);
        $this->assertSame(0, $svc->stopCount);
        $this->assertSame(Outcome::CleanStop, $res->outcome);
        $this->assertSame(0, $res->exitCode());
    }

    public function testAbandonsTheDrainWhenTheEscalationSignalFires(): void
    {
        $escalation = new Cancellation();
        $escalation->cancel();

        $a = new FakeService('alpha', finishAfterTicks: 2);
        $b = new FakeService('beta', finishAfterTicks: 2);
        $sup = new Supervisor(
            self::registryOf($a, $b),
            logger: new RecordingLogger(),
            escalation: $escalation,
        );
        $res = $sup->run(new Cancellation(), ['alpha', 'beta'], self::configs(['alpha', 'beta']));

        // The drain is abandoned, so neither service is stopped, and
        // the run exits with the crash code.
        $this->assertSame(0, $a->stopCount);
        $this->assertSame(0, $b->stopCount);
        $this->assertSame(1, $res->exitCode());
    }

    public function testAbandonsAStopThatExceedsItsBudgetAndMovesOn(): void
    {
        $slow = new FakeService('slow', finishAfterTicks: 2, stopDelay: 0.05);
        $fast = new FakeService('fast', finishAfterTicks: 2);
        $rec = new Recorder();
        $slow->recorder = $rec;
        $fast->recorder = $rec;

        $sup = new Supervisor(self::registryOf($slow, $fast), logger: new RecordingLogger());
        $res = $sup->run(new Cancellation(), ['slow', 'fast'], [
            'slow' => new ServiceConfig(enabled: true, stopTimeout: 0.001),
            'fast' => new ServiceConfig(enabled: true, stopTimeout: 30.0),
        ]);

        // One straggler must not block the whole shutdown: the next
        // service is stopped regardless.
        $this->assertSame(['fast', 'slow'], $rec->order('stop'));
        $this->assertSame(1, $res->exitCode());
        $this->assertArrayHasKey('slow', $res->failed);
    }

    public function testReportsAStopThatThrowsAsAFailure(): void
    {
        $sup = new Supervisor(
            self::registryOf(new FakeService(
                'api',
                finishAfterTicks: 2,
                failStopWith: new RuntimeException('drain exploded'),
            )),
            logger: new RecordingLogger(),
        );
        $res = $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $this->assertSame(1, $res->exitCode());
        $this->assertSame('drain exploded', $res->failed['api']);
    }

    public function testExceedingTheTotalShutdownBudgetIsShutdownTimeout(): void
    {
        $a = new FakeService('alpha', finishAfterTicks: 2, stopDelay: 0.05);
        $b = new FakeService('beta', finishAfterTicks: 2);
        $sup = new Supervisor(
            self::registryOf($a, $b),
            config: new SupervisorConfig(shutdownTimeout: 0.01),
            logger: new RecordingLogger(),
        );
        $res = $sup->run(new Cancellation(), ['alpha', 'beta'], self::configs(['alpha', 'beta']));

        $this->assertSame(Outcome::ShutdownTimeout, $res->outcome);
        $this->assertSame(1, $res->exitCode());
    }

    // -- failure policy ---------------------------------------------

    public function testBringsEverythingDownUnderFailFastWhenOneServiceCrashes(): void
    {
        $rec = new Recorder();
        $good = new FakeService('good');
        $bad = new FakeService(
            'bad',
            failTickAfter: new RuntimeException('crashed'),
            failTickAfterTicks: 1,
        );
        $good->recorder = $rec;
        $bad->recorder = $rec;

        $sup = new Supervisor(
            self::registryOf($good, $bad),
            config: new SupervisorConfig(failurePolicy: SupervisorConfig::POLICY_FAIL_FAST),
            logger: new RecordingLogger(),
        );
        $res = $sup->run(new Cancellation(), ['good', 'bad'], self::configs(['good', 'bad']));

        $this->assertSame(Outcome::RuntimeCrash, $res->outcome);
        $this->assertSame(1, $res->exitCode());
        // A tool whose transports front the same state is degraded,
        // not healthy, when one of them is gone.
        $this->assertSame(1, $good->stopCount);
        $this->assertSame('crashed', $res->failed['bad']);
    }

    public function testKeepsTheRestRunningUnderIsolateUntilTheLastOneStops(): void
    {
        $good = new FakeService('good', finishAfterTicks: 6);
        $bad = new FakeService(
            'bad',
            failTickAfter: new RuntimeException('crashed'),
            failTickAfterTicks: 1,
        );

        $sup = new Supervisor(
            self::registryOf($good, $bad),
            config: new SupervisorConfig(failurePolicy: SupervisorConfig::POLICY_ISOLATE),
            logger: new RecordingLogger(),
        );
        $res = $sup->run(new Cancellation(), ['good', 'bad'], self::configs(['good', 'bad']));

        // The survivor kept getting slices after its sibling died.
        $this->assertGreaterThan(1, $good->tickCount);
        // The process still exits with the crash code.
        $this->assertSame(1, $res->exitCode());
        $this->assertArrayHasKey('bad', $res->failed);
    }

    // -- exit-code taxonomy -----------------------------------------

    public function testMapsEveryOutcomeOntoTheContractTable(): void
    {
        $table = [
            ['clean-stop', Outcome::CleanStop, CliError::CODE_OK, 0],
            ['invalid-selection', Outcome::InvalidSelection, CliError::CODE_USAGE, 2],
            ['config-invalid', Outcome::ConfigInvalid, CliError::CODE_USAGE, 2],
            ['no-services', Outcome::NoServices, CliError::CODE_USAGE, 2],
            ['unknown-service', Outcome::UnknownService, CliError::CODE_NOT_FOUND, 3],
            ['policy-denied', Outcome::PolicyDenied, CliError::CODE_UNAUTHORIZED, 5],
            ['start-failed', Outcome::StartFailed, CliError::CODE_GENERIC, 1],
            ['runtime-crash', Outcome::RuntimeCrash, CliError::CODE_GENERIC, 1],
            ['shutdown-timeout', Outcome::ShutdownTimeout, CliError::CODE_GENERIC, 1],
        ];
        foreach ($table as [$value, $outcome, $code, $exit]) {
            $this->assertSame($value, $outcome->value);
            $this->assertSame($code, $outcome->code(), $value);
            $this->assertSame($exit, $outcome->exitCode(), $value);
        }
    }

    public function testTreatsACleanStopAsSuccessAndEverythingElseAsFailure(): void
    {
        $this->assertFalse(Outcome::CleanStop->isFailure());
        foreach (Outcome::cases() as $o) {
            if ($o !== Outcome::CleanStop) {
                $this->assertTrue($o->isFailure(), $o->value);
            }
        }
    }

    public function testWorstOutcomeKeepsTheFirstFailureAcrossAWholeRun(): void
    {
        // Under isolate the exit code reflects the worst outcome across
        // the whole run, not the last one.
        $this->assertSame(
            Outcome::RuntimeCrash,
            Outcome::worst([Outcome::CleanStop, Outcome::RuntimeCrash, Outcome::ShutdownTimeout]),
        );
        $this->assertSame(Outcome::CleanStop, Outcome::worst([]));
        $this->assertSame(Outcome::CleanStop, Outcome::worst([Outcome::CleanStop]));
    }

    public function testTreatsAStartFailureAsStartFailedAtExitOne(): void
    {
        $sup = new Supervisor(
            self::registryOf(FakeService::failing('api', 'bind refused')),
            logger: new RecordingLogger(),
        );
        $res = $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $this->assertSame(Outcome::StartFailed, $res->outcome);
        $this->assertSame(1, $res->exitCode());
        $this->assertStringContainsString('bind refused', (string) $res->error?->message);
    }

    public function testTreatsAReadinessTimeoutAsAStartFailure(): void
    {
        // readyAfterTicks far beyond what the budget allows.
        $sup = new Supervisor(
            self::registryOf(new FakeService('api', readyAfterTicks: 1_000_000)),
            logger: new RecordingLogger(),
        );
        $res = $sup->run(new Cancellation(), ['api'], [
            'api' => new ServiceConfig(enabled: true, readyTimeout: 0.05),
        ]);

        $this->assertSame(Outcome::StartFailed, $res->outcome);
        $this->assertSame(1, $res->exitCode());
        $this->assertStringContainsString('not ready', $res->failed['api']);
    }

    public function testDoesNotStartALaterServiceAfterAnEarlierOneFails(): void
    {
        $later = new FakeService('later');
        $sup = new Supervisor(
            self::registryOf(FakeService::failing('first', 'nope'), $later),
            logger: new RecordingLogger(),
        );
        $sup->run(new Cancellation(), ['first', 'later'], self::configs(['first', 'later']));

        $this->assertSame(0, $later->startCount);
    }

    public function testRefusesAnEmptySelectionRatherThanExitingZero(): void
    {
        $sup = new Supervisor(new ServiceRegistry(), logger: new RecordingLogger());
        $res = $sup->run(new Cancellation(), [], []);

        $this->assertSame(Outcome::NoServices, $res->outcome);
        $this->assertSame(2, $res->exitCode());
    }

    public function testPropagatesTransientExitSixUnchanged(): void
    {
        // An agent's retry branch behaves the same whichever language
        // the tool it is driving was written in.
        $sup = new Supervisor(
            self::registryOf(new FakeService(
                'api',
                failStartWith: new TransientServiceException('upstream blip'),
            )),
            logger: new RecordingLogger(),
        );
        $res = $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $this->assertSame(CliError::EXIT_TRANSIENT, $res->exitCode());
        $this->assertSame(CliError::CODE_TRANSIENT, $res->error?->code);
        $this->assertSame(CliError::TRANSIENCE_TRANSIENT, $res->error?->transience);
    }

    // -- re-execution ------------------------------------------------

    public function testRunsTwiceOnOneRegistryEachObservingOnlyItsOwnCancellation(): void
    {
        // A second run must serve until *its own* cancellation, never
        // inherit the first run's.
        $svc = new FakeService('api', finishAfterTicks: 2);
        $sup = new Supervisor(self::registryOf($svc), logger: new RecordingLogger());

        $first = $sup->run(new Cancellation(), ['api'], self::configs(['api']));
        $second = $sup->run(new Cancellation(), ['api'], self::configs(['api']));

        $this->assertSame(Outcome::CleanStop, $first->outcome);
        $this->assertSame(Outcome::CleanStop, $second->outcome);
        $this->assertSame(2, $svc->startCount);
        $this->assertSame(2, $svc->stopCount);
    }

    // -- configuration defaults --------------------------------------

    public function testConfigurationDefaultsMatchTheContractTable(): void
    {
        $this->assertSame(30.0, ServiceConfig::DEFAULT_READY_TIMEOUT);
        $this->assertSame(30.0, ServiceConfig::DEFAULT_STOP_TIMEOUT);
        $this->assertSame(60.0, SupervisorConfig::DEFAULT_SHUTDOWN_TIMEOUT);
        $this->assertSame('fail-fast', SupervisorConfig::POLICY_FAIL_FAST);
        // enabled defaults to false: an unrequested open port is the
        // risk the default guards against.
        $this->assertFalse((new ServiceConfig())->enabled);
    }
}
