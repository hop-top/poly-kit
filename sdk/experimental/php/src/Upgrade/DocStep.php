<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Closure;

/**
 * Moves a JSON document from version `from` to `to` (`to > from`).
 *
 * `apply` gets a private copy of the document as an array and returns the
 * migrated array; the version field is set to `to` afterwards, so it need
 * not touch it. Returning null or throwing fails the step (`DocApplyException`).
 * `verify`, when set, runs on `apply`'s result and signals failure by
 * throwing (`VerifyException`).
 */
final readonly class DocStep
{
    /**
     * @param Closure(array<mixed>): (array<mixed>|null) $apply
     * @param (Closure(array<mixed>): void)|null $verify
     */
    public function __construct(
        public int $from,
        public int $to,
        public Closure $apply,
        public ?Closure $verify = null,
        public string $note = '',
    ) {}
}
