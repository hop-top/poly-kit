<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli;

use Closure;
use HopTop\Kit\Cli\Delegation\Gate;
use HopTop\Kit\Cli\Delegation\GateRefusal;
use HopTop\Kit\Cli\Delegation\Globals;
use HopTop\Kit\Cli\Delegation\Leaf;
use HopTop\Kit\Cli\Delegation\Policy;
use HopTop\Kit\Cli\Delegation\PromptSource;
use HopTop\Kit\Output\CliError;

/**
 * A kit-owned CLI root: a command tree, the persistent delegation flags and
 * the gate every leaf passes before its handler runs.
 *
 * Persistent flags (accepted before or after the command): `--confirm
 * yes|no|prompt|auto`, `--max-ops <n>`, `--policy <name>`, `--dry-run` and
 * `--format <fmt>` (selects the error envelope's format). A leaf may declare
 * its own `--confirm` or `--max-ops`; that value shadows the root's, set or
 * not. `--policy` and `--dry-run` are the root's only, so a leaf-local
 * `--policy` never names the delegation policy. `--confirm-token` exists on
 * leaves annotated `kit/destructive-token: required`.
 *
 * The argv parser is deliberately small: `--flag value`, `--flag=value`,
 * `--` ending options; no short flags, no abbreviations.
 */
final class Root
{
    /** @var list<CommandSpec> */
    private array $commands = [];

    private readonly Gate $gate;

    /**
     * @param (Closure(string): Policy)|null $policyLoader resolves `--policy <name>`; see PolicyLoader::xdg()
     * @param resource|null $stdout
     * @param resource|null $stderr
     */
    public function __construct(
        public readonly string $program,
        public readonly string $version = '',
        public readonly string $about = '',
        ?Closure $policyLoader = null,
        ?PromptSource $prompt = null,
        private readonly mixed $stdout = null,
        private readonly mixed $stderr = null,
    ) {
        $this->gate = new Gate($program, $policyLoader, $prompt);
    }

    public function add(CommandSpec $c): self
    {
        $this->commands[] = $c;

        return $this;
    }

    /**
     * Run $args (without the program name); returns the exit code.
     *
     * @param list<string> $args
     */
    public function run(array $args): int
    {
        $out = $this->stdout ?? STDOUT;
        $err = $this->stderr ?? STDERR;
        $format = '';
        try {
            $p = $this->parse($args, $format);
            $leaf = new Leaf($this->program, $p['path'], $p['spec']->annotations, $format, $p['token']);
            $inv = static fn (bool $dry): mixed => null === $p['spec']->handler ? 0 : ($p['spec']->handler)(
                new Invocation($p['path'], $p['flags'], $p['args'], $out, $err, $dry),
            );
            $res = $this->gate->run($leaf, $p['globals'], $inv, err: $err, out: $out);

            return \is_int($res) ? $res : 0;
        } catch (GateRefusal $r) {
            $r->error->renderTo($err, $format);

            return $r->error->exitCode;
        }
    }

    /**
     * @param list<string> $args
     * @return array{spec: CommandSpec, path: list<string>, flags: array<string, string|bool>, args: list<string>, token: string, globals: Globals}
     * @throws GateRefusal
     */
    private function parse(array $args, string &$format): array
    {
        $group = null; // null = the root
        $spec = null;
        $path = [];
        $flags = [];
        $pos = [];
        $token = '';
        $g = ['confirm' => '', 'max-ops' => '', 'policy' => '', 'dry-run' => false];
        $endOpts = false;
        $n = \count($args);

        for ($i = 0; $i < $n; ++$i) {
            $t = $args[$i];
            if (!$endOpts && '--' === $t) {
                $endOpts = true;
                continue;
            }
            if (!$endOpts && str_starts_with($t, '-') && '-' !== $t) {
                if (!str_starts_with($t, '--')) {
                    throw self::usage("unknown shorthand flag: {$t}");
                }
                $name = substr($t, 2);
                $val = null;
                if (false !== ($eq = strpos($name, '='))) {
                    $val = substr($name, $eq + 1);
                    $name = substr($name, 0, $eq);
                }
                $def = null === $spec ? null : $spec->flag($name);
                $takesToken = null !== $spec && Gate::CONFIRM_TOKEN_FLAG === $name && Gate::requiresToken($spec->annotations);
                $isGlobal = \array_key_exists($name, $g) || 'format' === $name;
                if (null === $def && !$takesToken && !$isGlobal) {
                    throw self::usage("unknown flag: --{$name}");
                }
                $isBool = null !== $def ? !$def->takesValue() : 'dry-run' === $name;
                if ($isBool) {
                    $val = match ($val) {
                        null, 'true' => true,
                        'false' => false,
                        default => throw self::usage("invalid argument \"{$val}\" for \"--{$name}\" flag: want true or false"),
                    };
                } elseif (null === $val) {
                    if ($i + 1 >= $n) {
                        throw self::usage("flag needs an argument: --{$name}");
                    }
                    $val = $args[++$i];
                }
                if (null !== $def) {
                    $flags[$name] = $val;
                } elseif ($takesToken) {
                    $token = (string) $val;
                } elseif ('format' === $name) {
                    $format = (string) $val;
                } else {
                    $g[$name] = $val;
                }
                continue;
            }
            if (null !== $spec) {
                $pos[] = $t;
                continue;
            }
            $children = null === $group ? $this->commands : $group->children;
            $next = null;
            foreach ($children as $c) {
                if ($c->name === $t) {
                    $next = $c;
                    break;
                }
            }
            if (null === $next) {
                throw self::usage(sprintf('unknown command "%s" for "%s"', $t, implode(' ', [$this->program, ...$path])));
            }
            $path[] = $t;
            if ($next->isLeaf()) {
                $spec = $next;
            } else {
                $group = $next;
            }
        }
        if (null === $spec) {
            throw self::usage(sprintf('a command is required for "%s"', implode(' ', [$this->program, ...$path])));
        }

        // Go's flagValue: a leaf's own --confirm / --max-ops shadows the root's, set or not.
        $confirm = null !== $spec->flag('confirm') ? (string) ($flags['confirm'] ?? '') : (string) $g['confirm'];
        $maxOps = null !== $spec->flag('max-ops') ? (string) ($flags['max-ops'] ?? '') : (string) $g['max-ops'];
        if ('' !== $maxOps && 1 !== preg_match('/\A-?\d+\z/', $maxOps)) {
            throw self::usage("invalid argument \"{$maxOps}\" for \"--max-ops\" flag");
        }

        return [
            'spec' => $spec,
            'path' => $path,
            'flags' => $flags,
            'args' => $pos,
            'token' => $token,
            'globals' => new Globals($confirm, (int) $maxOps, (string) $g['policy'], (bool) $g['dry-run']),
        ];
    }

    private static function usage(string $msg): GateRefusal
    {
        return new GateRefusal(CliError::usage($msg));
    }
}
