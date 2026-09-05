<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * Orders a selected set for starting.
 *
 * The contract puts topological ordering under "not required" — but
 * only for a port whose seam has no dependency declaration at all:
 * "Ordering is required *where a port supports dependency
 * declarations at all*." This port does support them
 * (DeclaresDependencies), so the ordering rule applies here.
 */
final class StartOrder
{
    /**
     * $selected in topological order over the optional dependsOn
     * declarations, ties broken by the order in $selected (which the
     * Resolver returns in registration order).
     *
     * A dependency naming a service outside $selected is ignored
     * rather than an error: under the selector form exactly one
     * service runs, and its dependencies are the operator's business,
     * not a reason to refuse a deliberate single-service start.
     *
     * A dependency cycle throws, in the same class as a name
     * collision: it is a wiring bug that can only be fixed by editing
     * the registrations, and there is no order the supervisor could
     * pick that would be right.
     *
     * @param list<string> $selected
     * @return list<string>
     */
    public static function of(ServiceRegistry $registry, array $selected): array
    {
        $inSet = array_flip($selected);
        $deps = [];
        foreach ($selected as $name) {
            $svc = $registry->lookup($name);
            if (!$svc instanceof DeclaresDependencies) {
                continue;
            }
            $want = array_values(array_filter(
                $svc->dependsOn(),
                static fn (string $d): bool => isset($inSet[$d]) && $d !== $name,
            ));
            if ($want !== []) {
                $deps[$name] = $want;
            }
        }

        $mark = [];
        $out = [];
        foreach ($selected as $name) {
            self::visit($name, $deps, $mark, $out, []);
        }
        return $out;
    }

    /**
     * @param array<string, list<string>> $deps
     * @param array<string, int> $mark 1 = in progress, 2 = done.
     * @param list<string> $out
     * @param list<string> $path
     */
    private static function visit(
        string $name,
        array $deps,
        array &$mark,
        array &$out,
        array $path,
    ): void {
        $state = $mark[$name] ?? 0;
        if ($state === 2) {
            return;
        }
        if ($state === 1) {
            $cycle = implode(' -> ', [...$path, $name]);
            throw new ServiceRegistrationException("serve: dependency cycle: {$cycle}");
        }
        $mark[$name] = 1;
        foreach ($deps[$name] ?? [] as $want) {
            self::visit($want, $deps, $mark, $out, [...$path, $name]);
        }
        $mark[$name] = 2;
        $out[] = $name;
    }
}
