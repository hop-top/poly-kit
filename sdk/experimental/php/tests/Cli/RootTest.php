<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Cli;

use HopTop\Kit\Cli\CommandSpec;
use HopTop\Kit\Cli\Delegation\Gate;
use HopTop\Kit\Cli\Delegation\PolicyLoader;
use HopTop\Kit\Cli\Delegation\PromptSource;
use HopTop\Kit\Cli\FlagDef;
use HopTop\Kit\Cli\Invocation;
use HopTop\Kit\Cli\Root;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

final class RootTest extends TestCase
{
    public int $ran = 0;

    /** @param list<CommandSpec> $cmds
     * @return array{int, string, string}
     */
    private function exec(array $cmds, array $args, ?PromptSource $p = null, ?PolicyLoader $loader = null): array
    {
        $out = fopen('php://memory', 'w+b');
        $err = fopen('php://memory', 'w+b');
        self::assertIsResource($out);
        self::assertIsResource($err);
        $root = new Root('app', '1.0.0', 'About', $loader, $p ?? PromptSource::none(), $out, $err);
        foreach ($cmds as $c) {
            $root->add($c);
        }
        $code = $root->run($args);
        rewind($out);
        rewind($err);

        return [$code, (string) stream_get_contents($out), (string) stream_get_contents($err)];
    }

    private function leaf(string $name, array $ann = [], array $flags = []): CommandSpec
    {
        return new CommandSpec($name, '', annotations: $ann, flags: $flags, handler: function (Invocation $i) use ($name): int {
            $this->ran++;
            fwrite($i->stdout, json_encode(['name' => $name, 'flags' => $i->flags, 'args' => $i->args, 'dry' => $i->isDryRun()], JSON_THROW_ON_ERROR) . "\n");

            return 0;
        });
    }

    #[Test]
    public function persistentFlagsWorkBeforeAndAfterTheCommandAndWithEquals(): void
    {
        $c = [$this->leaf('rm', ['kit/side-effect' => 'destructive'])];
        foreach ([['--confirm', 'yes', 'rm'], ['rm', '--confirm', 'yes'], ['--confirm=yes', 'rm'], ['rm', '--confirm=yes']] as $a) {
            [$code] = $this->exec($c, $a);
            self::assertSame(0, $code, implode(' ', $a));
        }
    }

    #[Test]
    public function doubleDashEndsOptionsAndKeepsPositionals(): void
    {
        [$code, $out] = $this->exec([$this->leaf('ls', ['kit/side-effect' => 'read'])], ['ls', '--', '--confirm', 'x']);
        self::assertSame(0, $code);
        self::assertStringContainsString('"args":["--confirm","x"]', $out);
    }

    #[Test]
    public function unknownFlagCommandAndMissingValueAreUsage(): void
    {
        $c = [$this->leaf('ls', ['kit/side-effect' => 'read'])];
        foreach ([['ls', '--nope'], ['nope'], ['ls', '--confirm'], ['--max-ops', 'x', 'ls'], ['--dry-run=maybe', 'ls']] as $a) {
            [$code, , $err] = $this->exec($c, $a);
            self::assertSame(2, $code, implode(' ', $a));
            self::assertStringContainsString('USAGE', $err);
        }
        self::assertSame(0, $this->ran);
    }

    #[Test]
    public function leafLocalConfirmShadowsTheRootsEvenWhenUnset(): void
    {
        $c = [$this->leaf('rm', ['kit/side-effect' => 'destructive'], [new FlagDef('confirm')])];
        [$code] = $this->exec($c, ['--confirm', 'yes', 'rm']);
        self::assertSame(5, $code, 'root --confirm=yes must not lift a leaf that owns --confirm');
        [$code] = $this->exec($c, ['rm', '--confirm', 'yes']);
        self::assertSame(0, $code);
    }

    #[Test]
    public function leafLocalPolicyIsNotTheDelegationPolicy(): void
    {
        $c = [$this->leaf('add', ['kit/side-effect' => 'write'], [new FlagDef('policy')])];
        [$code, $out] = $this->exec($c, ['add', '--policy', 'x.yaml']);
        self::assertSame(0, $code);
        self::assertStringContainsString('"policy":"x.yaml"', $out);
    }

    #[Test]
    public function confirmTokenFlagExistsOnlyOnTokenLeaves(): void
    {
        $plain = [$this->leaf('rm', ['kit/side-effect' => 'destructive'])];
        [$code] = $this->exec($plain, ['rm', '--confirm-token', 'x']);
        self::assertSame(2, $code);
        $tok = [$this->leaf('purge', ['kit/side-effect' => 'destructive', 'kit/destructive-token' => 'required'])];
        [$code, , $err] = $this->exec($tok, ['--confirm', 'yes', 'purge']);
        self::assertSame(5, $code);
        self::assertStringContainsString('requires --confirm-token=' . Gate::destructiveToken('app purge'), $err);
        self::assertSame('9bcc9064589a', Gate::destructiveToken('kitapp purge'));
    }

    #[Test]
    public function tokenPreviewStaysOutOfMachineStderr(): void
    {
        $tok = [$this->leaf('purge', ['kit/side-effect' => 'destructive', 'kit/destructive-token' => 'required'])];
        [, , $err] = $this->exec($tok, ['--format', 'json', 'purge']);
        $j = json_decode($err, true, 512, JSON_THROW_ON_ERROR);
        self::assertSame('UNAUTHORIZED', $j['code']);
        self::assertStringContainsString('--confirm-token=', $j['suggested_fix']);
    }

