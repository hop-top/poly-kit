<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/** The outcome of verifying a presented `requestState`. */
enum ConfirmationStatus
{
    case Valid;
    case Expired;
    case Invalid;
}
