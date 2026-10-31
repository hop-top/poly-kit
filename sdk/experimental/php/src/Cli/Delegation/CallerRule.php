<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

/**
 * A `callers:` rule. Carried and validated, not evaluated: a CLI has no
 * transport-established caller, so only the policy's own rules answer.
 */
final readonly class CallerRule
{
    /**
     * @param array<string, list<string>> $allow
     * @param list<string> $requireDeclaredScope
     */
    public function __construct(
        public string $principal = '',
        public string $tenant = '',
        public string $scope = '',
        public array $allow = [],
        public int $maxOps = 0,
        public string $window = '',
        public array $requireDeclaredScope = [],
    ) {
    }
}
