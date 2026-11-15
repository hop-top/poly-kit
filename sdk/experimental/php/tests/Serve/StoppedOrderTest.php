<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Serve\ServiceConfig;
use HopTop\Kit\Serve\ServiceRegistry;
use HopTop\Kit\Serve\Supervisor;
use PHPUnit\Framework\TestCase;

/**
 * stopped is a service's stop returning, so on a canceled run the
 * events follow reverse start order whichever service happened to
 * finish in the pass that observed the cancellation.
 */
final class StoppedOrderTest extends TestCase
{
    /** @return list<string> */
    private static function stopped(RecordingLogger $log): array
    {
        $out = [];
        foreach ($log->withTopic('kit.serve.service.stopped') as $r) {
            $out[] = (string) $r['fields']['service'];
        }
        return $out;
    }

    /** @param list<string> $names @return array<string, ServiceConfig> */
    private static function configs(array $names): array
    {
        $out = [];
        foreach ($names as $n) {
            $out[$n] = new ServiceConfig(enabled: true, readyTimeout: 30.0, stopTimeout: 30.0);
        }
        return $out;
    }

    public function testFinishInThePassThatSeesCancelKeepsReverseOrder(): void
    {
        $cancel = new Cancellation();
        $registry = new ServiceRegistry();
        // alpha cancels mid-pass; beta then returns in the same pass.
        $registry->register(new CancellingService('alpha', $cancel));
        $registry->register(new FakeService('beta', finishAfterTicks: 1));
        $registry->register(new FakeService('gamma'));
        $log = new RecordingLogger();
        $sup = new Supervisor($registry, logger: $log);

        $res = $sup->run($cancel, ['alpha', 'beta', 'gamma'], self::configs(['alpha', 'beta', 'gamma']));

        $this->assertNull($res->error);
        $this->assertSame(['gamma', 'beta', 'alpha'], self::stopped($log));
    }

    public function testEarlySelfExitStillReportsStoppedOnceAtExit(): void
    {
        $cancel = new Cancellation();
        $registry = new ServiceRegistry();
        $registry->register(new FakeService('steady'));
        $registry->register(new FakeService('quitter', finishAfterTicks: 1));
        $registry->register(new CancellingService('canceller', $cancel, afterTicks: 5));
        $log = new RecordingLogger();
        $sup = new Supervisor($registry, logger: $log);

        $sup->run($cancel, ['steady', 'quitter', 'canceller'], self::configs(['steady', 'quitter', 'canceller']));

        // quitter reported at exit, ahead of the shutdown; once only.
        $this->assertSame(['quitter', 'canceller', 'steady'], self::stopped($log));
    }
}
