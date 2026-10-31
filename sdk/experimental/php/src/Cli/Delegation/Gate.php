<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

use Closure;
use HopTop\Kit\Cli\DryRunPolicy;
use HopTop\Kit\Cli\SideEffect;
use HopTop\Kit\Output\CliError;
use Symfony\Component\Yaml\Yaml;

/**
 * The delegation gates a leaf passes before its handler runs. Ports Go
 * `console/cli/policy_runE.go` and `global_dryrun.go` in Go's order:
 * `--dry-run` enforcement, the named `--policy`, the `--confirm` matrix with
 * the typed `--confirm-token`, then the `--max-ops` budget accounted after a
 * successful mutating run.
 *
 * `--dry-run` on an inferred destructive leaf (annotation absent, name in
 * the destructive table) never invokes the handler: such a leaf never
 * promised to honor dry-run, so its handler may really mutate. Kit renders a
 * preview plan itself.
 */
final class Gate
{
    public const CONFIRM_TOKEN_FLAG = 'confirm-token';

    public const SUPERVISOR_COMMAND = 'serve';

    /** @var Closure(string): Policy|null */
    private readonly ?Closure $policyLoader;

    private readonly PromptSource $prompt;

    /** @param (callable(string): Policy)|null $policyLoader */
    public function __construct(
        public readonly string $program,
        ?callable $policyLoader = null,
        ?PromptSource $prompt = null,
        private readonly bool $dryRunDisabled = false,
    ) {
        $this->policyLoader = null === $policyLoader ? null : Closure::fromCallable($policyLoader);
        $this->prompt = $prompt ?? PromptSource::controllingTty();
    }

    /** The token a typed-confirmation command demands: sha256(path)[:6] as hex. */
    public static function destructiveToken(string $commandPath): string
    {
        return substr(hash('sha256', $commandPath), 0, 12);
    }

    /** @param array<string, string> $annotations */
    public static function requiresToken(array $annotations): bool
    {
        return \in_array($annotations[SideEffect::DESTRUCTIVE_TOKEN] ?? '', ['required', 'true'], true);
    }

    /**
     * The engine for an invocation: `--policy` through the loader, else an
     * empty (default-permit) policy under `--max-ops`.
     *
     * @throws GateRefusal
     */
    public function engine(Globals $g): Engine
    {
        if (Policy::KIT_DEFAULT === $g->policy) {
            $p = Policy::kitDefault();
        } elseif ('' !== $g->policy) {
            if (null === $this->policyLoader) {
                throw new GateRefusal(CliError::usage('--policy is set but no policy loader is wired (policyLoader:)'));
            }
            try {
                $p = ($this->policyLoader)($g->policy);
            } catch (PolicyException $e) {
                // Not an authorization refusal: the gate never ran.
                throw new GateRefusal(CliError::usage("policy \"{$g->policy}\": {$e->getMessage()}"));
            }
        } else {
            $p = new Policy();
        }

        return new Engine($p, $g->maxOps);
    }

    /**
     * Run $inner behind the gates; it receives whether kit applied dry-run.
     * Returns null when kit previewed an inferred destructive leaf instead
     * of running it.
     *
     * @template T
     * @param callable(bool): T $inner
     * @param resource|null $err
     * @param resource|null $out
     * @return T|null
     * @throws GateRefusal
     */
    public function run(Leaf $leaf, Globals $g, callable $inner, ?Engine $engine = null, mixed $err = null, mixed $out = null): mixed
    {
        $ann = $leaf->annotations;
        // Fail closed: Go refuses an invalid tier at Root.Validate; this port
        // has no root validator, so the gate does, before the handler.
        $bad = SideEffect::malformed($ann);
        if (null !== $bad) {
            $e = CliError::usage("\"{$leaf->fullPath()}\" declares a malformed kit/side-effect tag \"{$bad}\" that names no tier");
            throw new GateRefusal(self::withFix($e, 'declare one of the kit side-effect tiers'));
        }
        $tier = SideEffect::resolve($leaf->name(), $ann);
        $inferred = SideEffect::inferredDestructive($leaf->name(), $ann);

        $dryRun = false;
        if ($g->dryRun && !$this->dryRunDisabled) {
            $p = SideEffect::resolveDryRun($leaf->name(), $ann);
            $refusal = SideEffect::dryRunRefusal($leaf->fullPath(), $ann, $p);
            if (null !== $refusal) {
                throw new GateRefusal($refusal);
            }
            $dryRun = DryRunPolicy::Allow === $p;
        }

        $eng = $engine ?? $this->engine($g);

        $policyConfirm = false;
        if (self::SUPERVISOR_COMMAND !== $leaf->verb()) {
            $v = $eng->authorize(new CommandRef($leaf->verb(), $tier, $ann[SideEffect::PERMISSIONS] ?? null));
            if (!$v->allowed) {
                throw new GateRefusal(CliError::unauthorized($v->reason));
            }
            $policyConfirm = $v->requireConfirm;
        }

        if ($dryRun && $inferred && '' === ($ann[SideEffect::DRY_RUN] ?? '')) {
            $this->renderPreview($leaf, $out ?? STDOUT);

            return null;
        }

        if (!$dryRun && (SideEffect::isDestructiveTier($tier) || $policyConfirm)) {
            $this->confirm($leaf, $tier ?? '', $g->confirm, $err ?? STDERR);
        }

        $result = $inner($dryRun);

        if (!$dryRun && (SideEffect::isWriteTier($tier) || SideEffect::isDestructiveTier($tier))) {
            try {
                $eng->recordOp();
            } catch (MaxOpsExceeded) {
                throw new GateRefusal(CliError::rateLimited("max-ops budget exceeded after running {$leaf->fullPath()}"));
            }
        }

        return $result;
    }

