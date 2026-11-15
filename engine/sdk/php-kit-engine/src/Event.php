<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

/** One `message` frame from `/events`. */
final readonly class Event
{
    public function __construct(
        public string $topic,
        public mixed $payload = null,
    ) {
    }
}
