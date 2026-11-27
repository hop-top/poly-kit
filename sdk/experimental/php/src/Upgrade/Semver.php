<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * Lenient semver, the order migrations run in.
 *
 * Parsing never fails. One leading `v` and build metadata after `+` are
 * dropped; each of major, minor and patch is the leading decimal digits of
 * its dot-separated part (missing or non-numeric is 0; anything after the
 * third dot belongs to patch and is ignored). A release sorts above its
 * pre-releases; pre-release identifiers compare left to right, a numeric
 * identifier (decimal, no leading zero) below an alphanumeric one, numbers
 * by value, text bytewise, and a shorter list of equal identifiers first.
 */
final readonly class Semver
{
    public function __construct(
        public int $major,
        public int $minor,
        public int $patch,
        public string $pre = '',
    ) {}

    public static function parse(string $version): self
    {
        $s = str_starts_with($version, 'v') ? substr($version, 1) : $version;
        $plus = strpos($s, '+');
        if ($plus !== false) {
            $s = substr($s, 0, $plus);
        }
        $pre = '';
        $dash = strpos($s, '-');
        if ($dash !== false) {
            $pre = substr($s, $dash + 1);
            $s = substr($s, 0, $dash);
        }
        $parts = explode('.', $s, 3);

        return new self(
            self::lead($parts[0]),
            self::lead($parts[1] ?? ''),
            self::lead($parts[2] ?? ''),
            $pre,
        );
    }

    /** Sign of `$a` against `$b`: -1, 0 or 1. */
    public static function compare(string $a, string $b): int
    {
        return self::parse($a)->compareTo(self::parse($b));
    }

    /** Sign of this version against `$other`: -1, 0 or 1. */
    public function compareTo(self $other): int
    {
        $d = $this->major <=> $other->major;
        if ($d === 0) {
            $d = $this->minor <=> $other->minor;
        }
        if ($d === 0) {
            $d = $this->patch <=> $other->patch;
        }
        if ($d !== 0) {
            return $d;
        }
        if ($this->pre === '' || $other->pre === '') {
            return ($this->pre === '' ? 1 : 0) <=> ($other->pre === '' ? 1 : 0);
        }

        return self::comparePre($this->pre, $other->pre);
    }

    private static function lead(string $part): int
    {
        if (preg_match('/^[0-9]+/', $part, $m) !== 1) {
            return 0;
        }

        return (int) $m[0];
    }

    private static function comparePre(string $a, string $b): int
    {
        $x = explode('.', $a);
        $y = explode('.', $b);
        $n = min(count($x), count($y));
        for ($i = 0; $i < $n; $i++) {
            $d = self::compareIdent($x[$i], $y[$i]);
            if ($d !== 0) {
                return $d;
            }
        }

        return count($x) <=> count($y);
    }

    private static function compareIdent(string $a, string $b): int
    {
        $aNum = preg_match('/^(0|[1-9][0-9]*)$/', $a) === 1;
        $bNum = preg_match('/^(0|[1-9][0-9]*)$/', $b) === 1;
        if ($aNum && $bNum) {
            // digit strings: longer is larger, same length compares bytewise
            return strlen($a) <=> strlen($b) ?: strcmp($a, $b) <=> 0;
        }
        if ($aNum) {
            return -1;
        }
        if ($bNum) {
            return 1;
        }

        return strcmp($a, $b) <=> 0;
    }
}