    /**
     * @param resource $err
     * @throws GateRefusal
     */
    private function confirm(Leaf $leaf, string $tier, string $raw, mixed $err): void
    {
        $mode = ConfirmMode::resolve($this->prompt, $raw);
        $path = $leaf->fullPath();
        if (self::requiresToken($leaf->annotations)) {
            // Never lets --confirm=yes alone proceed.
            $expected = self::destructiveToken($path);
            $got = $leaf->confirmToken;
            if ('' === $got) {
                $refusal = CliError::unauthorized("destructive command {$path} requires --confirm-token={$expected}");
                if (\in_array($leaf->format, ['json', 'yaml'], true)) {
                    // The preview would leave stderr unparseable; the envelope carries the instruction.
                    $refusal = self::withFix($refusal, "re-run with --confirm-token={$expected} to proceed");
                } else {
                    fwrite($err, "This is a destructive operation requiring typed confirmation.\nToken: {$expected}\n"
                        . "Re-run with --confirm-token={$expected} to proceed.\n");
                }
                throw new GateRefusal($refusal);
            }
            if ($got !== $expected) {
                throw new GateRefusal(CliError::unauthorized("--confirm-token mismatch for {$path}; expected {$expected}"));
            }

            return;
        }
        if (ConfirmMode::Yes === $mode || ConfirmMode::Auto === $mode) {
            return;
        }
        if (ConfirmMode::No === $mode) {
            // Transient: re-invoking with --confirm=yes clears it.
            throw new GateRefusal(CliError::unauthorized("destructive command {$path} refused: --confirm=no (or non-TTY default)")
                ->withTransience(CliError::TRANSIENCE_TRANSIENT));
        }
        $answer = $this->prompt->get()?->ask("This is a {$tier} operation ({$path}). Continue? [y/N] ");
        if (!\in_array($answer, ['y', 'yes'], true)) {
            throw new GateRefusal(CliError::unauthorized("aborted by user at confirm prompt for {$path}")
                ->withTransience(CliError::TRANSIENCE_TRANSIENT));
        }
    }

    private static function withFix(CliError $e, string $fix): CliError
    {
        return new CliError($e->code, $e->message, $e->cause, $fix, $e->alternatives, $e->exitCode, $e->transience, $e->source);
    }

    /** @param resource $out */
    private function renderPreview(Leaf $leaf, mixed $out): void
    {
        $path = $leaf->fullPath();
        $plan = [
            'command' => $path,
            'effects' => [[
                'kind' => 'unknown-destructive',
                'target' => $path,
                'reversible' => false,
                'detail' => 'inferred destructive-shared; a real run requires --confirm',
            ]],
            'warnings' => ["{$path} declares no kit/side-effect and was not run; kit previewed it "
                . 'because a command named like this may mutate state and never promised dry-run'],
            'generated_at' => gmdate('Y-m-d\TH:i:s\Z'),
        ];
        if ('json' === $leaf->format) {
            $json = json_encode($plan, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);
            // JSON_PRETTY_PRINT hard-codes 4 spaces; Go's encoder indents by 2.
            $json = (string) preg_replace_callback('/^( +)/m', static fn (array $m): string => str_repeat(' ', intdiv(\strlen($m[1]), 2)), $json);
            fwrite($out, $json . "\n");
        } elseif ('yaml' === $leaf->format) {
            fwrite($out, Yaml::dump($plan, 4, 2));
        } else {
            $text = "Dry run: {$path} (not run)\n";
            foreach ($plan['warnings'] as $w) {
                $text .= "Warning: {$w}\n";
            }
            fwrite($out, $text);
        }
    }
}
