<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\SafetyClass;
use HopTop\Kit\Mcp\Surface;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * The surface-exposure gate.
 *
 * This is the check that decides whether a remote caller may run a
 * destructive command, so the default has to be provably closed rather
 * than merely conventional.
 */
final class PolicyTest extends TestCase
{
    #[Test]
    public function defaultPolicyBlocksDestructiveInvocationOnEveryRemoteSurface(): void
    {
        $policy = Policy::default();
        $destructive = new SafetyClass(destructive: true);

        self::assertFalse(
            $policy->allowed($destructive, Surface::Mcp),
            'an empty allow-list must mean block-all, not allow-all',
        );
    }

    #[Test]
    public function localSurfacesRunDestructiveCommandsRegardless(): void
    {
        $policy = Policy::default();
        $destructive = new SafetyClass(destructive: true);

        self::assertTrue($policy->allowed($destructive, Surface::Cli));
        self::assertTrue($policy->allowed($destructive, Surface::Lib));
    }

    #[Test]
    public function nonDestructiveCommandsRunOnEverySurface(): void
    {
        $policy = Policy::default();

        self::assertTrue($policy->allowed(new SafetyClass(), Surface::Mcp));
    }

    #[Test]
    public function destructiveInvocationNeedsItsSurfaceNamedExplicitly(): void
    {
        $policy = new Policy(allowDestructiveOn: [Surface::Mcp]);

        self::assertTrue($policy->allowed(new SafetyClass(destructive: true), Surface::Mcp));
    }

    #[Test]
    public function everyDestructiveTierIsClassifiedDestructive(): void
    {
        foreach (['destructive', 'destructive-local', 'destructive-shared'] as $tier) {
            self::assertTrue(
                SafetyClass::classify(['kit/side-effect' => $tier])->destructive,
                $tier.' must classify as destructive',
            );
        }

        self::assertFalse(SafetyClass::classify(['kit/side-effect' => 'write'])->destructive);
        self::assertFalse(SafetyClass::classify([])->destructive);
    }

    #[Test]
    public function annotationsDriveAuthAndConfirmationFlags(): void
    {
        $class = SafetyClass::classify([
            'kit/auth-required' => 'true',
            'kit/requires-confirmation' => 'true',
            'kit/permissions' => 'read, write , ',
        ]);

        self::assertTrue($class->authRequired);
        self::assertTrue($class->requiresConfirmation);
        self::assertSame(['read', 'write'], $class->permissions);
    }
}
