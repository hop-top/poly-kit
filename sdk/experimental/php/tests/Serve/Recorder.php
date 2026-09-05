<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

/** Shared order-of-events log, so stop order can be asserted exactly. */
final class Recorder
{
    /** @var list<string> */
    public array $events = [];

    public function record(string $verb, string $service): void
    {
        $this->events[] = "{$verb}:{$service}";
    }

    /**
     * The services named by $verb, in the order it happened.
     *
     * @return list<string>
     */
    public function order(string $verb): array
    {
        $out = [];
        foreach ($this->events as $e) {
            if (str_starts_with($e, "{$verb}:")) {
                $out[] = substr($e, strlen($verb) + 1);
            }
        }
        return $out;
    }
}
