<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

interface Prompter
{
    /** Write $question, read one line; lowercased, trimmed; null at EOF. */
    public function ask(string $question): ?string;
}
