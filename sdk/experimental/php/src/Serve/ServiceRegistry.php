<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The seam kit-owned and adopter-owned services register into. A tool
 * builds one before the root command runs; the supervisor reads it.
 */
final class ServiceRegistry
{
    /** @var array<string, Service> */
    private array $byName = [];

    /** @var list<string> */
    private array $order = [];

    /**
     * Adds $svc under its name.
     *
     * Throws on an invalid name and on a duplicate. An adopter
     * deliberately replacing a kit-shipped service calls override()
     * instead — the documented escape hatch, and the only path that
     * accepts a duplicate name.
     */
    public function register(Service $svc): void
    {
        $name = $svc->name();
        $invalid = Names::validate($name);
        if ($invalid !== null) {
            throw new ServiceRegistrationException($invalid);
        }
        if (isset($this->byName[$name])) {
            throw new ServiceRegistrationException(
                "serve: service \"{$name}\" already registered (use override to replace)",
            );
        }
        $this->byName[$name] = $svc;
        $this->order[] = $name;
    }

    /**
     * Registers $svc, replacing any service already under its name and
     * keeping that name's original position in list().
     *
     * Still throws on an invalid name: override lifts the collision
     * rule, not the grammar.
     */
    public function override(Service $svc): void
    {
        $name = $svc->name();
        $invalid = Names::validate($name);
        if ($invalid !== null) {
            throw new ServiceRegistrationException($invalid);
        }
        if (!isset($this->byName[$name])) {
            $this->order[] = $name;
        }
        $this->byName[$name] = $svc;
    }

    /** The service registered under $name, if any. */
    public function lookup(string $name): ?Service
    {
        return $this->byName[$name] ?? null;
    }

    /**
     * Every registered identifier, in registration order.
     *
     * @return list<string>
     */
    public function names(): array
    {
        return $this->order;
    }

    /**
     * Every registered service, in registration order.
     *
     * @return list<Service>
     */
    public function list(): array
    {
        return array_map(
            fn (string $n): Service => $this->byName[$n],
            $this->order,
        );
    }

    /** Number of registered services. */
    public function size(): int
    {
        return count($this->byName);
    }
}
