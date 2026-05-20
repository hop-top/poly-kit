<?php

declare(strict_types=1);

namespace HopTop\Kit\Id;

/**
 * Thrown by `Typed::parse()` when the parsed prefix doesn't match the
 * expected prefix declared on the typed wrapper subclass.
 *
 * Example: `TaskId::parse('invoice_01j…')` raises this because the
 * `TaskId` wrapper declared `PREFIX = 'task'`.
 */
final class PrefixMismatchException extends IdException {}
