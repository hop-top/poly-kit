<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Skill;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

/**
 * Runs every agent-skill snippet (skills/<skill>/assets/snippets/<name>/
 * <name>.php) in a fresh PHP process against this package's autoloader and
 * compares stdout with the sibling <name>.expected, so a snippet cannot rot.
 */
final class SnippetsTest extends TestCase
{
    /** @return iterable<string, array{string}> */
    public static function snippets(): iterable
    {
        $root = dirname(__DIR__, 2);
        $files = glob($root . '/skills/*/assets/snippets/*/*.php');
        foreach ($files === false ? [] : $files as $file) {
            yield basename(dirname($file)) => [$file];
        }
    }

    public function testSnippetsExist(): void
    {
        $this->assertNotEmpty(iterator_to_array(self::snippets()));
    }

    #[DataProvider('snippets')]
    public function testSnippetOutputMatchesExpected(string $file): void
    {
        $expected = substr($file, 0, -strlen('.php')) . '.expected';
        $this->assertFileExists($expected, 'snippet without .expected');

        $cmd = [
            PHP_BINARY,
            '-d', 'auto_prepend_file=' . dirname(__DIR__, 2) . '/vendor/autoload.php',
            $file,
        ];
        $proc = proc_open($cmd, [1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
        $this->assertIsResource($proc);
        $stdout = (string) stream_get_contents($pipes[1]);
        $stderr = (string) stream_get_contents($pipes[2]);
        fclose($pipes[1]);
        fclose($pipes[2]);
        $code = proc_close($proc);

        $this->assertSame(0, $code, $stderr);
        $this->assertSame('', $stderr);
        $this->assertSame((string) file_get_contents($expected), $stdout);
    }
}
