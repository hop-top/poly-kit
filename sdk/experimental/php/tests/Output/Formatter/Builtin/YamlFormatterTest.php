<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output\Formatter\Builtin;

use HopTop\Kit\Output\Formatter\Builtin\YamlFormatter;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use PHPUnit\Framework\TestCase;
use Symfony\Component\Yaml\Yaml;

class YamlFormatterTest extends TestCase
{
    private function render(mixed $data, array $opts = [], array $cols = [], array $columns = []): string
    {
        $resolved = array_merge(['inline' => 4], $opts);
        $w = fopen('php://memory', 'w+b');
        (new YamlFormatter())->render($w, $data, $resolved, $cols, $columns);
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

    /**
     * Rule 1: ColumnSpec order drives the emitted key order. Asserted on
     * the raw YAML text, not just the parsed map, so the sequence is pinned
     * byte-for-byte.
     */
    public function testColumnSpecOrderDrivesEmittedKeyOrder(): void
    {
        $out = $this->render(
            [['count' => 1, 'name' => 'alpha', 'status' => 'ok']],
            columns: [
                ColumnSpec::of('status', 'status'),
                ColumnSpec::of('name', 'name'),
                ColumnSpec::of('count', 'count'),
            ],
        );
        $this->assertSame("-\n  status: ok\n  name: alpha\n  count: 1\n", $out);
        $parsed = Yaml::parse($out);
        $this->assertSame(['status', 'name', 'count'], array_keys($parsed[0]));
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
        $parsed = Yaml::parse($out);
        $this->assertSame(['status', 'name'], array_keys($parsed[0]));
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
