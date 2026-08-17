<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Builtin\TableFormatter;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use PHPUnit\Framework\TestCase;

class TableFormatterTest extends TestCase
{
    private function render(mixed $data, array $opts = [], array $cols = [], array $columns = []): string
    {
        $resolved = array_merge(['header' => true], $opts);
        $w = fopen('php://memory', 'w+b');
        (new TableFormatter())->render($w, $data, $resolved, $cols, $columns);
        rewind($w);
        return stream_get_contents($w) ?: '';
    }

    /**
     * Rule 1: no ColumnSpec supplied — payload key order is the fallback.
     */
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

    /**
     * Rule 1 fallback: key inference applies only on the no-ColumnSpec path.
     */
    public function testInfersColumnsFromFirstRowWhenNoColumnSpec(): void
    {
        $out = $this->render([['a' => 1, 'b' => 2]]);
        $this->assertSame("a  b\n1  2\n", $out);
    }

    /**
     * Rule 2: --cols reorders as well as selects — user order wins, over
     * both the inferred key order and any ColumnSpec order.
     */
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

    /**
     * Rule 1 fallback: single map, no ColumnSpec — key order drives columns.
     */
    public function testSingleRowMapTreatedAsOneRowWhenNoColumnSpec(): void
    {
        $out = $this->render(['name' => 'alpha', 'count' => 1]);
        $lines = explode("\n", trim($out));
        $this->assertSame('name   count', $lines[0]);
        $this->assertSame('alpha  1', $lines[1]);
    }

    /**
     * Rule 3: header == key, so --cols entries are simultaneously the
     * header labels and the row lookups. No ColumnSpec needed to hold.
     */
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

    /**
     * Rule 1: ColumnSpec list drives default column order + headers. The
     * payload key order here (count, name, status) deliberately disagrees
     * with the spec order (status, name, count) so only spec order can
     * produce the asserted line.
     */
    public function testColumnSpecOrderDrivesDefaultColumnOrder(): void
    {
        $out = $this->render(
            [['count' => 1, 'name' => 'alpha', 'status' => 'ok']],
            columns: [
                ColumnSpec::of('status', 'status'),
                ColumnSpec::of('name', 'name'),
                ColumnSpec::of('count', 'count'),
            ],
        );
        $lines = explode("\n", trim($out));
        $this->assertSame('status  name   count', $lines[0]);
        $this->assertSame('ok      alpha  1', $lines[1]);
    }

    /**
     * Rule 1: the ColumnSpec list also SELECTS — payload keys absent from
     * the spec are not columns.
     */
    public function testColumnSpecSelectsSubsetOfPayloadKeys(): void
    {
        $out = $this->render(
            [['name' => 'alpha', 'secret' => 'hunter2', 'count' => 1]],
            columns: [
                ColumnSpec::of('count', 'count'),
                ColumnSpec::of('name', 'name'),
            ],
        );
        $lines = explode("\n", trim($out));
        $this->assertSame('count  name', $lines[0]);
        $this->assertSame('1      alpha', $lines[1]);
        $this->assertStringNotContainsString('secret', $out);
        $this->assertStringNotContainsString('hunter2', $out);
    }

    /**
     * Rule 2: --cols beats ColumnSpec order too, not just inferred order.
     */
    public function testColsOverridesColumnSpecOrder(): void
    {
        $out = $this->render(
            [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
            cols: ['count', 'status'],
            columns: [
                ColumnSpec::of('name', 'name'),
                ColumnSpec::of('status', 'status'),
                ColumnSpec::of('count', 'count'),
            ],
        );
        $lines = explode("\n", trim($out));
        $this->assertSame('count  status', $lines[0]);
        $this->assertSame('1      ok', $lines[1]);
    }

    /**
     * Rule 4: zero rows emits nothing — not even a bare header row — even
     * though a ColumnSpec list supplies headers. Emptiness is decided by
     * ROW count, not header count.
     */
    public function testZeroRowsWithColumnSpecEmitsNothing(): void
    {
        $out = $this->render(
            [],
            columns: [
                ColumnSpec::of('name', 'name'),
                ColumnSpec::of('count', 'count'),
            ],
        );
        $this->assertSame('', $out);
    }

    /**
     * Rule 4: same for --cols supplying the headers with no rows.
     */
    public function testZeroRowsWithColsEmitsNothing(): void
    {
        $out = $this->render([], cols: ['name', 'count']);
        $this->assertSame('', $out);
    }

    /**
     * Rule 4: and with no header source at all.
     */
    public function testZeroRowsWithNoSchemaEmitsNothing(): void
    {
        $this->assertSame('', $this->render([]));
    }

    /**
     * Rule 5: priority is accepted and stored but has no effect on
     * rendering in the payload SDKs (Go-only feature today).
     */
    public function testPriorityIsAcceptedButIgnored(): void
    {
        $spec = [
            ColumnSpec::of('name', 'name', priority: 1),
            ColumnSpec::of('count', 'count', priority: 9),
        ];
        $out = $this->render([['name' => 'alpha', 'count' => 1]], columns: $spec);
        $lines = explode("\n", trim($out));
        $this->assertSame('name   count', $lines[0]);
        $this->assertSame(1, $spec[0]->priority);
        $this->assertSame(9, $spec[1]->priority);
    }
}
