<?php

declare(strict_types=1);

namespace HopTop\Kit\Id;

use JsonSerializable;
use Stringable;

/**
 * Abstract base for prefix-bound TypeID wrappers.
 *
 * PHP has no generics, so kit ships an abstract class that subclasses
 * specialise by declaring a `PREFIX` constant. This buys compile-time
 * (well, parse-time) safety: a function typed as `TaskId` cannot accept
 * an `InvoiceId`, even though both wrap a plain string under the hood.
 *
 *   final class TaskId extends Typed {
 *       protected const string PREFIX = 'task';
 *   }
 *
 *   $t = TaskId::generate();                        // task_01j…
 *   $t2 = TaskId::parse('task_01j…');               // OK
 *   TaskId::parse('invoice_01j…');                  // PrefixMismatchException
 *
 * The class implements `JsonSerializable` and `Stringable` so the wire
 * form is the bare canonical TypeID string, matching the ADR.
 */
abstract class Typed implements JsonSerializable, Stringable
{
    /** Override on each subclass with the entity-type prefix (e.g. 'task'). */
    protected const string PREFIX = '';

    final public function __construct(
        public readonly string $value,
    ) {}

    /**
     * Generate a fresh typed Id.
     *
     * @return static
     */
    public static function generate(): static
    {
        return new static(Id::new(static::PREFIX));
    }

    /**
     * Parse a canonical TypeID string into this typed wrapper.
     *
     * Validates spec conformance AND that the prefix matches this class's
     * `PREFIX` constant.
     *
     * @return static
     *
     * @throws InvalidPrefixException
     * @throws InvalidSuffixException
     * @throws PrefixMismatchException If parsed prefix differs from static::PREFIX.
     */
    public static function parse(string $s): static
    {
        $parsed = Id::parse($s);
        if ($parsed->prefix !== static::PREFIX) {
            throw new PrefixMismatchException(sprintf(
                'expected prefix "%s", got "%s" in "%s"',
                static::PREFIX,
                $parsed->prefix,
                $s,
            ));
        }

        return new static($s);
    }

    public function __toString(): string
    {
        return $this->value;
    }

    public function jsonSerialize(): string
    {
        return $this->value;
    }
}
