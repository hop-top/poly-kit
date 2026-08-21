<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/** Which handler serves a request. */
enum Era
{
    case Legacy;
    case Modern;
}
