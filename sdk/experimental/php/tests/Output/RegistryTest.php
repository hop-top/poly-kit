<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output;

use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use HopTop\Kit\Output\Formatter\Builtin\YamlFormatter;
use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Registry;
use InvalidArgumentException;
use PHPUnit\Framework\TestCase;

class RegistryTest extends TestCase
{
    public function testRegisterAndLookup(): void
    {
        $r = new Registry();
        $f = new JsonFormatter();
        $r->register($f);
        $this->assertSame($f, $r->lookup('json'));
        $this->assertNull($r->lookup('missing'));
    }

    public function testDuplicateRegisterThrows(): void
    {
        $r = new Registry();
        $r->register(new JsonFormatter());

        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage("'json' already registered");
        $r->register(new JsonFormatter());
    }

    public function testOverrideReplaces(): void
    {
        $r = new Registry();
        $r->register(new JsonFormatter());
        $replacement = new JsonFormatter();
        $r->override($replacement);
        $this->assertSame($replacement, $r->lookup('json'));
    }

    public function testKeysSortedForStableOutput(): void
    {
        $r = new Registry();
        $r->register(new YamlFormatter());
        $r->register(new JsonFormatter());
        $this->assertSame(['json', 'yaml'], $r->keys());
    }

    public function testExtensionMapLowercaseStripsDot(): void
    {
        $r = new Registry();
        $r->register(new JsonFormatter());
        $r->register(new YamlFormatter());
        $map = $r->extensionMap();
        $this->assertSame('json', $map['json']);
        $this->assertSame('yaml', $map['yaml']);
        $this->assertSame('yaml', $map['yml']);
    }

    public function testEmptyKeyRejected(): void
    {
        $r = new Registry();
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage('key is empty');
        $r->register(new class implements Formatter {
            public function key(): string { return ''; }
            public function extensions(): array { return []; }
            public function options(): array { return []; }
            public function render(mixed $writer, mixed $data, array $opts, array $cols): void {}
        });
    }

    public function testDefaultSingleton(): void
    {
        $a = Registry::default();
        $b = Registry::default();
        $this->assertSame($a, $b);
        $this->assertNotNull($a->lookup('json'));
        $this->assertNotNull($a->lookup('yaml'));
    }
}