    #[Test]
    public function malformedTierIsRefusedBeforeTheHandler(): void
    {
        [$code, , $err] = $this->exec([$this->leaf('x', ['kit/side-effect' => 'bogus'])], ['--confirm', 'yes', 'x']);
        self::assertSame(2, $code);
        self::assertStringContainsString('malformed kit/side-effect tag "bogus"', $err);
        self::assertSame(0, $this->ran);
    }

    #[Test]
    public function inferredDestructiveDryRunRendersPlanWithoutHandler(): void
    {
        $c = [$this->leaf('delete')];
        [$code, $out] = $this->exec($c, ['--format', 'json', '--dry-run', 'delete']);
        self::assertSame(0, $code);
        $plan = json_decode($out, true, 512, JSON_THROW_ON_ERROR);
        self::assertSame('app delete', $plan['command']);
        self::assertSame('unknown-destructive', $plan['effects'][0]['kind']);
        self::assertFalse($plan['effects'][0]['reversible']);
        self::assertMatchesRegularExpression('/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/', $plan['generated_at']);
        self::assertSame(0, $this->ran);

        [, $yaml] = $this->exec($c, ['--format', 'yaml', '--dry-run', 'delete']);
        self::assertStringContainsString("command: 'app delete'", $yaml);
        [, $text] = $this->exec($c, ['--dry-run', 'delete']);
        self::assertStringStartsWith("Dry run: app delete (not run)\nWarning: ", $text);
        self::assertSame(0, $this->ran);
    }

    #[Test]
    public function explicitDryRunAnnotationRunsTheHandlerEvenWhenNamedDestructive(): void
    {
        $c = [$this->leaf('delete', ['kit/side-effect' => 'destructive', 'kit/dry-run' => 'supported'])];
        [$code, $out] = $this->exec($c, ['--dry-run', 'delete']);
        self::assertSame(0, $code);
        self::assertStringContainsString('"dry":true', $out);
    }

    #[Test]
    public function maxOpsBudgetRefusesAfterTheBudgetIsSpent(): void
    {
        $gate = new Gate('app', null, PromptSource::none());
        $eng = $gate->engine(new \HopTop\Kit\Cli\Delegation\Globals(maxOps: 1));
        $leaf = new \HopTop\Kit\Cli\Delegation\Leaf('app', ['add'], ['kit/side-effect' => 'write']);
        $g = new \HopTop\Kit\Cli\Delegation\Globals(maxOps: 1);
        self::assertSame('ran', $gate->run($leaf, $g, static fn (): string => 'ran', engine: $eng));
        try {
            $gate->run($leaf, $g, static fn (): string => 'ran', engine: $eng);
            self::fail('budget not enforced');
        } catch (\HopTop\Kit\Cli\Delegation\GateRefusal $e) {
            self::assertSame(64, $e->error->exitCode);
        }
    }

    #[Test]
    public function readsAreNotCountedAgainstTheBudget(): void
    {
        $gate = new Gate('app', null, PromptSource::none());
        $g = new \HopTop\Kit\Cli\Delegation\Globals(maxOps: 1);
        $eng = $gate->engine($g);
        $leaf = new \HopTop\Kit\Cli\Delegation\Leaf('app', ['ls'], ['kit/side-effect' => 'read']);
        for ($i = 0; $i < 3; ++$i) {
            $gate->run($leaf, $g, static fn (): int => 1, engine: $eng);
        }
        $this->addToAssertionCount(1);
    }

    #[Test]
    public function policyFlagWithoutLoaderIsUsage(): void
    {
        [$code, , $err] = $this->exec([$this->leaf('add', ['kit/side-effect' => 'write'])], ['--policy', 'x', 'add']);
        self::assertSame(2, $code);
        self::assertStringContainsString('no policy loader', $err);
    }

    #[Test]
    public function kitDefaultNeedsNoLoader(): void
    {
        $c = [$this->leaf('add', ['kit/side-effect' => 'write']), $this->leaf('ls', ['kit/side-effect' => 'read'])];
        [$code, , $err] = $this->exec($c, ['--policy', 'kit-default', 'add']);
        self::assertSame(5, $code);
        self::assertStringContainsString('policy kit-default:', $err);
        [$code] = $this->exec($c, ['--policy', 'kit-default', 'ls']);
        self::assertSame(0, $code);
    }

    #[Test]
    public function promptSourceReadsScriptedAnswersAndDefaultsByTerminal(): void
    {
        $c = [$this->leaf('rm', ['kit/side-effect' => 'destructive'])];
        [$code] = $this->exec($c, ['rm'], PromptSource::fromAnswers(['Y']));
        self::assertSame(0, $code);
        [$code, , $err] = $this->exec($c, ['--confirm', 'prompt', 'rm'], PromptSource::fromAnswers(['n']));
        self::assertSame(5, $code);
        self::assertStringContainsString('aborted by user at confirm prompt for app rm', $err);
        [$code] = $this->exec($c, ['--confirm', 'prompt', 'rm'], PromptSource::none());
        self::assertSame(5, $code);
    }
}
