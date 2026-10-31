<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

use HopTop\Kit\Cli\SideEffect;

/** Per-invocation enforcement state; not shared across invocations. */
final class Engine
{
    public readonly Policy $policy;

    public readonly int $maxOps;

    private int $opsCount = 0;

    public function __construct(?Policy $policy = null, int $maxOpsOverride = 0)
    {
        $this->policy = $policy ?? new Policy();
        $this->maxOps = $maxOpsOverride > 0 ? $maxOpsOverride : $this->policy->maxOps;
    }

    public function authorize(CommandRef $cmd): Verdict
    {
        $se = $cmd->sideEffect;
        if (null === $se && '' !== $this->policy->unannotated) {
            $se = $this->policy->unannotated;
        }
        if (null === $se || 'read' === $se) {
            return new Verdict(true);
        }
        if (!SideEffect::isValidTier($se)) {
            // Fail closed on a tier outside kit's vocabulary.
            return $this->refuse("policy: unknown side-effect \"{$se}\" for {$cmd->verb}");
        }
        $patterns = $this->lookupClass($se);
        if (null !== $patterns && !Glob::matchAny($patterns, $cmd->verb)) {
            return $this->refuse("policy: {$se} not allowed for {$cmd->verb}");
        }

        return new Verdict(true, Glob::matchAny($this->policy->requireConfirm, $cmd->verb));
    }

    /** Account one successful mutating operation; throws past the budget. */
    public function recordOp(): void
    {
        ++$this->opsCount;
        if ($this->maxOps > 0 && $this->opsCount > $this->maxOps) {
            throw new MaxOpsExceeded();
        }
    }

    private function refuse(string $reason): Verdict
    {
        if ('' !== $this->policy->remedy) {
            $reason = "{$reason} (policy {$this->policy->name}: {$this->policy->remedy})";
        }

        return new Verdict(false, false, $reason);
    }

    /** @return list<string>|null */
    private function lookupClass(string $se): ?array
    {
        if (\array_key_exists($se, $this->policy->allow)) {
            return $this->policy->allow[$se];
        }
        $cls = SideEffect::classOf($se);
        if (null !== $cls && $cls !== $se) {
            return $this->policy->allow[$cls] ?? null;
        }

        return null;
    }
}
