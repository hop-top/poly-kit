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
 *
 * Construction invariant: every instance holds a string that (a) parses
 * as a valid canonical TypeID AND (b) carries this subclass's declared
 * prefix. The constructor enforces both, so `new TaskId($s)` cannot
 * silently wrap garbage — it will throw the appropriate Id exception.
 */
abstract class Typed implements JsonSerializable, Stringable
{
    /** Override on each subclass with the entity-type prefix (e.g. 'task'). */
    protected const string PREFIX = '';

    /**
     * Construct a typed wrapper from a canonical TypeID string.
     *
     * Validates that $value is a parseable TypeID AND that its prefix
     * matches this class's `PREFIX` constant. Callers preferring named
     * constructors can use `::parse()` / `::fromString()` — both share the
     * same validation path.
     *
     * @throws InvalidPrefixException
     * @throws InvalidSuffixException
     * @throws PrefixMismatchException If parsed prefix differs from static::PREFIX.
     */
    final public function __construct(
        public readonly string $value,
    ) {
        $parsed = Id::parse($value);
        if ($parsed->prefix !== static::PREFIX) {
            throw new PrefixMismatchException(sprintf(
                'expected prefix "%s", got "%s" in "%s"',
                static::PREFIX,
                $parsed->prefix,
                $value,
            ));
        }
    }

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
     * `PREFIX` constant. Equivalent to invoking the constructor directly,
     * kept as a named constructor for call-site readability.
     *
     * @return static
     *
     * @throws InvalidPrefixException
     * @throws InvalidSuffixException
     * @throws PrefixMismatchException If parsed prefix differs from static::PREFIX.
     */
    public static function parse(string $s): static
    {
        return new static($s);
    }

    /**
     * Alias for `parse()` — matches the naming used by the kit-go binding
     * (`TypedID.FromString`) for cross-language consistency.
     *
     * @return static
     *
     * @throws InvalidPrefixException
     * @throws InvalidSuffixException
     * @throws PrefixMismatchException
     */
    public static function fromString(string $s): static
    {
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
