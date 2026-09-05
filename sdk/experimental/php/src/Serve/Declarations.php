<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The optional declarations a registration MAY carry.
 *
 * The contract requires these as *concepts* a registration may carry,
 * and requires the described effects where a service does carry one;
 * how a port lets a registration opt in is not contract. Go uses
 * optional interfaces, TypeScript optional properties. PHP takes Go's
 * shape here rather than TS's, because an optional method on an
 * interface is not expressible in PHP: a separate interface per
 * declaration is the language's own way of saying "may carry".
 */
interface ValidatesConfig
{
    /**
     * The second of the three validation gates. Returns an error
     * message, or null when the resolved configuration is complete and
     * usable.
     */
    public function validateConfig(): ?string;
}

/**
 * Ordering declaration. Start order is topological over these, ties
 * broken by registration order; stop order is the exact reverse of the
 * order services actually started.
 */
interface DeclaresDependencies
{
    /** @return list<string> */
    public function dependsOn(): array;
}

/**
 * Address declaration. Read once the service reports ready and carried
 * into the readiness event under `address`, so an operator learns
 * where the service actually bound — including a port the kernel
 * picked for a wildcard address, which configuration cannot reveal.
 */
interface DeclaresAddress
{
    public function addr(): string;
}

/**
 * Policy declaration: the `kit/side-effect` and `kit/network` classes.
 * A service that does not implement this is unclassified and passes
 * the policy gate.
 */
interface DeclaresClass
{
    /** @return array{0: string, 1: string} side effect, then network. */
    public function serviceClass(): array;
}
