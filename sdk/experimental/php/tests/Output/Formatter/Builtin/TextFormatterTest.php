<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Builtin\TextFormatter;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Formatter\Projection;
use PHPUnit\Framework\TestCase;
use RuntimeException;

class TextFormatterTest extends TestCase
{
    /**
     * @param array<string,mixed>   $opts
     * @param list<string>          $cols
     * @param list<ColumnSpec>|null $columns
     */
    private function render(mixed $data, array $opts = [], array $cols = [], ?array $columns = null): string
    {
        $resolved = array_merge(['style' => 'kv', 'separator' => '='], $opts);
        $w = fopen('php://memory', 'w+b');
        // Mirror the Dispatcher: --cols and the ColumnSpec list are collapsed
        // into one ordered list before the formatter ever sees them.
        (new TextFormatter())->render(
            $w,
            $data,
            $resolved,
            Projection::resolveEffectiveCols($cols, $columns),
        );
        rewind($w);
        return stream_get_contents($w) ?: '';
    }

    /**
     * Blank line BETWEEN records, never trailing — matches go text.go.
     */
    public function testKvDefaultStyle(): void
    {
        $this->assertSame(
            "name=alpha\ncount=1\n\nname=beta\ncount=22\n",
            $this->render([
                ['name' => 'alpha', 'count' => 1],
                ['name' => 'beta',  'count' => 22],
            ]),
        );
    }

    public function testKvCustomSeparator(): void
    {
        $this->assertSame(
            "name: alpha\n",
            $this->render([['name' => 'alpha']], ['separator' => ': ']),
        );
    }

    public function testLinesStyleIsTabSeparatedWithoutHeader(): void
    {
        $this->assertSame(
            "alpha\t1\nbeta\t22\n",
            $this->render(
                [
                    ['name' => 'alpha', 'count' => 1],
                    ['name' => 'beta',  'count' => 22],
                ],
                ['style' => 'lines'],
            ),
        );
    }

    /**
     * Records are 1-indexed; blank line BETWEEN records, never trailing.
     */
    public function testParagraphStyle(): void
    {
        $this->assertSame(
            "Record 1:\n  name: alpha\n  count: 1\n\nRecord 2:\n  name: beta\n  count: 22\n",
            $this->render(
                [
                    ['name' => 'alpha', 'count' => 1],
                    ['name' => 'beta',  'count' => 22],
                ],
                ['style' => 'paragraph'],
            ),
        );
    }

    /**
     * Rule 2: --cols reorders as well as selects. Requested order disagrees
     * with both alphabetical and payload declaration order.
     */
    public function testColsReorderBeatsAlphabeticalAndDeclaration(): void
    {
        $this->assertSame(
            "status=ok\nname=alpha\n",
            $this->render(
                [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
                cols: ['status', 'name'],
            ),
        );
    }

    /**
     * Rule 1: ColumnSpec list order drives the default, beating payload key
     * order (which is the OPPOSITE here).
     */
    public function testColumnSpecDrivesDefaultOrder(): void
    {
        $this->assertSame(
            "status=ok\nname=alpha\n",
            $this->render(
                [['name' => 'alpha', 'status' => 'ok']],
                columns: [
                    new ColumnSpec(header: 'status', key: 'status', priority: 9),
                    new ColumnSpec(header: 'name', key: 'name', priority: 7),
                ],
            ),
        );
    }

    /**
     * Rule 4: zero rows emits nothing, even with $cols populated.
     */
    public function testZeroRowsEmitsNothing(): void
    {
        $this->assertSame('', $this->render([], cols: ['name', 'count']));
    }

    public function testSingleMapPayloadIsOneRecord(): void
    {
        $this->assertSame("name=alpha\n", $this->render(['name' => 'alpha']));
    }

    public function testUnknownStyleRejected(): void
    {
        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage('unknown style');
        $this->render([['a' => 1]], ['style' => 'bogus']);
    }

    public function testNullAndMissingCellsRenderEmpty(): void
    {
        $this->assertSame(
            "a=\nb=1\nmissing=\n",
            $this->render([['a' => null, 'b' => 1]], cols: ['a', 'b', 'missing']),
        );
    }
}
