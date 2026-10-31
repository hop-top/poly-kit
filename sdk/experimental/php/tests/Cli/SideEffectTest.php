<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Cli;

use HopTop\Kit\Cli\SideEffect;
use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\SafetyClass;
use HopTop\Kit\Mcp\Surface;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * Mirrors the `infer` tree of contracts/cli-root-v1: one destructive-name
 * table, explicit annotation wins, malformed is never inferred over.
 */
final class SideEffectTest extends TestCase
{
    /** @return iterable<string, array{string, array<string,string>, ?string, bool}> */
    public static function vectors(): iterable
    {
        yield 'unannotated delete' => ['delete', [], 'destructive-shared', true];
        yield 'unannotated remove' => ['remove', [], 'destructive-shared', true];
        yield 'unannotated rm' => ['rm', [], 'destructive-shared', true];
        yield 'unannotated destroy' => ['destroy', [], 'destructive-shared', true];
        yield 'unannotated purge' => ['purge', [], 'destructive-shared', true];
        yield 'unannotated drop' => ['drop', [], 'destructive-shared', true];
        yield 'empty annotation is absent' => ['delete', ['kit/side-effect' => ''], 'destructive-shared', true];
        yield 'outside table' => ['list', [], null, false];
        yield 'near the table' => ['delete-all', [], null, false];
        yield 'case sensitive' => ['Delete', [], null, false];
        yield 'explicit read beats name' => ['drop', ['kit/side-effect' => 'read'], 'read', false];
        yield 'malformed not inferred over' => ['purge', ['kit/side-effect' => 'bogus'], 'bogus', false];
        yield 'declared destructive-local kept' => ['delete', ['kit/side-effect' => 'destructive-local'], 'destructive-local', false];
        yield 'declared on non-table name' => ['wipe', ['kit/side-effect' => 'destructive'], 'destructive', false];
    }

    /**
     * @param array<string,string> $annotations
     */
    #[Test]
    #[DataProvider('vectors')]
    public function resolvesAndInfers(string $name, array $annotations, ?string $tier, bool $inferred): void
    {
        self::assertSame($inferred, SideEffect::inferredDestructive($name, $annotations));
        self::assertSame($tier, SideEffect::resolve($name, $annotations));
    }

    #[Test]
    public function tableIsSortedAndComplete(): void
    {
        self::assertSame(
            ['delete', 'destroy', 'drop', 'purge', 'rm', 'remove'],
            SideEffect::destructiveNames(),
        );
    }

    #[Test]
    public function safetyClassTreatsInferredDestructiveAsDestructive(): void
    {
        self::assertTrue(SafetyClass::classify([], 'delete')->destructive);
        self::assertFalse(SafetyClass::classify([], 'list')->destructive);
        self::assertFalse(SafetyClass::classify(['kit/side-effect' => 'read'], 'drop')->destructive);
        self::assertFalse(SafetyClass::classify(['kit/side-effect' => 'bogus'], 'purge')->destructive);
        self::assertFalse(SafetyClass::classify([])->destructive, 'no name, no inference');
    }

    #[Test]
    public function bridgeBlocksUnannotatedDestructiveNamedLeafOnMcp(): void
    {
        $root = new Command('root');
        $root->addCommand(
            new Command('delete', 'Delete', runner: static fn (): string => 'ran'),
            new Command('list', 'List', runner: static fn (): string => 'ran'),
        );
        $bridge = new Bridge($root);
        $policy = Policy::default();
        $classes = [];
        foreach ($bridge->leaves() as $leaf) {
            $classes[implode(' ', $leaf->path)] = $policy->allowed($leaf->class, Surface::Mcp);
        }
        self::assertSame(['delete' => false, 'list' => true], $classes);
    }
}
