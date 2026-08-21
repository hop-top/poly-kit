<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\FlagSpec;
use HopTop\Kit\Mcp\Result;

/**
 * The command trees the cross-language wire fixtures were generated from.
 *
 * The legacy and modern fixture cases run against *different* trees — the
 * modern one declares fewer flags on `widget add` — so the two are kept
 * separate here rather than folded into one shared tree.
 */
final class LockTrees
{
    /** The tree behind the ten `legacy/*` fixture cases. */
    public static function legacy(): Command
    {
        $add = new Command(
            name: 'add',
            description: 'Add a widget',
            flags: [
                new FlagSpec('name', 'string', 'widget name', required: true),
                new FlagSpec('count', 'int', 'widget count'),
                new FlagSpec('force', 'bool', 'force flag'),
                new FlagSpec('tag', 'stringSlice', 'tag list'),
                new FlagSpec('hidden-flag', 'string', 'should be hidden', hidden: true),
                new FlagSpec('deprecated-flag', 'string', 'should be dropped', deprecated: true),
            ],
            annotations: ['kit/side-effect' => 'write'],
            runner: static fn (): Result => new Result(stdout: "added\n"),
        );

        return self::tree($add);
    }

    /** The tree behind the seven `modern/*` fixture cases. */
    public static function modern(): Command
    {
        $add = new Command(
            name: 'add',
            description: 'Add a widget',
            flags: [
                new FlagSpec('name', 'string', 'widget name', required: true),
                new FlagSpec('count', 'int', 'widget count'),
            ],
            annotations: ['kit/side-effect' => 'write'],
            runner: static fn (): Result => new Result(stdout: "added\n"),
        );

        return self::tree($add);
    }

    /** Everything except `widget add` is identical between the two trees. */
    private static function tree(Command $add): Command
    {
        $delete = new Command(
            name: 'delete',
            description: 'Delete a widget',
            annotations: ['kit/side-effect' => 'destructive'],
            runner: static fn (): Result => new Result(stdout: "deleted\n"),
        );

        $widget = (new Command(name: 'widget'))->addCommand($add, $delete);

        $secret = new Command(
            name: 'secret',
            description: 'Locked',
            annotations: ['kit/auth-required' => 'true'],
            runner: static fn (): Result => new Result(),
        );

        $deploy = new Command(
            name: 'deploy',
            description: 'Deploy',
            annotations: ['kit/requires-confirmation' => 'true'],
            runner: static fn (): Result => new Result(),
        );

        $ping = new Command(
            name: 'ping',
            description: 'Ping the server',
            annotations: ['kit/side-effect' => 'read'],
            runner: static fn (): Result => new Result(stdout: "pong\n"),
        );

        return (new Command(name: 'root'))->addCommand($widget, $secret, $deploy, $ping);
    }
}
