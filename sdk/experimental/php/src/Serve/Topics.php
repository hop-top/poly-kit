<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The six lifecycle transitions, as the 4-segment past-tense topic
 * strings the contract fixes.
 *
 * These strings are contract for a port that publishes at all: "a
 * subscriber is written against the string and does not know which
 * language published it". This SDK has no event bus, so nothing here
 * publishes today — the transitions reach an operator through the log
 * sink instead, which the contract explicitly accepts. The vocabulary
 * is still defined here, in one place, because the log carries the
 * same object and action words, and because the day this SDK grows a
 * bus the topics must already be the right strings rather than
 * something invented then.
 */
final class Topics
{
    /** The 2-segment source.category prefix serve events publish under. */
    public const string DEFAULT_PREFIX = 'kit.serve';

    /**
     * Action segments. The action is `ready_reported`, not a bare
     * `ready`: the bare form fails the past-tense validation in
     * event-topics.md, so a port emitting it would publish a topic Go
     * subscribers reject.
     */
    public const string ACTION_STARTED = 'started';
    public const string ACTION_READY_REPORTED = 'ready_reported';
    public const string ACTION_FAILED = 'failed';
    public const string ACTION_STOPPED = 'stopped';

    /**
     * Object segments. The service identifier travels in the payload,
     * not the topic, so subscribers are not forced to re-bind when a
     * tool gains a service.
     */
    public const string OBJECT_SERVICE = 'service';
    public const string OBJECT_SUPERVISOR = 'supervisor';

    /**
     * The conformant topic set for $prefix, keyed `<object>.<action>`.
     *
     * @return array<string, string>
     */
    public static function all(string $prefix = self::DEFAULT_PREFIX): array
    {
        $p = $prefix === '' ? self::DEFAULT_PREFIX : $prefix;
        $out = [];
        foreach (self::pairs() as [$object, $action]) {
            $out["{$object}.{$action}"] = "{$p}.{$object}.{$action}";
        }
        return $out;
    }

    /**
     * The six transitions as object/action pairs, in the order the
     * contract's table lists them.
     *
     * @return list<array{0: string, 1: string}>
     */
    public static function pairs(): array
    {
        return [
            [self::OBJECT_SERVICE, self::ACTION_STARTED],
            [self::OBJECT_SERVICE, self::ACTION_READY_REPORTED],
            [self::OBJECT_SERVICE, self::ACTION_FAILED],
            [self::OBJECT_SERVICE, self::ACTION_STOPPED],
            [self::OBJECT_SUPERVISOR, self::ACTION_READY_REPORTED],
            [self::OBJECT_SUPERVISOR, self::ACTION_STOPPED],
        ];
    }
}
