<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry;

use HopTop\Kit\Telemetry\Consent;
use PHPUnit\Framework\TestCase;

class ConsentTest extends TestCase
{
    private string $tmpDir;
    private string|false $prevXdgConfig;

    protected function setUp(): void
    {
        $this->tmpDir = sys_get_temp_dir() . '/kit-consent-' . bin2hex(random_bytes(8));
        mkdir($this->tmpDir . '/kit', 0o700, true);

        $this->prevXdgConfig = getenv('XDG_CONFIG_HOME');
        putenv('XDG_CONFIG_HOME=' . $this->tmpDir);
    }

    protected function tearDown(): void
    {
        if ($this->prevXdgConfig === false) {
            putenv('XDG_CONFIG_HOME');
        } else {
            putenv('XDG_CONFIG_HOME=' . $this->prevXdgConfig);
        }
        $this->rmrf($this->tmpDir);
    }

    public function testPathRespectsXdgConfigHome(): void
    {
        $this->assertSame(
            $this->tmpDir . '/kit/telemetry.yaml',
            Consent::path(),
        );
    }

    public function testMissingFileReturnsDenied(): void
    {
        $c = Consent::load();
        $this->assertFalse($c->allowed);
        $this->assertSame(0, $c->promptVersion);
        $this->assertSame('config', $c->decisionSource);
        $this->assertNull($c->decidedAt);
    }

    public function testMalformedYamlReturnsDenied(): void
    {
        file_put_contents(Consent::path(), "telemetry:\n  consent:\n    state: granted\n  : : :\nbroken\n");
        $c = Consent::load();
        $this->assertFalse($c->allowed);
        $this->assertSame('config', $c->decisionSource);
    }

    public function testUnknownStateReturnsDenied(): void
    {
        $yaml = <<<YAML
            telemetry:
              consent:
                state: maybe
                prompt_version: 1
                decision_source: cli
                decided_at: "2026-01-01T00:00:00Z"
            YAML;
        file_put_contents(Consent::path(), $yaml);

        $c = Consent::load();
        $this->assertFalse($c->allowed);
        // Unknown state -> safe default block, not the on-disk metadata.
        $this->assertSame(0, $c->promptVersion);
        $this->assertSame('config', $c->decisionSource);
    }

    public function testMissingConsentBlockReturnsDenied(): void
    {
        file_put_contents(Consent::path(), "telemetry:\n  enabled: true\n");
        $c = Consent::load();
        $this->assertFalse($c->allowed);
    }

    public function testTopLevelScalarReturnsDenied(): void
    {
        file_put_contents(Consent::path(), 'just-a-string');
        $c = Consent::load();
        $this->assertFalse($c->allowed);
    }

    public function testHappyGranted(): void
    {
        $yaml = <<<YAML
            telemetry:
              consent:
                state: granted
                prompt_version: 2
                decision_source: cli
                decided_at: "2026-05-19T12:00:00Z"
            YAML;
        file_put_contents(Consent::path(), $yaml);

        $c = Consent::load();
        $this->assertTrue($c->allowed);
        $this->assertSame(2, $c->promptVersion);
        $this->assertSame('cli', $c->decisionSource);
        $this->assertSame('2026-05-19T12:00:00Z', $c->decidedAt);
    }

    public function testHappyDenied(): void
    {
        $yaml = <<<YAML
            telemetry:
              consent:
                state: denied
                prompt_version: 1
                decision_source: tui
                decided_at: "2026-05-19T12:00:00Z"
            YAML;
        file_put_contents(Consent::path(), $yaml);

        $c = Consent::load();
        $this->assertFalse($c->allowed);
        $this->assertSame(1, $c->promptVersion);
        $this->assertSame('tui', $c->decisionSource);
        $this->assertSame('2026-05-19T12:00:00Z', $c->decidedAt);
    }

    public function testDeniedFactory(): void
    {
        $d = Consent::denied();
        $this->assertFalse($d->allowed);
        $this->assertSame(0, $d->promptVersion);
        $this->assertSame('config', $d->decisionSource);
        $this->assertNull($d->decidedAt);
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
