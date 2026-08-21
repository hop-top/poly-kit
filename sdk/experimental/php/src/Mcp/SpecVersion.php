<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/** The protocol revisions a mount can serve. */
enum SpecVersion: string
{
    case Legacy = '2024-11-05';
    case Modern = '2026-07-28';
}
