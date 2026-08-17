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
     * The rule: quote a field iff it contains the delimiter, a double quote,
     * LF, CR, or BEGINS with a unicode whitespace character. Trailing spaces
     * are deliberately NOT quoted.
     *
     * php's own fputcsv quotes trailing space and tab and is therefore NOT
     * used: this formatter would otherwise bake php's disagreement with the
     * other runtimes into the very port meant to close the portability gap.
     *
     * A quoted field's bytes are preserved verbatim — RFC 4180 lists CR and
     * LF as separate alternatives inside `escaped`, so a bare CR between
     * quotes is legal and dropping it would be silent data loss.
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
     * CRLF mode changes the RECORD TERMINATOR and nothing else. An embedded
     * LF stays an LF and a lone CR is preserved; rewriting either would
     * mutate the caller's value.
     */
    public function testCrlfChangesOnlyTheRecordTerminator(): void
    {
        $this->assertSame(
            "a,b,c,d,e,f,g,h,i\r\n"
                . "plain,\"with,comma\",\"with\"\"quote\",\"with\nnewline\","
                . "\" leading space\",trailing ,,with\ttab,\"with\rcr\"\r\n",
            $this->render(self::adversarialRow(), ['crlf' => true]),
        );
    }

    /**
     * Quote-all must preserve CR/LF too. The previous implementation dropped
     * a lone CR here as well — worse than the go writer it was copied from,
     * which preserved it on this path while dropping it on the default one.
     */
    public function testCrlfQuoteAllPreservesCr(): void
    {
        $out = $this->render(self::adversarialRow(), ['crlf' => true, 'quote-all' => true]);
        $this->assertStringContainsString("\"with\rcr\"", $out, 'lone CR must survive quote-all');
        $this->assertStringContainsString("\"with\nnewline\"", $out, 'in-field LF must stay LF');
        $this->assertStringNotContainsString('withcr', $out, 'CR must not be dropped');
    }

    /**
     * Round-trip is the acceptance criterion, not byte-equality: byte
     * equality alone would be satisfied by every runtime agreeing on lossy
     * output. Decoded with php's own str_getcsv.
     *
     * @return iterable<string,array{bool,bool}>
     */
    public static function roundTripModes(): iterable
    {
        yield 'lf' => [false, false];
        yield 'crlf' => [true, false];
        yield 'lf/quote-all' => [false, true];
        yield 'crlf/quote-all' => [true, true];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('roundTripModes')]
    public function testRoundTripsAdversarialRow(bool $crlf, bool $quoteAll): void
    {
        $expected = [
            'plain', 'with,comma', 'with"quote', "with\nnewline",
            ' leading space', 'trailing ', '', "with\ttab", "with\rcr",
        ];
        $out = $this->render(
            self::adversarialRow(),
            ['crlf' => $crlf, 'quote-all' => $quoteAll, 'no-header' => true],
        );
        // Strip the single record terminator, then decode the lone record.
        $body = substr($out, 0, -($crlf ? 2 : 1));
        $this->assertSame($expected, str_getcsv($body, ',', '"', ''));
    }

    /**
     * Go decides leading-whitespace quoting with unicode.IsSpace on the first
     * rune, not on a literal ASCII space. A leading TAB, vertical tab or NBSP
     * must therefore be quoted too.
     *
     * @return iterable<string,array{string,string}>
     */
    public static function leadingWhitespaceCases(): iterable
    {
        yield 'tab' => ["\tlead", "\"\tlead\"\n"];
        yield 'space' => [' lead', "\" lead\"\n"];
        yield 'nbsp' => ["\u{a0}lead", "\"\u{a0}lead\"\n"];
        yield 'vtab' => ["\vlead", "\"\vlead\"\n"];
        yield 'trailing space stays bare' => ['trail ', "trail \n"];
        yield 'plain stays bare' => ['plain', "plain\n"];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('leadingWhitespaceCases')]
    public function testQuotesAnyLeadingUnicodeSpace(string $input, string $want): void
    {
        $this->assertSame($want, $this->render([['v' => $input]], ['no-header' => true]));
    }

    /**
     * `\.` alone on a line terminates a PostgreSQL COPY stream; go's writer
     * quotes it defensively and so must this one.
     */
    public function testQuotesPostgresCopySentinel(): void
    {
        $this->assertSame("\"\\.\"\n", $this->render([['v' => '\\.']], ['no-header' => true]));
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
