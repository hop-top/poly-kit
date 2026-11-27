<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Driver;

use HopTop\Kit\Tests\Upgrade\Support\TempDir;
use HopTop\Kit\Upgrade\Driver\ConfigFile;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

final class ConfigFileTest extends TestCase
{
    use TempDir;

    #[Test]
    public function backupNamesFilesAfterTheirFullPath(): void
    {
        file_put_contents("{$this->tmp}/app.yaml", 'a: 1');
        mkdir("{$this->tmp}/sub");
        file_put_contents("{$this->tmp}/sub/app.yaml", 'b: 2');
        $paths = ["{$this->tmp}/app.yaml", "{$this->tmp}/sub/../sub/app.yaml", "{$this->tmp}/missing.yaml"];
        mkdir("{$this->tmp}/bk");
        (new ConfigFile($paths))->backup("{$this->tmp}/bk");

        $flat = fn (string $p): string => str_replace('/', '_', $p);
        $got = array_values(array_diff((array) scandir("{$this->tmp}/bk"), ['.', '..']));
        sort($got);
        $want = [$flat("{$this->tmp}/app.yaml"), $flat("{$this->tmp}/sub/app.yaml")];
        sort($want);
        self::assertSame($want, $got, 'a missing file is skipped, paths are cleaned');
    }

    #[Test]
    public function restoreCopiesFilesBackAndSkipsWhatTheBackupLacks(): void
    {
        $a = "{$this->tmp}/conf/a.yaml";
        $b = "{$this->tmp}/conf/b.yaml";
        mkdir(dirname($a));
        file_put_contents($a, 'one');
        $d = new ConfigFile([$a, $b]);
        mkdir("{$this->tmp}/bk");
        $d->backup("{$this->tmp}/bk");

        unlink($a);
        rmdir(dirname($a));
        mkdir(dirname($b));
        file_put_contents($b, 'untouched');
        $d->restore("{$this->tmp}/bk");

        self::assertSame('one', file_get_contents($a), 'restored, parent directory recreated');
        self::assertSame('untouched', file_get_contents($b), 'not in the backup, so left alone');
    }

    #[Test]
    public function restoreOverwritesExistingContent(): void
    {
        $a = "{$this->tmp}/a.yaml";
        file_put_contents($a, 'original');
        $d = new ConfigFile([$a]);
        mkdir("{$this->tmp}/bk");
        $d->backup("{$this->tmp}/bk");
        file_put_contents($a, 'a much longer replacement');
        $d->restore("{$this->tmp}/bk");
        self::assertSame('original', file_get_contents($a));
    }

    #[Test]
    public function aDirectoryInPlaceOfAFileFailsTheBackup(): void
    {
        mkdir("{$this->tmp}/d");
        mkdir("{$this->tmp}/bk");
        $this->expectExceptionMessage('is a directory');
        (new ConfigFile(["{$this->tmp}/d"]))->backup("{$this->tmp}/bk");
    }

    #[Test]
    public function nameDefaultsToConfigAndVersionReadsTheFile(): void
    {
        mkdir("{$this->tmp}/dd/config", 0750, true);
        file_put_contents("{$this->tmp}/dd/config/version", '3.0.0');
        $d = new ConfigFile([], dataDir: "{$this->tmp}/dd");
        self::assertSame('config', $d->name());
        self::assertSame('3.0.0', $d->version());
        self::assertSame('', (new ConfigFile([], name: 'other', dataDir: "{$this->tmp}/dd"))->version());
    }
}
