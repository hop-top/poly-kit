<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Builtin\TableFormatter;
use PHPUnit\Framework\TestCase;

class TableFormatterTest extends TestCase
{
    private function render(mixed $data, array $opts = [], array $cols = []): string
    {
        $resolved = array_merge(['header' => true], $opts);
        $w = fopen('php://memory', 'w+b');
        (new TableFormatter())->render($w, $data, $resolved, $cols);
        rewind($w);
        return stream_get_contents($w) ?: '';
    }

    public function testRendersHeaderAndBodyFromListOfMaps(): void
    {
        $out = $this->render([
            ['name' => 'alpha', 'count' => 1],
            ['name' => 'beta',  'count' => 22],
        ]);
        $lines = explode("\n", trim($out));
        $this->assertCount(3, $lines);
        $this->assertSame('name   count', $lines[0]);
        $this->assertSame('alpha  1', $lines[1]);
        $this->assertSame('beta   22', $lines[2]);
    }

    public function testInfersColumnsFromFirstRow(): void
    {
        $out = $this->render([['a' => 1, 'b' => 2]]);
        $this->assertStringContainsString('a  b', $out);
    }

    public function testColsProjectionOverridesInferredOrder(): void
    {
        $out = $this->render(
            [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
            cols: ['status', 'name'],
        );
        $lines = explode("\n", trim($out));
        $this->assertSame('status  name', $lines[0]);
        $this->assertSame('ok      alpha', $lines[1]);
    }

    public function testHeaderFalseSuppressesHeader(): void
    {
        $out = $this->render(
            [['name' => 'alpha']],
            opts: ['header' => false],
        );
        $this->assertSame("alpha\n", $out);
    }

    public function testSingleRowMapTreatedAsOneRow(): void
    {
        $out = $this->render(['name' => 'alpha', 'count' => 1]);
        $lines = explode("\n", trim($out));
        $this->assertSame('name   count', $lines[0]);
        $this->assertSame('alpha  1', $lines[1]);
    }

    public function testMissingKeysRenderAsEmpty(): void
    {
        $out = $this->render(
            [
                ['name' => 'alpha', 'count' => 1],
                ['name' => 'beta'],
            ],
            cols: ['name', 'count'],
        );
        $lines = explode("\n", trim($out));
        $this->assertSame('alpha  1', $lines[1]);
        // beta has no 'count' — cell renders empty (trailing whitespace
        // would have stripped to bare "beta" — trim() above hides it).
        $this->assertSame('beta', $lines[2]);
    }

    public function testNullAndBoolAndArrayValuesStringified(): void
    {
        $out = $this->render([
            ['v' => null, 'b' => true, 'a' => ['x', 'y']],
        ]);
        $this->assertStringContainsString('true', $out);
        $this->assertStringContainsString('["x","y"]', $out);
    }
}
