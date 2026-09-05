<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Output\CliError;
use HopTop\Kit\Serve\Names;
use HopTop\Kit\Serve\Outcome;
use HopTop\Kit\Serve\PolicyGate;
use HopTop\Kit\Serve\PolicyVerdict;
use HopTop\Kit\Serve\Resolver;
use HopTop\Kit\Serve\Service;
use HopTop\Kit\Serve\ServiceConfig;
use HopTop\Kit\Serve\ServiceRegistrationException;
use HopTop\Kit\Serve\ServiceRegistry;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

/**
 * The hierarchy, the override rule, and the refusals — the part of the
 * contract an operator's systemd unit is written against.
 */
final class ResolverTest extends TestCase
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
    private static function enabled(array $names): array
    {
        $out = [];
        foreach ($names as $n) {
            $out[$n] = new ServiceConfig(enabled: true);
        }
        return $out;
    }

    // -- names ------------------------------------------------------

    #[DataProvider('validNames')]
    public function testAcceptsTheContractGrammar(string $name): void
    {
        $this->assertNull(Names::validate($name));
    }

    /** @return list<array{string}> */
    public static function validNames(): array
    {
        return [['a'], ['api'], ['api-2'], ['a-b-c'], ['x9']];
    }

    #[DataProvider('invalidNames')]
    public function testRejectsAnythingOutsideTheGrammar(string $name): void
    {
        $this->assertNotNull(Names::validate($name));
    }

    /** @return list<array{string}> */
    public static function invalidNames(): array
    {
        return [
            [''], ['API'], ['2api'], ['-api'], ['api_x'], ['api.x'], ['api '], ['ap i'],
        ];
    }

    public function testReservesExactlyAllNoneAndList(): void
    {
        $this->assertSame(['all', 'none', 'list'], Names::RESERVED);
        foreach (['all', 'none', 'list'] as $n) {
            $this->assertTrue(Names::isReserved($n));
            $this->assertNotNull(Names::validate($n), "{$n} must not be registrable");
        }
        $this->assertFalse(Names::isReserved('api'));
    }

    // -- registration seam ------------------------------------------

    public function testListsInRegistrationOrder(): void
    {
        $r = self::registryOf(
            new FakeService('zeta'),
            new FakeService('alpha'),
            new FakeService('mid'),
        );
        $this->assertSame(['zeta', 'alpha', 'mid'], $r->names());
        $this->assertSame(3, $r->size());
    }

    public function testRefusesADuplicateNameAtConstructionTime(): void
    {
        $r = self::registryOf(new FakeService('api'));

        // Last-writer-wins is forbidden: it turns a wiring bug into a
        // service silently not running.
        $this->expectException(ServiceRegistrationException::class);
        $this->expectExceptionMessage('already registered');
        $r->register(new FakeService('api'));
    }

    public function testOverrideReplacesInPlaceAndKeepsPosition(): void
    {
        $r = self::registryOf(new FakeService('api'), new FakeService('socket'));
        $replacement = new FakeService('api', addr: 'replaced');
        $r->override($replacement);

        $this->assertSame(['api', 'socket'], $r->names());
        $this->assertSame($replacement, $r->lookup('api'));
        $this->assertSame(2, $r->size());
    }

    public function testOverrideStillRefusesAnInvalidName(): void
    {
        $r = new ServiceRegistry();
        $this->expectException(ServiceRegistrationException::class);
        $r->override(new FakeService('list'));
    }

    public function testRefusesAReservedOrInvalidNameAtRegistration(): void
    {
        $r = new ServiceRegistry();
        $this->expectException(ServiceRegistrationException::class);
        $this->expectExceptionMessage('reserved');
        $r->register(new FakeService('all'));
    }

    // -- the hierarchy ----------------------------------------------

    public function testSupervisorFormSelectsEveryConfiguredAndEnabledService(): void
    {
        $r = self::registryOf(new FakeService('api'), new FakeService('socket'));
        $res = Resolver::resolve($r, [], self::enabled(['api', 'socket']));

        $this->assertSame(['api', 'socket'], $res->selected);
        $this->assertFalse($res->explicit);
        $this->assertNull($res->error);
    }

    public function testSkipsADisabledServiceSilentlyRatherThanFailing(): void
    {
        $r = self::registryOf(new FakeService('api'), new FakeService('socket'));
        $res = Resolver::resolve($r, [], [
            'api' => new ServiceConfig(enabled: true),
            'socket' => new ServiceConfig(enabled: false),
        ]);

        $this->assertSame(['api'], $res->selected);
        $this->assertSame(['socket'], $res->skipped);
        // Skipping must not affect the exit code.
        $this->assertNull($res->error);
    }

    public function testIgnoresAServiceWithNoConfigBlockAtAll(): void
    {
        $r = self::registryOf(new FakeService('api'), new FakeService('ghostly'));
        $res = Resolver::resolve($r, [], self::enabled(['api']));

        $this->assertSame(['api'], $res->selected);
        // Not configured at all is not the same as configured-disabled.
        $this->assertSame([], $res->skipped);
    }

    public function testSelectionPreservesRegistrationOrderNotConfigOrder(): void
    {
        $r = self::registryOf(new FakeService('zeta'), new FakeService('alpha'));
        $res = Resolver::resolve($r, [], [
            'alpha' => new ServiceConfig(enabled: true),
            'zeta' => new ServiceConfig(enabled: true),
        ]);
        $this->assertSame(['zeta', 'alpha'], $res->selected);
    }

    public function testSelectorFormSelectsExactlyTheNamedService(): void
    {
        $r = self::registryOf(new FakeService('api'), new FakeService('socket'));
        $res = Resolver::resolve($r, ['socket'], self::enabled(['api', 'socket']));

        $this->assertSame(['socket'], $res->selected);
        $this->assertTrue($res->explicit);
    }

    // -- the override rule ------------------------------------------

    public function testStartsADisabledServiceWhenNamedExplicitly(): void
    {
        // The load-bearing rule: a port that made `serve <service>`
        // respect `enabled` would silently do nothing where the
        // reference starts a server.
        $r = self::registryOf(new FakeService('api'));
        $res = Resolver::resolve($r, ['api'], ['api' => new ServiceConfig(enabled: false)]);

        $this->assertSame(['api'], $res->selected);
        $this->assertNull($res->error);
    }

    public function testStartsAServiceWithNoConfigBlockAtAllWhenNamed(): void
    {
        $r = self::registryOf(new FakeService('api'));
        $res = Resolver::resolve($r, ['api'], []);

        $this->assertSame(['api'], $res->selected);
        $this->assertNull($res->error);
    }

    public function testTheSameServiceIsRefusedUnderTheSupervisorForm(): void
    {
        // Same registry, same config; only the form differs.
        $r = self::registryOf(new FakeService('api'));
        $configs = ['api' => new ServiceConfig(enabled: false)];

        $this->assertSame(['api'], Resolver::resolve($r, ['api'], $configs)->selected);

        $aggregate = Resolver::resolve($r, [], $configs);
        $this->assertSame([], $aggregate->selected);
        $this->assertSame(Outcome::NoServices, $aggregate->outcome);
    }

    public function testOverrideDoesNotLiftTheConfigurationGate(): void
    {
        $r = self::registryOf(new FakeService('api', configError: 'addr: missing'));
        $res = Resolver::resolve($r, ['api'], []);

        $this->assertSame(Outcome::ConfigInvalid, $res->outcome);
        $this->assertSame(2, $res->error?->exitCode);
        $this->assertStringContainsString('service "api": addr: missing', (string) $res->error?->message);
    }

    public function testOverrideDoesNotLiftThePolicyGate(): void
    {
        $r = self::registryOf(new FakeService('api', class: ['destructive', 'ingress']));
        $res = Resolver::resolve($r, ['api'], [], self::denyingGate('blocked'));

        $this->assertSame(Outcome::PolicyDenied, $res->outcome);
        $this->assertSame(5, $res->error?->exitCode);
        $this->assertStringContainsString('side_effect=destructive', (string) $res->error?->message);
        $this->assertStringContainsString('network=ingress', (string) $res->error?->message);
        $this->assertStringContainsString('blocked', (string) $res->error?->message);
    }

    public function testEvaluatesTheGatesInOrderRegistrationConfigPolicy(): void
    {
        // A service that would fail BOTH config and policy reports the
        // config failure, because configuration is evaluated first.
        $r = self::registryOf(new FakeService(
            'api',
            configError: 'addr: missing',
            class: ['destructive', 'ingress'],
        ));
        $res = Resolver::resolve($r, ['api'], [], self::denyingGate('blocked'));
        $this->assertSame(Outcome::ConfigInvalid, $res->outcome);

        // And an unknown name beats both, because registration is first.
        $unknown = Resolver::resolve($r, ['ghost'], [], self::denyingGate('blocked'));
        $this->assertSame(Outcome::UnknownService, $unknown->outcome);
    }

    public function testPassesAnUnclassifiedServiceThroughThePolicyGate(): void
    {
        // A service declaring no class is unclassified and passes.
        $r = self::registryOf(new BareService('api'));
        $res = Resolver::resolve($r, ['api'], [], self::denyingGate('blocked'));

        $this->assertSame(['api'], $res->selected);
        $this->assertNull($res->error);
    }

    public function testPassesEveryServiceWhenNoGateIsWired(): void
    {
        $r = self::registryOf(new FakeService('api', class: ['destructive', 'ingress']));
        $this->assertSame(['api'], Resolver::resolve($r, ['api'], [], null)->selected);
    }

    // -- invalid selection ------------------------------------------

    public function testRefusesTwoOrMorePositionalArgumentsAsUsage2(): void
    {
        $r = self::registryOf(new FakeService('api'), new FakeService('socket'));
        $res = Resolver::resolve($r, ['api', 'socket'], self::enabled(['api', 'socket']));

        $this->assertSame(Outcome::InvalidSelection, $res->outcome);
        $this->assertSame(CliError::CODE_USAGE, $res->error?->code);
        $this->assertSame(2, $res->error?->exitCode);
        $this->assertStringContainsString('at most one service name', (string) $res->error?->message);
    }

    public function testRefusesAnUnknownServiceAsNotFound3NamingTheKnownSet(): void
    {
        $r = self::registryOf(new FakeService('api'), new FakeService('socket'));
        $res = Resolver::resolve($r, ['ghost'], []);

        $this->assertSame(Outcome::UnknownService, $res->outcome);
        $this->assertSame(CliError::CODE_NOT_FOUND, $res->error?->code);
        $this->assertSame(3, $res->error?->exitCode);
        $this->assertStringContainsString('unknown service "ghost"', (string) $res->error?->message);
        $this->assertStringContainsString('known: api, socket', (string) $res->error?->message);
    }

    public function testRefusesAReservedWordAsASelection(): void
    {
        // `list` cannot be registered, so naming it can only ever be a
        // NOT_FOUND — which is exactly why --list is a flag.
        $r = self::registryOf(new FakeService('api'));
        $res = Resolver::resolve($r, ['list'], []);
        $this->assertSame(Outcome::UnknownService, $res->outcome);
    }

    public function testSuggestsTheNearestNameOnANearMiss(): void
    {
        $r = self::registryOf(new FakeService('socket'));
        $res = Resolver::resolve($r, ['sockat'], []);
        $this->assertStringContainsString('socket', (string) $res->error?->suggestedFix);
    }

    public function testSuggestsNothingWhenNoNameIsClose(): void
    {
        $r = self::registryOf(new FakeService('socket'));
        $res = Resolver::resolve($r, ['zzzzzzzz'], []);
        $this->assertSame('', $res->error?->suggestedFix);
    }

    // -- zero services ----------------------------------------------

    public function testRefusesZeroResolvedServicesUnderTheSupervisorForm(): void
    {
        // A process that exits 0 without listening is indistinguishable
        // from a successful start to systemd or a container runtime.
        $r = self::registryOf(new FakeService('api'));
        $res = Resolver::resolve($r, [], ['api' => new ServiceConfig(enabled: false)]);

        $this->assertSame(Outcome::NoServices, $res->outcome);
        $this->assertSame(2, $res->error?->exitCode);
        $this->assertStringContainsString('no services configured and enabled', (string) $res->error?->message);
    }

    public function testRefusesAnEmptyRegistryUnderTheSupervisorForm(): void
    {
        $res = Resolver::resolve(new ServiceRegistry(), [], []);
        $this->assertSame(Outcome::NoServices, $res->outcome);
        $this->assertSame(2, $res->error?->exitCode);
    }

    private static function denyingGate(string $reason): PolicyGate
    {
        return new class ($reason) implements PolicyGate {
            public function __construct(private readonly string $reason)
            {
            }

            public function allow(string $sideEffect, string $network): PolicyVerdict
            {
                return PolicyVerdict::deny($this->reason);
            }
        };
    }
}
