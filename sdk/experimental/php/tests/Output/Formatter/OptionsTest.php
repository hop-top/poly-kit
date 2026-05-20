<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter;

use HopTop\Kit\Output\Formatter\Options;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use InvalidArgumentException;
use PHPUnit\Framework\TestCase;

class OptionsTest extends TestCase
{
    public function testParseStringValue(): void
    {
        $specs = [new OptionSpec('delimiter', OptionType::String, default: ',')];
        $out = Options::parse(['delimiter=;'], $specs);
        $this->assertSame(['delimiter' => ';'], $out);
    }

    public function testParseIntValue(): void
    {
        $specs = [new OptionSpec('indent', OptionType::Int, default: 2)];
        $out = Options::parse(['indent=4'], $specs);
        $this->assertSame(['indent' => 4], $out);
    }

    public function testParseIntRejectsNonNumeric(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage("'abc' is not an int");
        Options::parse(['indent=abc'], [new OptionSpec('indent', OptionType::Int)]);
    }

    public function testParseBoolKeyOnlyMeansTrue(): void
    {
        $specs = [new OptionSpec('header', OptionType::Bool)];
        $out = Options::parse(['header'], $specs);
        $this->assertSame(['header' => true], $out);
    }

    public function testParseBoolKeyOnlyRejectedForNonBool(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage("requires a value");
        Options::parse(['delimiter'], [new OptionSpec('delimiter', OptionType::String)]);
    }

    public function testParseBoolAcceptsCommonForms(): void
    {
        $spec = new OptionSpec('flag', OptionType::Bool);
        foreach (['flag=true', 'flag=1', 'flag=yes', 'flag=y', 'flag=t'] as $pair) {
            $this->assertSame(['flag' => true], Options::parse([$pair], [$spec]));
        }
        foreach (['flag=false', 'flag=0', 'flag=no', 'flag=n', 'flag=f'] as $pair) {
            $this->assertSame(['flag' => false], Options::parse([$pair], [$spec]));
        }
    }

    public function testParseEnumValidatesAgainstAllowed(): void
    {
        $spec = new OptionSpec('case', OptionType::Enum, enum: ['upper', 'lower']);
        $this->assertSame(['case' => 'upper'], Options::parse(['case=upper'], [$spec]));

        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage("'mixed' not in {upper, lower}");
        Options::parse(['case=mixed'], [$spec]);
    }

    public function testParseUnknownKeyListsValid(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage("unknown option 'foo' (valid: bar, baz)");
        Options::parse(['foo=1'], [
            new OptionSpec('bar', OptionType::String),
            new OptionSpec('baz', OptionType::String),
        ]);
    }

    public function testParseDefaultsFillMissingKeys(): void
    {
        $specs = [
            new OptionSpec('a', OptionType::String, default: 'A'),
            new OptionSpec('b', OptionType::Int, default: 7),
        ];
        $out = Options::parse(['a=override'], $specs);
        $this->assertSame(['a' => 'override', 'b' => 7], $out);
    }

    public function testParseEmptyKeyRejected(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage('empty option key');
        Options::parse(['=value'], [new OptionSpec('x', OptionType::String)]);
    }
}
