<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

enum ConfirmMode: string
{
    case Auto = 'auto';
    case Yes = 'yes';
    case No = 'no';
    case Prompt = 'prompt';

    /** Parse `--confirm`; empty or unknown is `prompt` with a terminal, else `no`. */
    public static function resolve(PromptSource $src, string $raw): self
    {
        return self::tryFrom(strtolower(trim($raw))) ?? (null !== $src->get() ? self::Prompt : self::No);
    }
}
