<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli;

/** What `--dry-run` does on a leaf; see {@see SideEffect::resolveDryRun()}. */
enum DryRunPolicy
{
    case Allow;
    case Noop;
    case RejectInteractive;
    case RejectOptOut;
    case RejectUntagged;
    case RejectMalformed;
}
