<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

/**
 * Resolves `--policy <name>` against `$XDG_CONFIG_HOME/<program>/policies/`
 * (default `~/.config`). `kit-default` is reserved and reads no file.
 */
final class PolicyLoader
{
    private function __construct()
    {
    }

    /** @return \Closure(string): Policy */
    public static function xdg(string $program): \Closure
    {
        return static fn (string $name): Policy => self::named($program, $name);
    }

    public static function named(string $program, string $name): Policy
    {
        if (Policy::KIT_DEFAULT === $name) {
            return Policy::kitDefault();
        }

        return self::load(self::resolve($program, $name));
    }

    public static function validName(string $name): bool
    {
        return 1 === preg_match('/\A[A-Za-z0-9_-]{1,64}\z/', $name);
    }

    /** `<config home>/<program>/policies/<name>.yaml`; the name is checked first. */
    public static function resolve(string $program, string $name): string
    {
        if ('' === $program) {
            throw new PolicyException('policy: tool name required for XDG resolution');
        }
        if ('' === $name) {
            throw new PolicyException('policy: policy name required');
        }
        if (!self::validName($name)) {
            throw new PolicyException("policy: invalid policy name \"{$name}\" (want [A-Za-z0-9_-]{1,64})");
        }

        return self::configHome() . "/{$program}/policies/{$name}.yaml";
    }

    public static function load(string $path): Policy
    {
        if (!is_file($path)) {
            throw new PolicyException("policy: load {$path}: no such file or directory");
        }
        $data = @file_get_contents($path);
        if (false === $data) {
            throw new PolicyException("policy: load {$path}: permission denied");
        }
        try {
            $p = Policy::parse($data);
        } catch (PolicyException $e) {
            throw new PolicyException("policy: parse {$path}: {$e->getMessage()}", 0, $e);
        }
        if ('' === $p->name) {
            $p->name = preg_replace('/\.ya?ml\z/', '', basename($path)) ?? basename($path);
        }

        return $p;
    }

    private static function configHome(): string
    {
        $home = getenv('XDG_CONFIG_HOME');
        if (false === $home || '' === $home) {
            $home = (getenv('HOME') ?: '.') . '/.config';
        }

        return $home;
    }
}
