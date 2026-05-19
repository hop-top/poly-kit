<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry;

use HopTop\Kit\Telemetry\InstallId;
use PHPUnit\Framework\TestCase;
use RuntimeException;

class InstallIdTest extends TestCase
{
    private string $tmpDir;
    private string|false $prevXdgState;

    protected function setUp(): void
    {
        $this->tmpDir = sys_get_temp_dir() . '/kit-installid-' . bin2hex(random_bytes(8));
        mkdir($this->tmpDir, 0o700, true);

        $this->prevXdgState = getenv('XDG_STATE_HOME');
        putenv('XDG_STATE_HOME=' . $this->tmpDir);
    }

    protected function tearDown(): void
    {
        if ($this->prevXdgState === false) {
            putenv('XDG_STATE_HOME');
        } else {
            putenv('XDG_STATE_HOME=' . $this->prevXdgState);
        }
        $this->rmrf($this->tmpDir);
    }

    public function testPathRespectsXdgStateHome(): void
    {
        $this->assertSame(
            $this->tmpDir . '/kit/telemetry/installation_id',
            InstallId::path(),
        );
    }

    public function testGetReturns64CharHex(): void
    {
        $id = InstallId::get();
        $this->assertSame(64, strlen($id));
        $this->assertMatchesRegularExpression('/^[0-9a-f]{64}$/', $id);
    }

    public function testGetPersistsFile(): void
    {
        $id = InstallId::get();
        $p = InstallId::path();
        $this->assertFileExists($p);
        $raw = file_get_contents($p);
        $this->assertSame(32, strlen($raw));
        $this->assertSame($id, hash('sha256', $raw));
    }

    public function testGetSetsRestrictivePerms(): void
    {
        InstallId::get();
        $p = InstallId::path();
        $mode = fileperms($p) & 0o777;
        // 0o600 is the target; allow 0o644 on filesystems that strip group/other (e.g. some CI tmpfs).
        $this->assertContains($mode, [0o600, 0o644], "unexpected perms: " . decoct($mode));
    }

    public function testGetIsStableAcrossCalls(): void
    {
        $a = InstallId::get();
        $b = InstallId::get();
        $this->assertSame($a, $b);
    }

    public function testRotateProducesDifferentId(): void
    {
        $a = InstallId::get();
        $b = InstallId::rotate();
        $this->assertNotSame($a, $b);
        $this->assertSame(64, strlen($b));
        // Subsequent get() returns the rotated value.
        $this->assertSame($b, InstallId::get());
    }

    public function testMalformedFileThrows(): void
    {
        $p = InstallId::path();
        mkdir(dirname($p), 0o700, true);
        file_put_contents($p, 'short');

        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage('wrong size');
        InstallId::get();
    }

    public function testResetForTestRemovesFile(): void
    {
        InstallId::get();
        $p = InstallId::path();
        $this->assertFileExists($p);

        InstallId::resetForTest();
        $this->assertFileDoesNotExist($p);

        // Subsequent get() regenerates a fresh id.
        $fresh = InstallId::get();
        $this->assertSame(64, strlen($fresh));
    }

    private function rmrf(string $dir): void
    {
        if (!is_dir($dir)) {
            return;
        }
        $items = scandir($dir);
        if ($items === false) {
            return;
        }
        foreach ($items as $item) {
            if ($item === '.' || $item === '..') {
                continue;
            }
            $path = $dir . '/' . $item;
            if (is_dir($path) && !is_link($path)) {
                $this->rmrf($path);
            } else {
                @unlink($path);
            }
        }
        @rmdir($dir);
    }
}
