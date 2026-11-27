<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use stdClass;
use Throwable;

/**
 * Migrates one JSON document in memory: no I/O.
 *
 * The document is an object with an integer version at
 * `DocOptions::$versionField`. Steps are `DocStep`s; the chain picks the
 * step whose `from` is the current version, wherever it was given. Every
 * step must have `to > from` and `to <= latest`, checked before any runs.
 *
 * A document already at the latest version comes back as the input text;
 * otherwise the result is re-encoded with object keys sorted. On error an
 * exception is thrown and the input is untouched.
 *
 * Steps see a document as nested arrays; an empty JSON object inside it is an
 * empty `stdClass` so it survives the round trip as `{}`.
 */
final class DocumentMigrator
{
    private const ENCODE = JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_PRESERVE_ZERO_FRACTION;

    /**
     * @param list<DocStep> $steps
     * @throws UpgradeException
     */
    public static function migrate(string $json, array $steps, DocOptions $opts = new DocOptions()): DocResult
    {
        [$out, $report] = self::run(self::decode($json), $steps, $opts);
        if ($report->applied === []) {
            return new DocResult($json, $report);
        }

        return new DocResult(self::encode($out), $report);
    }

    /**
     * Like `migrate` on a decoded document. The caller's array is never
     * modified: steps work on a deep copy taken through JSON.
     *
     * @param array<mixed> $doc
     * @param list<DocStep> $steps
     * @throws UpgradeException
     */
    public static function migrateArray(array $doc, array $steps, DocOptions $opts = new DocOptions()): DocMapResult
    {
        try {
            $raw = json_encode($doc, self::ENCODE);
        } catch (\JsonException $e) {
            throw new DocInvalidException("copy document: {$e->getMessage()}", 0, $e);
        }
        $m = self::decode($raw);
        [$out, $report] = self::run($m, $steps, $opts);

        return new DocMapResult($out, $report);
    }

    /** @return array<mixed> */
    private static function decode(string $json): array
    {
        try {
            $v = json_decode($json, false, 512, JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            throw new DocInvalidException("parse document: {$e->getMessage()}", 0, $e);
        }
        if (!$v instanceof stdClass) {
            throw new DocInvalidException('document is not a JSON object');
        }

        return self::toArray($v);
    }

    /** @return array<mixed> */
    private static function toArray(stdClass $o): array
    {
        $out = [];
        foreach ((array) $o as $k => $v) {
            $out[$k] = self::fromDecoded($v);
        }

        return $out;
    }

    private static function fromDecoded(mixed $v): mixed
    {
        if ($v instanceof stdClass) {
            return (array) $v === [] ? new stdClass() : self::toArray($v);
        }
        if (is_array($v)) {
            return array_map(self::fromDecoded(...), $v);
        }

        return $v;
    }

    /** @param array<mixed> $doc */
    private static function encode(array $doc): string
    {
        return json_encode(self::sorted($doc), self::ENCODE);
    }

    private static function sorted(mixed $v): mixed
    {
        if (!is_array($v)) {
            return $v;
        }
        $v = array_map(self::sorted(...), $v);
        if (!array_is_list($v)) {
            ksort($v, SORT_STRING);
        }

        return $v;
    }

    private static function isMap(mixed $v): bool
    {
        return $v instanceof stdClass || (is_array($v) && ($v === [] || !array_is_list($v)));
    }

    /** @return array<mixed> */
    private static function asMap(mixed $v): array
    {
        return is_array($v) ? $v : [];
    }

    /** @param array<mixed> $doc */
    private static function readVersion(array $doc, string $path): ?int
    {
        $cur = $doc;
        foreach (explode('.', $path) as $seg) {
            if (!is_array($cur) || !self::isMap($cur) || !array_key_exists($seg, $cur)) {
                return null;
            }
            $cur = $cur[$seg];
        }

        return is_int($cur) ? $cur : null;
    }

    /**
     * @param array<mixed> $doc
     * @return array<mixed>
     */
    private static function writeVersion(array $doc, string $path, int $v): array
    {
        $segs = explode('.', $path);
        $last = array_pop($segs);
        $cur = &$doc;
        foreach ($segs as $seg) {
            $next = $cur[$seg] ?? null;
            $cur[$seg] = self::isMap($next) ? self::asMap($next) : [];
            $cur = &$cur[$seg];
        }
        $cur[$last] = $v;
        unset($cur);

        return $doc;
    }

    /**
     * @param array<mixed> $doc
     * @param list<DocStep> $steps
     * @return array{0: array<mixed>, 1: DocReport}
     */
    private static function run(array $doc, array $steps, DocOptions $opts): array
    {
        $field = $opts->versionField === '' ? 'version' : $opts->versionField;
        $cur = self::readVersion($doc, $field);
        if ($cur === null) {
            if (!$opts->missingIsZero) {
                throw new DocVersionException(sprintf('document has no integer version at "%s"', $field));
            }
            $cur = 0;
        }
        $latest = $opts->latest;
        if ($latest === 0) {
            foreach ($steps as $s) {
                $latest = max($latest, $s->to);
            }
        }
        foreach ($steps as $s) {
            if ($s->to <= $s->from) {
                throw new DocInvalidException(sprintf('migration %d -> %d does not move forward', $s->from, $s->to));
            }
            if ($s->to > $latest) {
                throw new DocInvalidException(sprintf('migration %d -> %d goes past the latest known %d', $s->from, $s->to, $latest));
            }
        }
        if ($cur > $latest) {
            throw new DocNewerException(sprintf('document version %d is newer than the latest known %d', $cur, $latest));
        }
        $from = $cur;
        $applied = [];
        while ($cur < $latest) {
            $step = null;
            foreach ($steps as $s) {
                if ($s->from === $cur) {
                    $step = $s;
                    break;
                }
            }
            if ($step === null) {
                throw new DocGapException(sprintf('no migration from version %d (highest target is %d); chain has a gap', $cur, $latest));
            }
            $id = sprintf('migration %d -> %d', $step->from, $step->to);
            try {
                $out = ($step->apply)($doc);
            } catch (Throwable $e) {
                throw new DocApplyException("$id: {$e->getMessage()}", 0, $e);
            }
            if (!is_array($out)) {
                throw new DocApplyException("$id returned nil map");
            }
            if ($step->verify !== null) {
                try {
                    ($step->verify)($out);
                } catch (Throwable $e) {
                    throw new VerifyException("$id: verify: {$e->getMessage()}", 0, $e);
                }
            }
            $doc = self::writeVersion($out, $field, $step->to);
            $cur = $step->to;
            $applied[] = new DocApplied($step->from, $step->to, $step->note);
        }

        return [$doc, new DocReport($from, $cur, $applied)];
    }
}
