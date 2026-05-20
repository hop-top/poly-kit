<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry;

use HopTop\Kit\Telemetry\Mode;
use PHPUnit\Framework\TestCase;

class ModeTest extends TestCase
{
    public function testCanonicalLowercase(): void
    {
        $this->assertSame(Mode::Off, Mode::tryFromLoose('off'));
        $this->assertSame(Mode::Anon, Mode::tryFromLoose('anon'));
        $this->assertSame(Mode::Full, Mode::tryFromLoose('full'));
    }

    public function testCaseInsensitive(): void
    {
        $this->assertSame(Mode::Off, Mode::tryFromLoose('OFF'));
        $this->assertSame(Mode::Anon, Mode::tryFromLoose('Anon'));
        $this->assertSame(Mode::Full, Mode::tryFromLoose('FULL'));
    }

    public function testWhitespaceTrimmed(): void
    {
        $this->assertSame(Mode::Full, Mode::tryFromLoose('  full  '));
        $this->assertSame(Mode::Anon, Mode::tryFromLoose("\tanon\n"));
    }

    public function testNullDefaultsToOff(): void
    {
        $this->assertSame(Mode::Off, Mode::tryFromLoose(null));
    }

    public function testEmptyDefaultsToOff(): void
    {
        $this->assertSame(Mode::Off, Mode::tryFromLoose(''));
        $this->assertSame(Mode::Off, Mode::tryFromLoose('   '));
    }

    public function testGarbageDefaultsToOff(): void
    {
        $this->assertSame(Mode::Off, Mode::tryFromLoose('garbage'));
        $this->assertSame(Mode::Off, Mode::tryFromLoose('verbose'));
        $this->assertSame(Mode::Off, Mode::tryFromLoose('1'));
    }

    public function testEnumValues(): void
    {
        $this->assertSame('off', Mode::Off->value);
        $this->assertSame('anon', Mode::Anon->value);
        $this->assertSame('full', Mode::Full->value);
    }
}
