<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

use Closure;

/**
 * Supplies the terminal a prompt asks on; null means nobody to ask.
 * Interactivity is the terminal's availability, not isatty on stdin, and
 * the prompt never reads the command's stdin.
 */
final class PromptSource
{
    private static ?Prompter $tty = null;

    private static bool $ttyTried = false;

    /** @param Closure(): ?Prompter $fn */
    private function __construct(private readonly Closure $fn)
    {
    }

    public function get(): ?Prompter
    {
        return ($this->fn)();
    }

    public static function none(): self
    {
        return new self(static fn (): ?Prompter => null);
    }

    /** @param list<string> $answers */
    public static function fromAnswers(array $answers): self
    {
        $p = new class($answers) implements Prompter {
            /** @param list<string> $answers */
            public function __construct(private array $answers)
            {
            }

            public function ask(string $question): ?string
            {
                $a = array_shift($this->answers);

                return null === $a ? null : strtolower(trim($a));
            }
        };

        return new self(static fn (): Prompter => $p);
    }

    /** The process's `/dev/tty`, opened once; the default. */
    public static function controllingTty(): self
    {
        return new self(static function (): ?Prompter {
            if (!self::$ttyTried) {
                self::$ttyTried = true;
                self::$tty = self::openTty();
            }

            return self::$tty;
        });
    }

    private static function openTty(): ?Prompter
    {
        $rd = @fopen('/dev/tty', 'r');
        $wr = @fopen('/dev/tty', 'w');
        if (false === $rd || false === $wr || !stream_isatty($rd)) {
            return null;
        }

        return new class($rd, $wr) implements Prompter {
            /** @param resource $rd
             * @param resource $wr
             */
            public function __construct(private $rd, private $wr)
            {
            }

            public function ask(string $question): ?string
            {
                fwrite($this->wr, $question);
                fflush($this->wr);
                $line = fgets($this->rd);

                return false === $line ? null : strtolower(trim($line));
            }
        };
    }
}
