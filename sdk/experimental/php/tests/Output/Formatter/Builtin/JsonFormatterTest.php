<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use PHPUnit\Framework\TestCase;

class JsonFormatterTest extends TestCase
{
    private function render(mixed $data, array $opts = [], array $cols = []): string
    {
        $f = new JsonFormatter();
        $resolved = array_merge(['indent' => 2], $opts);
        $w = fopen('php://memory', 'w+b');
        $f->render($w, $data, $resolved, $cols);
        rewind($w);
        return stream_get_contents($w) ?: '';
    }

    public function testRendersList(): void
    {
        $out = $this->render([
            ['name' => 'alpha', 'count' => 1],
            ['name' => 'beta', 'count' => 2],
        ]);
        $this->assertJsonStringEqualsJsonString(
            '[{"name":"alpha","count":1},{"name":"beta","count":2}]',
            $out,
        );
    }

    public function testRendersSingleRow(): void
    {
        $out = $this->render(['name' => 'alpha', 'count' => 1]);
        $this->assertJsonStringEqualsJsonString('{"name":"alpha","count":1}', $out);
    }

    public function testIndentZeroCompacts(): void
    {
        $out = $this->render(['a' => 1, 'b' => 2], ['indent' => 0]);
        $this->assertSame("{\"a\":1,\"b\":2}\n", $out);
    }

    public function testIndentFourMatchesPhpDefault(): void
    {
        $out = $this->render(['a' => 1], ['indent' => 4]);
        $this->assertStringContainsString("\n    \"a\": 1\n", $out);
    }

    public function testColsProjectsKeys(): void
    {
        $out = $this->render(
            [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
            cols: ['name', 'status'],
        );
        $decoded = json_decode($out, true);
        $this->assertSame([['name' => 'alpha', 'status' => 'ok']], $decoded);
    }
}
