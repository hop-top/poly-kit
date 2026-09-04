<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Surfaces a command leaf can be invoked through.
 *
 * Ports Go's `Surface` (go/transport/cmdsurface). `Cli` and `Lib` are
 * the local-runtime surfaces the policy gate always trusts; everything
 * else is remote and subject to the destructive ceiling.
 */
enum Surface: string
{
    case Cli = 'cli';
    case Lib = 'lib';
    case Mcp = 'mcp';
}
