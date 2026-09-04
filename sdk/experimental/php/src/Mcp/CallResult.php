<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Builds the `tools/call` result envelope shared by both eras.
 *
 * Ports Go's `renderCallResult` / `errorResultBlock`. Keeping one builder
 * is what guarantees the content-block layout cannot drift between eras.
 */
final class CallResult
{
    /**
     * The content list always carries the stdout block — even when stdout
     * is empty — with stderr and structured data appended only when
     * present.
     *
     * @return array<string, mixed>
     */
    public static function render(Result $result): array
    {
        $content = [
            ['type' => 'text', 'text' => $result->stdout],
        ];

        if ('' !== $result->stderr) {
            $content[] = ['type' => 'text', 'text' => '[stderr] '.$result->stderr];
        }

        if (null !== $result->data) {
            // The serialized form doubles as the spec-recommended fallback
            // for clients that ignore structuredContent.
            $content[] = ['type' => 'text', 'text' => Json::encode($result->data)];
        }

        return [
            'content' => $content,
            'isError' => 0 !== $result->exitCode,
        ];
    }

    /**
     * A single-block result flagged `isError`.
     *
     * Refusals (blocked destructive calls, missing auth) travel this way
     * rather than as transport errors.
     *
     * @return array<string, mixed>
     */
    public static function errorBlock(string $message): array
    {
        return [
            'content' => [['type' => 'text', 'text' => $message]],
            'isError' => true,
        ];
    }
}
