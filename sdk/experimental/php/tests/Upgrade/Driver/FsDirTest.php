<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Driver;

use HopTop\Kit\Tests\Upgrade\Support\TempDir;
use HopTop\Kit\Upgrade\Driver\FsDir;
use HopTop\Kit\Upgrade\Driver\TarNames;
use HopTop\Kit\Upgrade\UpgradeException;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

final class FsDirTest extends TestCase
{
    use TempDir;

    private function tree(string $dir): void
    {
        mkdir("$dir/sub", 0755, true);
        mkdir("$dir/empty", 0700);
        file_put_contents("$dir/a.txt", "alpha\n");
        file_put_contents("$dir/sub/b.txt", "beta\n");
        file_put_contents("$dir/zero", '');
    }

    /** @return array<string, string> relative path => content ('' for dirs, with trailing slash) */
    private static function snapshot(string $dir): array
    {
        $out = [];
        $walk = static function (string $d, string $rel) use (&$walk, &$out): void {
            foreach ((array) scandir($d) as $n) {
                if ($n === '.' || $n === '..') {
                    continue;
                }
                $p = "$d/$n";
                $r = $rel === '' ? $n : "$rel/$n";
                if (is_dir($p)) {
                    $out["$r/"] = '';
                    $walk($p, $r);
                } else {
                    $out[$r] = (string) file_get_contents($p);
                }
            }
        };
        $walk($dir, '');
        ksort($out);

        return $out;
    }

    #[Test]
    public function backupThenRestoreRoundTrips(): void
    {
        $dir = "{$this->tmp}/data";
        $this->tree($dir);
        $before = self::snapshot($dir);
        $d = new FsDir('files', $dir);
        mkdir("{$this->tmp}/bk");
        $d->backup("{$this->tmp}/bk");
        self::assertFileExists("{$this->tmp}/bk/files.tar.gz");
        self::assertSame([], glob("{$this->tmp}/bk/.*.tar") ?: [], 'temporary tar left behind');

        file_put_contents("$dir/a.txt", 'changed');
        file_put_contents("$dir/new.txt", 'extra');
        rmdir("$dir/empty");
        $d->restore("{$this->tmp}/bk");

        self::assertSame($before, self::snapshot($dir));
        self::assertSame(0700, fileperms("$dir/empty") & 0777, 'directory permissions come back');
    }

    #[Test]
    public function archiveHasTheGoLayout(): void
    {
        $dir = "{$this->tmp}/data";
        $this->tree($dir);
        mkdir("{$this->tmp}/bk");
        (new FsDir('files', $dir))->backup("{$this->tmp}/bk");
        $tar = "{$this->tmp}/files.tar";
        $in = gzopen("{$this->tmp}/bk/files.tar.gz", 'rb');
        file_put_contents($tar, stream_get_contents($in));
        $names = TarNames::of($tar);
        sort($names);
        self::assertSame(['a.txt', 'empty', 'sub', 'sub/b.txt', 'zero'], $names);
        if (PHP_OS_FAMILY !== 'Windows' && trim((string) shell_exec('command -v tar')) !== '') {
            $listing = (string) shell_exec('tar -tvf ' . escapeshellarg($tar) . ' 2>&1');
            self::assertMatchesRegularExpression('/^drwx------.* empty$/m', $listing);
            self::assertMatchesRegularExpression('/^-rw-r--r--.* a\.txt$/m', $listing);
        }
    }

    #[Test]
    public function restoresAnArchiveTheGoDriverWrote(): void
    {
        $bk = "{$this->tmp}/bk";
        mkdir($bk);
        copy(__DIR__ . '/fixtures/go-backup.tar.gz', "$bk/files.tar.gz");
        $dir = "{$this->tmp}/data";
        mkdir($dir);
        file_put_contents("$dir/stale", 'x');
        (new FsDir('files', $dir))->restore($bk);

        self::assertSame([
            'a.txt' => "alpha\n", 'deep/' => '', 'deep/er/' => '', 'deep/er/c.txt' => "gamma\n",
            'empty/' => '', 'sub/' => '', 'sub/b.txt' => "beta\n",
        ], self::snapshot($dir));
        self::assertSame(0700, fileperms("$dir/empty") & 0777);
    }

    #[Test]
    public function aSecondBackupToTheSameDestinationWins(): void
    {
        $dir = "{$this->tmp}/data";
        mkdir($dir);
        mkdir("{$this->tmp}/bk");
        $d = new FsDir('files', $dir);
        file_put_contents("$dir/f", 'one');
        $d->backup("{$this->tmp}/bk");
        $d->restore("{$this->tmp}/bk");
        file_put_contents("$dir/f", 'two');
        $d->backup("{$this->tmp}/bk");
        file_put_contents("$dir/f", 'three');
        $d->restore("{$this->tmp}/bk");
        self::assertSame('two', file_get_contents("$dir/f"));
    }

