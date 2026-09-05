<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * Surfaces one lifecycle transition.
 *
 * With no event bus in this SDK the log is the only sink, and the
 * contract's conditional lands squarely on the log branch: the six
 * transitions are carried with the fixed object/action vocabulary and
 * the fixed payload keys — `service`, `error`, `address` — as
 * structured fields, at INFO for started/ready_reported/stopped and
 * ERROR for failed.
 *
 * Every emitted record also carries the `topic` it *would* publish
 * under. That costs one field and means an operator grepping a PHP
 * tool's stderr matches the same string a subscriber binds to on a Go
 * tool's bus, which is the cross-language legibility the contract is
 * actually asking for.
 */
final class Emitter
{
    /** @var array<string, string> */
    private readonly array $topics;

    public function __construct(
        private readonly ServeLogger $log,
        string $prefix = Topics::DEFAULT_PREFIX,
    ) {
        $this->topics = Topics::all($prefix);
    }

    /**
     * Surfaces $object.$action.
     *
     * `elapsed_ms` is carried because the contract says a port SHOULD
     * where it is cheap; nothing downstream is specified to read it.
     *
     * @param array<string, scalar|null> $payload
     */
    public function emit(string $object, string $action, array $payload = []): void
    {
        $fields = ['topic' => $this->topics["{$object}.{$action}"] ?? ''] + $payload;

        if ($action === Topics::ACTION_FAILED) {
            $this->log->error("serve: {$action}", $fields);
            return;
        }
        $this->log->info("serve: {$action}", $fields);
    }
}
