<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

/** A child PHP process running one of the helper scripts in this directory. */
final class Proc
{
    /** @var resource */
    private $proc;

    /** @var array<int, resource> */
    private array $pipes = [];

    /** @param list<string> $args */
    public function __construct(string $script, array $args = [])
    {
        $p = proc_open([PHP_BINARY, __DIR__ . "/$script", ...$args], [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $this->pipes);
        if (!is_resource($p)) {
            throw new \RuntimeException("cannot start $script");
        }
        $this->proc = $p;
    }

    /** @return resource */
    public function stdin()
    {
        return $this->pipes[0];
    }

    /** Blocks for one line of stdout. */
    public function readLine(): string
    {
        return trim((string) fgets($this->pipes[1]));
    }

    /** Closes stdin, waits, and returns [exit code, stdout + stderr]. @return array{int, string} */
    public function finish(): array
    {
        if (is_resource($this->pipes[0])) {
            fclose($this->pipes[0]);
        }
        $out = (string) stream_get_contents($this->pipes[1]) . (string) stream_get_contents($this->pipes[2]);
        fclose($this->pipes[1]);
        fclose($this->pipes[2]);

        return [proc_close($this->proc), $out];
    }
}
