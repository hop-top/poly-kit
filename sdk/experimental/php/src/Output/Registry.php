<?php

declare(strict_types=1);

namespace HopTop\Kit\Output;

use HopTop\Kit\Output\Formatter\Formatter;
use InvalidArgumentException;

/**
 * Holds Formatter implementations keyed by Formatter::key().
 *
 * register() throws on duplicate key (PHP idiom for "this argument can't
 * be accepted in current state"); adopters intentionally replacing a
 * built-in must call override().
 *
 * Mirrors py Registry, ts Registry, go *Registry. Includes a static
 * default() accessor that returns the process-wide singleton — built-in
 * formatters register against it at bootstrap time.
 */
final class Registry
{
    /** @var array<string,Formatter> */
    private array $byKey = [];

    private static ?Registry $default = null;

    public static function default(): Registry
    {
        if (self::$default === null) {
            self::$default = new Registry();
            Builtins::register(self::$default);
        }
        return self::$default;
    }

    /**
     * Replace the process-wide default registry — tests use this to start
     * from a clean slate then restore. Returns the previous default so
     * callers can restore without leaking state.
     */
    public static function setDefault(Registry $r): Registry
    {
        $prev = self::$default ?? new Registry();
        self::$default = $r;
        return $prev;
    }

    public function register(Formatter $f): void
    {
        $this->validate($f);
        $key = $f->key();
        if (isset($this->byKey[$key])) {
            throw new InvalidArgumentException(sprintf(
                "output: formatter '%s' already registered (use override to replace)",
                $key,
            ));
        }
        $this->byKey[$key] = $f;
    }

    public function override(Formatter $f): void
    {
        $this->validate($f);
        $this->byKey[$f->key()] = $f;
    }

    public function lookup(string $key): ?Formatter
    {
        return $this->byKey[$key] ?? null;
    }

    /**
     * @return list<string> registered keys, sorted for stable output
     */
    public function keys(): array
    {
        $keys = array_keys($this->byKey);
        sort($keys);
        return array_values($keys);
    }

    /**
     * @return list<Formatter> formatters in key order
     */
    public function formatters(): array
    {
        $keys = $this->keys();
        return array_values(array_map(fn (string $k) => $this->byKey[$k], $keys));
    }

    /**
     * @return array<string,string> ext (lowercase, no leading dot) → formatter key
     */
    public function extensionMap(): array
    {
        $out = [];
        foreach ($this->keys() as $k) {
            foreach ($this->byKey[$k]->extensions() as $ext) {
                $out[strtolower(ltrim($ext, '.'))] = $k;
            }
        }
        return $out;
    }

    private function validate(Formatter $f): void
    {
        if ($f->key() === '') {
            throw new InvalidArgumentException('output: formatter key is empty');
        }
    }
}
