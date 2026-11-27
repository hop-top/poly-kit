<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

use Closure;
use HopTop\Kit\Upgrade\BackupUnsupported;
use HopTop\Kit\Upgrade\SchemaDriver;

/**
 * The corpus's driver (modelled on the Go runner's `recordingDriver`): logs
 * every state-changing call into the log the scenario's migrations share. It
 * is a `SchemaDriver` only; `StoringDriver`, `LockingDriver` and
 * `LockingStoringDriver` add the capabilities the driver fields ask for.
 */
class RecordingDriver implements SchemaDriver
{
    /**
     * @param array<string, mixed> $dc the scenario's driver object
     * @param Closure(string): void $record
     */
    public function __construct(protected array $dc, protected Closure $record) {}

    public function name(): string
    {
        return $this->dc['name'];
    }

    public function version(): string
    {
        if ($this->dc['lock'] ?? false) {
            ($this->record)($this->name() . ' version');
        }
        if (($this->dc['version_fail'] ?? '') !== '') {
            throw new \RuntimeException($this->dc['version_fail']);
        }

        return $this->dc['version'] ?? '';
    }

    public function setVersion(string $version): void
    {
        ($this->record)($this->name() . ' set_version ' . $version);
        $for = $this->dc['set_version_fail_for'] ?? '';
        if (($this->dc['set_version_fail'] ?? '') !== '' && ($for === '' || $for === $version)) {
            throw new \RuntimeException($this->dc['set_version_fail']);
        }
        $this->dc['version'] = $version;
    }

    public function backup(string $dest): void
    {
        ($this->record)($this->name() . ' backup ' . basename($dest));
        if ($this->dc['backup_unsupported'] ?? false) {
            throw new BackupUnsupported();
        }
        self::failIf($this->dc['backup_fail'] ?? '');
    }

    public function restore(string $src): void
    {
        ($this->record)($this->name() . ' restore ' . basename($src));
        if ($this->dc['restore_unsupported'] ?? false) {
            throw new BackupUnsupported();
        }
        self::failIf($this->dc['restore_fail'] ?? '');
    }

    protected static function failIf(string $msg): void
    {
        if ($msg !== '') {
            throw new \RuntimeException($msg);
        }
    }
}
