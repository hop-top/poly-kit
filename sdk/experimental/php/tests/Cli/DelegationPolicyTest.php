<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Cli;

use HopTop\Kit\Cli\Delegation\CommandRef;
use HopTop\Kit\Cli\Delegation\Engine;
use HopTop\Kit\Cli\Delegation\Glob;
use HopTop\Kit\Cli\Delegation\MaxOpsExceeded;
use HopTop\Kit\Cli\Delegation\Policy;
use HopTop\Kit\Cli\Delegation\PolicyException;
use HopTop\Kit\Cli\Delegation\PolicyLoader;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

final class DelegationPolicyTest extends TestCase
{
    #[Test]
    public function globFollowsPathMatch(): void
    {
        self::assertTrue(Glob::match('*', 'abc'));
        self::assertFalse(Glob::match('a*', 'a/b'));
        self::assertTrue(Glob::match('a?c', 'abc'));
        self::assertTrue(Glob::match('[a-c]x', 'bx'));
        self::assertFalse(Glob::match('[^a-c]x', 'bx'));
        self::assertTrue(Glob::match('a\\*', 'a*'));
        self::assertNull(Glob::match('[', 'x'));
        self::assertNull(Glob::match('a\\', 'a'));
    }

    #[Test]
    public function matchAnyHonoursWildcardAndVerbPrefix(): void
    {
        self::assertTrue(Glob::matchAny(['*'], 'widget add'));
        self::assertTrue(Glob::matchAny(['widget:*'], 'widget add'));
        self::assertTrue(Glob::matchAny(['widget:*'], 'widget'));
        self::assertFalse(Glob::matchAny(['widget:*'], 'widgets add'));
        self::assertFalse(Glob::matchAny([], 'x'));
    }

    #[Test]
    public function engineAuthorizesByClass(): void
    {
        $e = new Engine(Policy::parse("allow:\n  write: []\n  destructive: []\n"));
        self::assertTrue($e->authorize(new CommandRef('ls', 'read'))->allowed);
        self::assertTrue($e->authorize(new CommandRef('x', null))->allowed);
        $w = $e->authorize(new CommandRef('add', 'write-shared'));
        self::assertFalse($w->allowed);
        self::assertSame('policy: write-shared not allowed for add', $w->reason);
        self::assertSame('policy: destructive-shared not allowed for rm', $e->authorize(new CommandRef('rm', 'destructive-shared'))->reason);
    }

    #[Test]
    public function engineFailsClosedOnUnknownTier(): void
    {
        $v = (new Engine(new Policy()))->authorize(new CommandRef('x', 'bogus'));
        self::assertFalse($v->allowed);
        self::assertSame('policy: unknown side-effect "bogus" for x', $v->reason);
    }

    #[Test]
    public function requireConfirmLiftsMatchingVerbs(): void
    {
        $e = new Engine(Policy::parse("require_confirm:\n  - add\n"));
        self::assertTrue($e->authorize(new CommandRef('add', 'write'))->requireConfirm);
        self::assertFalse($e->authorize(new CommandRef('ls', 'write'))->requireConfirm);
    }

    #[Test]
    public function unannotatedPolicyClassAppliesToUntaggedCommands(): void
    {
        $e = new Engine(Policy::parse("unannotated: write\nallow:\n  write: []\n"));
        self::assertFalse($e->authorize(new CommandRef('x', null))->allowed);
    }

    #[Test]
    public function kitDefaultRefusesWritesWithRemedy(): void
    {
        $v = (new Engine(Policy::kitDefault()))->authorize(new CommandRef('add', 'write'));
        self::assertFalse($v->allowed);
        self::assertStringContainsString('policy: write not allowed for add', $v->reason);
        self::assertStringContainsString('policy kit-default:', $v->reason);
        self::assertTrue((new Engine(Policy::kitDefault()))->authorize(new CommandRef('ls', 'read'))->allowed);
    }

    #[Test]
    public function recordOpEnforcesTheBudget(): void
    {
        $e = new Engine(new Policy(), 1);
        $e->recordOp();
        $this->expectException(MaxOpsExceeded::class);
        $e->recordOp();
    }

    #[Test]
    public function overrideBeatsPolicyBudget(): void
    {
        $e = new Engine(new Policy(maxOps: 1), 3);
        $e->recordOp();
        $e->recordOp();
        $e->recordOp();
        $this->addToAssertionCount(1);
    }

    #[Test]
    public function parseRejectsBrokenAndInvalid(): void
    {
        foreach (["allow: [\n", "- a\n- b\n", "unannotated: nope\n", "permissions:\n  - when: x\n"] as $bad) {
            try {
                Policy::parse($bad);
                self::fail("accepted: $bad");
            } catch (PolicyException) {
                $this->addToAssertionCount(1);
            }
        }
    }

    #[Test]
    public function parseValidatesCallers(): void
    {
        $this->expectException(PolicyException::class);
        Policy::parse("callers:\n  - principal: '['\n");
    }

    #[Test]
    public function loaderResolvesUnderXdgAndChecksNames(): void
    {
        $tmp = sys_get_temp_dir() . '/kit-pol-' . bin2hex(random_bytes(5));
        mkdir($tmp . '/xdg/app/policies', 0o700, true);
        file_put_contents($tmp . '/xdg/app/policies/ro.yaml', "allow:\n  write: []\n");
        $prev = getenv('XDG_CONFIG_HOME');
        putenv('XDG_CONFIG_HOME=' . $tmp . '/xdg');
        try {
            $l = PolicyLoader::xdg('app');
            self::assertSame('ro', $l('ro')->name);
            self::assertSame('kit-default', $l('kit-default')->name);
            foreach (['../x', '', $tmp . '/xdg/app/policies/ro', 'ghost'] as $bad) {
                try {
                    $l($bad);
                    self::fail("accepted $bad");
                } catch (PolicyException) {
                    $this->addToAssertionCount(1);
                }
            }
        } finally {
            $prev === false ? putenv('XDG_CONFIG_HOME') : putenv('XDG_CONFIG_HOME=' . $prev);
            @unlink($tmp . '/xdg/app/policies/ro.yaml');
            @rmdir($tmp . '/xdg/app/policies');
            @rmdir($tmp . '/xdg/app');
            @rmdir($tmp . '/xdg');
            @rmdir($tmp);
        }
    }
}
