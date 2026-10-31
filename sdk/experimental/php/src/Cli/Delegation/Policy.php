<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

use HopTop\Kit\Cli\SideEffect;
use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;

/**
 * The delegation-safety policy: which side-effect classes may run, how many
 * mutating operations one invocation may perform, which commands always ask
 * for confirmation. Ports Go `console/cli/policy`.
 *
 * Not evaluated: `permissions:` rules (carried and shape-checked; the
 * expression evaluator has no PHP counterpart) and `callers:` rules.
 */
final class Policy
{
    public const KIT_DEFAULT = 'kit-default';

    /**
     * @param array<string, list<string>> $allow
     * @param list<string> $requireConfirm
     * @param list<array<string, mixed>> $permissions
     * @param list<CallerRule> $callers
     */
    public function __construct(
        public string $name = '',
        public array $allow = [],
        public int $maxOps = 0,
        public array $requireConfirm = [],
        public array $permissions = [],
        public string $unannotated = '',
        public array $callers = [],
        public string $remedy = '',
    ) {
    }

    /** The policy kit ships: reads only for a caller nobody established. */
    public static function kitDefault(): self
    {
        return new self(
            name: self::KIT_DEFAULT,
            allow: ['write' => [], 'destructive' => []],
            unannotated: 'write',
            callers: [new CallerRule(
                allow: ['write' => ['*'], 'destructive' => ['*']],
                requireDeclaredScope: ['destructive'],
            )],
            remedy: "kit's default beyond loopback: writes need an authenticated caller, "
                . 'destructive commands a declared kit/permissions scope; name a --policy '
                . 'to choose otherwise',
        );
    }

    /** Parse and validate YAML policy text. */
    public static function parse(string $text): self
    {
        try {
            $raw = Yaml::parse($text);
        } catch (ParseException $e) {
            throw new PolicyException(str_replace("\n", ' ', $e->getMessage()), 0, $e);
        }
        if (null === $raw) {
            $raw = [];
        }
        if (!\is_array($raw) || ([] !== $raw && array_is_list($raw))) {
            throw new PolicyException('policy document must be a mapping');
        }
        try {
            $p = new self(
                name: self::str($raw['name'] ?? ''),
                allow: self::classMap($raw['allow'] ?? null),
                maxOps: self::int($raw['max_ops'] ?? 0),
                requireConfirm: self::strs($raw['require_confirm'] ?? null),
                permissions: self::rules($raw['permissions'] ?? null),
                unannotated: self::str($raw['unannotated'] ?? ''),
                callers: array_map(self::caller(...), self::rules($raw['callers'] ?? null)),
            );
        } catch (\InvalidArgumentException $e) {
            throw new PolicyException('invalid field: ' . $e->getMessage(), 0, $e);
        }
        $p->validate();

        return $p;
    }

    public function validate(): void
    {
        $seen = [];
        foreach ($this->permissions as $i => $r) {
            $name = self::str($r['name'] ?? '');
            if ('' === $name) {
                throw new PolicyException("permissions[{$i}]: name required");
            }
            if (isset($seen[$name])) {
                throw new PolicyException("permission rule \"{$name}\": duplicate name");
            }
            $seen[$name] = true;
            if (empty($r['when'])) {
                throw new PolicyException("permission rule \"{$name}\": 'when' required");
            }
            foreach (['effect', 'otherwise'] as $fld) {
                $v = self::str($r[$fld] ?? '');
                if ('' === $v) {
                    throw new PolicyException("permission rule \"{$name}\": {$fld} required (allow|deny)");
                }
                if (!\in_array($v, ['allow', 'deny'], true)) {
                    throw new PolicyException("permission rule \"{$name}\": {$fld} \"{$v}\" invalid (want allow|deny)");
                }
            }
        }
        $errs = [];
        if ('' !== $this->unannotated && null === SideEffect::classOf($this->unannotated)) {
            $errs[] = "unannotated: unknown side-effect class \"{$this->unannotated}\"";
        }
        foreach ($this->callers as $i => $r) {
            foreach (['principal' => $r->principal, 'tenant' => $r->tenant] as $fld => $pat) {
                if (null === Glob::match($pat, '')) {
                    $errs[] = "callers[{$i}]: {$fld} \"{$pat}\": syntax error in pattern";
                }
            }
            foreach (array_keys($r->allow) as $c) {
                if (null === SideEffect::classOf((string) $c)) {
                    $errs[] = "callers[{$i}]: allow: unknown side-effect class \"{$c}\"";
                }
            }
            foreach ($r->requireDeclaredScope as $c) {
                if (null === SideEffect::classOf($c)) {
                    $errs[] = "callers[{$i}]: require_declared_scope: unknown side-effect class \"{$c}\"";
                }
            }
            if ($r->maxOps < 0) {
                $errs[] = "callers[{$i}]: max_ops {$r->maxOps} is negative; 0 means no budget";
            }
        }
        if ([] !== $errs) {
            throw new PolicyException(implode("\n", $errs));
        }
    }

    private static function str(mixed $v): string
    {
        return \is_scalar($v) ? (string) $v : '';
    }

    private static function int(mixed $v): int
    {
        if (null === $v || '' === $v) {
            return 0;
        }
        if (!\is_int($v) && !(\is_string($v) && is_numeric($v))) {
            throw new \InvalidArgumentException('expected an integer');
        }

        return (int) $v;
    }

    /** @return list<string> */
    private static function strs(mixed $v): array
    {
        if (null === $v) {
            return [];
        }
        if (!\is_array($v) || !array_is_list($v)) {
            throw new \InvalidArgumentException('expected a list');
        }

        return array_map(static fn (mixed $x): string => self::str($x), $v);
    }

    /** @return array<string, list<string>> */
    private static function classMap(mixed $v): array
    {
        if (null === $v) {
            return [];
        }
        if (!\is_array($v) || ([] !== $v && array_is_list($v))) {
            throw new \InvalidArgumentException('expected a mapping');
        }
        $out = [];
        foreach ($v as $k => $list) {
            $out[(string) $k] = self::strs($list);
        }

        return $out;
    }

    /** @return list<array<string, mixed>> */
    private static function rules(mixed $v): array
    {
        if (null === $v) {
            return [];
        }
        if (!\is_array($v) || !array_is_list($v)) {
            throw new \InvalidArgumentException('expected a list');
        }
        $out = [];
        foreach ($v as $r) {
            if (!\is_array($r)) {
                throw new \InvalidArgumentException('expected a mapping');
            }
            $out[] = $r;
        }

        return $out;
    }

    /** @param array<string, mixed> $c */
    private static function caller(array $c): CallerRule
    {
        return new CallerRule(
            principal: self::str($c['principal'] ?? ''),
            tenant: self::str($c['tenant'] ?? ''),
            scope: self::str($c['scope'] ?? ''),
            allow: self::classMap($c['allow'] ?? null),
            maxOps: self::int($c['max_ops'] ?? 0),
            window: self::str($c['window'] ?? ''),
            requireDeclaredScope: self::strs($c['require_declared_scope'] ?? null),
        );
    }
}