    #[Test]
    public function aMissingDirectoryBacksUpNothing(): void
    {
        mkdir("{$this->tmp}/bk");
        (new FsDir('files', "{$this->tmp}/absent"))->backup("{$this->tmp}/bk");
        self::assertSame([], glob("{$this->tmp}/bk/*") ?: []);
        $this->expectException(UpgradeException::class);
        $this->expectExceptionMessage('backup archive not found');
        (new FsDir('files', "{$this->tmp}/absent"))->restore("{$this->tmp}/bk");
    }

    #[Test]
    public function aSymlinkFailsTheBackup(): void
    {
        $dir = "{$this->tmp}/data";
        mkdir($dir);
        symlink('/etc/hosts', "$dir/link");
        mkdir("{$this->tmp}/bk");
        try {
            (new FsDir('files', $dir))->backup("{$this->tmp}/bk");
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringContainsString('symlink', $e->getMessage());
        }
        self::assertSame([], glob("{$this->tmp}/bk/*") ?: [], 'no archive for a failed backup');
    }

    #[Test]
    public function aPathPartTooLongForUstarFailsTheBackupCleanly(): void
    {
        $dir = "{$this->tmp}/data";
        mkdir($dir);
        file_put_contents("$dir/" . str_repeat('x', 120), '');
        mkdir("{$this->tmp}/bk");
        try {
            (new FsDir('files', $dir))->backup("{$this->tmp}/bk");
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertStringStartsWith('create archive:', $e->getMessage());
        }
        self::assertSame([], array_values(array_diff((array) scandir("{$this->tmp}/bk"), ['.', '..'])), 'no archive and no temporary tar');
    }

    /** A one-entry-per-name ustar archive, gzipped. */
    private static function rawTar(array $entries): string
    {
        $raw = '';
        foreach ($entries as $name => $body) {
            $h = str_pad($name, 100, "\0") . sprintf("%07o\0", 0644) . "0000000\0" . "0000000\0"
                . sprintf("%011o\0", strlen($body)) . "00000000000\0" . '        ' . '0'
                . str_pad('', 100, "\0") . "ustar\0" . '00' . str_pad('', 247, "\0");
            $sum = array_sum(array_map('ord', str_split($h)));
            $h = substr($h, 0, 148) . sprintf("%06o\0 ", $sum) . substr($h, 156);
            $raw .= $h . str_pad($body, (int) (ceil(strlen($body) / 512) * 512), "\0");
        }

        return (string) gzencode($raw . str_repeat("\0", 1024));
    }

    #[Test]
    public function restoreRefusesPathTraversalBeforeTouchingAnything(): void
    {
        $dir = "{$this->tmp}/data";
        mkdir($dir);
        file_put_contents("$dir/keep", 'keep');
        mkdir("{$this->tmp}/bk");
        file_put_contents("{$this->tmp}/bk/files.tar.gz", self::rawTar(['ok.txt' => 'hi', '../evil.txt' => 'x']));
        try {
            (new FsDir('files', $dir))->restore("{$this->tmp}/bk");
            self::fail('no exception');
        } catch (UpgradeException $e) {
            self::assertSame('path traversal blocked: ../evil.txt', $e->getMessage());
        }
        self::assertFileDoesNotExist("{$this->tmp}/evil.txt");
        self::assertSame('keep', file_get_contents("$dir/keep"), 'the directory was left alone');
    }

    #[Test]
    public function aSiblingDirectoryWithTheSamePrefixIsOutside(): void
    {
        $dir = "{$this->tmp}/data";
        mkdir($dir);
        mkdir("{$this->tmp}/bk");
        file_put_contents("{$this->tmp}/bk/files.tar.gz", self::rawTar(['../data2/x' => 'x']));
        $this->expectExceptionMessage('path traversal blocked: ../data2/x');
        (new FsDir('files', $dir))->restore("{$this->tmp}/bk");
    }

    #[Test]
    public function versionReadsTheMigratorsFile(): void
    {
        mkdir("{$this->tmp}/dd/files", 0750, true);
        file_put_contents("{$this->tmp}/dd/files/version", "1.2.0\n");
        self::assertSame('1.2.0', (new FsDir('files', "{$this->tmp}/x", dataDir: "{$this->tmp}/dd"))->version());
        self::assertSame('', (new FsDir('other', "{$this->tmp}/x", dataDir: "{$this->tmp}/dd"))->version());
    }
}
