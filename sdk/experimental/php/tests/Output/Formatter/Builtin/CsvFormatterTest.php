<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Builtin\CsvFormatter;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Formatter\Projection;
use PHPUnit\Framework\TestCase;
use RuntimeException;

class CsvFormatterTest extends TestCase
{
    /**
     * @param array<string,mixed>   $opts
     * @param list<string>          $cols
     * @param list<ColumnSpec>|null $columns
     */
    private function render(mixed $data, array $opts = [], array $cols = [], ?array $columns = null): string
    {
        $resolved = array_merge(
            ['delimiter' => ',', 'no-header' => false, 'quote-all' => false, 'crlf' => false],
            $opts,
        );
        $w = fopen('php://memory', 'w+b');
        // Mirror the Dispatcher: --cols and the ColumnSpec list are collapsed
        // into one ordered list before the formatter ever sees them.
        (new CsvFormatter())->render(
            $w,
            $data,
            $resolved,
            Projection::resolveEffectiveCols($cols, $columns),
        );
        rewind($w);
        return stream_get_contents($w) ?: '';
    }

    /**
     * The adversarial row that pins csv quoting rules byte-for-byte.
     *
     * The cross-language ordering harness compares observed COLUMN ORDER and
     * never raw bytes, so quoting parity is enforced here and nowhere else.
     * Any drift in the quoting rule must fail loudly at this assertion.
     *
     * Go's rule, which py and ts reproduce byte-for-byte: quote a field iff
     * it contains the delimiter, a double quote, LF, CR, or BEGINS with a
     * space. Trailing spaces and tabs are deliberately NOT quoted.
     *
     * php's own fputcsv quotes both of those and is therefore NOT used: this
     * formatter would otherwise bake php's disagreement with go/py/ts into
     * the very port meant to close the portability gap.
     *
     * @return list<array<string,string>>
     */
    private static function adversarialRow(): array
    {
        return [[
            'a' => 'plain',
            'b' => 'with,comma',
            'c' => 'with"quote',
            'd' => "with\nnewline",
            'e' => ' leading space',
            'f' => 'trailing ',
            'g' => '',
            'h' => "with\ttab",
            'i' => "with\rcr",
        ]];
    }

    public function testQuotingMatchesGoByteForByte(): void
    {
        $this->assertSame(
            "a,b,c,d,e,f,g,h,i\n"
                . "plain,\"with,comma\",\"with\"\"quote\",\"with\nnewline\","
                . "\" leading space\",trailing ,,with\ttab,\"with\rcr\"\n",
            $this->render(self::adversarialRow()),
        );
    }

    /**
     * CRLF mode follows Go: an embedded LF is rewritten to CRLF inside the
     * quoted field, and a lone CR is DROPPED. ts's csv-stringify disagrees on
     * both counts; go is the stated reference runtime, so go wins.
     */
    public function testCrlfMatchesGoIncludingDroppedLoneCr(): void
    {
        $this->assertSame(
            "a,b,c,d,e,f,g,h,i\r\n"
                . "plain,\"with,comma\",\"with\"\"quote\",\"with\r\nnewline\","
                . "\" leading space\",trailing ,,with\ttab,\"withcr\"\r\n",
            $this->render(self::adversarialRow(), ['crlf' => true]),
        );
    }

    public function testHeaderAndRows(): void
    {
        $this->assertSame(
            "name,count\nalpha,1\nbeta,22\n",
            // Deliberately NOT alphabetical: sorted order would be count, name.
            $this->render([
                ['name' => 'alpha', 'count' => 1],
                ['name' => 'beta',  'count' => 22],
            ]),
        );
    }

    public function testNoHeaderOption(): void
    {
        $this->assertSame(
            "alpha,1\n",
            $this->render([['name' => 'alpha', 'count' => 1]], ['no-header' => true]),
        );
    }

    public function testQuoteAllOption(): void
    {
        $this->assertSame(
            "\"name\",\"count\"\n\"al\"\"pha\",\"1\"\n",
            $this->render([['name' => 'al"pha', 'count' => 1]], ['quote-all' => true]),
        );
    }

    public function testCustomDelimiter(): void
    {
        // 'a;b' must be quoted because it contains the ACTIVE delimiter;
        // 'c,d' must NOT be, because a comma is just a character now.
        $this->assertSame(
            "x;y\n\"a;b\";c,d\n",
            $this->render([['x' => 'a;b', 'y' => 'c,d']], ['delimiter' => ';']),
        );
    }

    public function testRejectsMultiCharDelimiter(): void
    {
        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage('delimiter must be exactly one character');
        $this->render([['x' => 1]], ['delimiter' => '||']);
    }

    /**
     * Rule 2: --cols reorders as well as selects. The requested order
     * disagrees with BOTH alphabetical (count, name, status) and payload
     * declaration order (name, count, status), so a runtime that merely
     * selects without reordering is caught here.
     */
    public function testColsReorderBeatsAlphabeticalAndDeclaration(): void
    {
        $this->assertSame(
            "status,name\nok,alpha\n",
            $this->render(
                [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
                cols: ['status', 'name'],
            ),
        );
    }

    /**
     * Rule 1: the ColumnSpec list drives default order and headers when the
     * caller supplies one and the user passes no --cols. Payload key order
     * here is the OPPOSITE of the spec order.
     */
    public function testColumnSpecDrivesDefaultOrder(): void
    {
        $this->assertSame(
            "status,name\nok,alpha\n",
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
     * Rule 4: zero rows emits nothing — not even a bare header row, even
     * though $cols is populated. Emptiness is decided by ROW count.
     */
    public function testZeroRowsEmitsNothing(): void
    {
        $this->assertSame(
            '',
            $this->render([], cols: ['name', 'count']),
        );
    }

    public function testMissingAndNullCellsAreEmpty(): void
    {
        $this->assertSame(
            "a,b,missing\n,1,\n",
            $this->render([['a' => null, 'b' => 1]], cols: ['a', 'b', 'missing']),
        );
    }

    public function testSingleMapPayloadIsOneRow(): void
    {
        $this->assertSame(
            "name\nalpha\n",
            $this->render(['name' => 'alpha']),
        );
    }

    public function testBooleanCellsRenderAsGoStyleLiterals(): void
    {
        $this->assertSame(
            "t,f\ntrue,false\n",
            $this->render([['t' => true, 'f' => false]]),
        );
    }
}
