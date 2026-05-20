<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Builtin\YamlFormatter;
use PHPUnit\Framework\TestCase;
use Symfony\Component\Yaml\Yaml;

class YamlFormatterTest extends TestCase
{
    private function render(mixed $data, array $opts = [], array $cols = []): string
    {
        $resolved = array_merge(['inline' => 4], $opts);
        $w = fopen('php://memory', 'w+b');
        (new YamlFormatter())->render($w, $data, $resolved, $cols);
        rewind($w);
        return stream_get_contents($w) ?: '';
    }

    public function testRendersList(): void
    {
        $out = $this->render([['name' => 'alpha'], ['name' => 'beta']]);
        $parsed = Yaml::parse($out);
        $this->assertSame([['name' => 'alpha'], ['name' => 'beta']], $parsed);
    }

    public function testRendersMap(): void
    {
        $out = $this->render(['name' => 'alpha', 'count' => 1]);
        $parsed = Yaml::parse($out);
        $this->assertSame(['name' => 'alpha', 'count' => 1], $parsed);
    }

    public function testColsProjectsKeys(): void
    {
        $out = $this->render(
            [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
            cols: ['name', 'status'],
        );
        $parsed = Yaml::parse($out);
        $this->assertSame([['name' => 'alpha', 'status' => 'ok']], $parsed);
    }

    public function testInlineDepthControlsStyle(): void
    {
        // inline=0 forces everything inline (compact)
        $compact = $this->render(['a' => ['b' => 1]], ['inline' => 0]);
        $this->assertStringContainsString('{', $compact);
        // inline=10 keeps everything block
        $block = $this->render(['a' => ['b' => 1]], ['inline' => 10]);
        $this->assertStringNotContainsString('{', $block);
    }
}
