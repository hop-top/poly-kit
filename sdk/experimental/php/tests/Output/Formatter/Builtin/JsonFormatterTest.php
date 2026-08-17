<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use PHPUnit\Framework\TestCase;

class JsonFormatterTest extends TestCase
{
    private function render(mixed $data, array $opts = [], array $cols = [], array $columns = []): string
    {
        $f = new JsonFormatter();
        $resolved = array_merge(['indent' => 2], $opts);
        $w = fopen('php://memory', 'w+b');
        $f->render($w, $data, $resolved, $cols, $columns);
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
        $this->assertSame(['name', 'status'], array_keys($decoded[0]));
    }

    /**
     * Rule 1: ColumnSpec order drives the serialized key order. Payload key
     * order (count, name, status) disagrees with spec order deliberately.
     */
    public function testColumnSpecOrderDrivesSerializedKeyOrder(): void
    {
        $out = $this->render(
            [['count' => 1, 'name' => 'alpha', 'status' => 'ok']],
            columns: [
                ColumnSpec::of('status', 'status'),
                ColumnSpec::of('name', 'name'),
                ColumnSpec::of('count', 'count'),
            ],
        );
        $decoded = json_decode($out, true);
        $this->assertSame(['status', 'name', 'count'], array_keys($decoded[0]));
        $this->assertSame([['status' => 'ok', 'name' => 'alpha', 'count' => 1]], $decoded);
    }

    /**
     * Rule 1: ColumnSpec selects as well as orders — unspec'd keys drop.
     */
    public function testColumnSpecSelectsSubsetOnSingleRow(): void
    {
        $out = $this->render(
            ['name' => 'alpha', 'secret' => 'hunter2', 'count' => 1],
            columns: [
                ColumnSpec::of('count', 'count'),
                ColumnSpec::of('name', 'name'),
            ],
        );
        $decoded = json_decode($out, true);
        $this->assertSame(['count', 'name'], array_keys($decoded));
        $this->assertStringNotContainsString('hunter2', $out);
    }

    /**
     * Rule 2: --cols beats ColumnSpec order.
     */
    public function testColsOverridesColumnSpecOrder(): void
    {
        $out = $this->render(
            [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
            cols: ['status', 'name'],
            columns: [
                ColumnSpec::of('name', 'name'),
                ColumnSpec::of('count', 'count'),
                ColumnSpec::of('status', 'status'),
            ],
        );
        $decoded = json_decode($out, true);
        $this->assertSame(['status', 'name'], array_keys($decoded[0]));
    }
}
