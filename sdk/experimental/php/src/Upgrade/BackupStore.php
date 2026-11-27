<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * The backups of one schema under `<base>/backups/<schema>/`: one directory
 * `pre-<version>` per backup and a sibling `pre-<version>.version` file with
 * the version the run was at when it was taken.
 *
 * @internal
 */
final class BackupStore
{
    private const SUFFIX = '.version';

    public function __construct(
        private readonly string $base,
        private readonly string $schema,
        private readonly int $retention,
    ) {}

    private function schemaDir(): string
    {
        return "{$this->base}/backups/{$this->schema}";
    }

    public function hasBackup(string $version): bool
    {
        return file_exists("{$this->schemaDir()}/pre-$version");
    }

    /** Creates and returns the backup directory for `$version`. */
    public function backupDir(string $version): string
    {
        $dir = "{$this->schemaDir()}/pre-$version";
        try {
            FsUtil::mkdir($dir);
        } catch (UpgradeException $e) {
            throw new UpgradeException("create backup dir: {$e->getMessage()}", 0, $e);
        }

        return $dir;
    }

    /** Saves the version the run starts at beside the backup ('' is an empty file). */
    public function savePreRun(string $backupPath, string $version): void
    {
        try {
            FsUtil::put($backupPath . self::SUFFIX, $version);
        } catch (UpgradeException $e) {
            throw new UpgradeException("write pre-run version: {$e->getMessage()}", 0, $e);
        }
    }

    /** @return array{0: string, 1: bool} the saved version, and whether one was saved */
    public function readPreRun(string $backupPath): array
    {
        $file = $backupPath . self::SUFFIX;
        if (!file_exists($file)) {
            return ['', false];
        }
        $text = @file_get_contents($file);
        if ($text === false) {
            throw new UpgradeException('read pre-run version: ' . FsUtil::lastError());
        }

        return [trim($text), true];
    }

    /** The most recent backup directory (by semver). */
    public function latestBackup(): string
    {
        $dirs = $this->dirs();
        if ($dirs === []) {
            throw new NotRestored('no backups found for ' . $this->schema, 0, new BackupUnsupported());
        }
        usort($dirs, static fn (array $a, array $b): int => $b[0]->compareTo($a[0]));

        return $this->schemaDir() . '/' . $dirs[0][1];
    }

    /** Keeps the newest `retention` backups, removes the rest. */
    public function prune(): void
    {
        $dirs = $this->dirs();
        usort($dirs, static fn (array $a, array $b): int => $a[0]->compareTo($b[0]));
        if (count($dirs) <= $this->retention) {
            return;
        }
        foreach (array_slice($dirs, 0, count($dirs) - $this->retention) as [, $name]) {
            $p = $this->schemaDir() . '/' . $name;
            try {
                FsUtil::removeAll($p);
                if (file_exists($p . self::SUFFIX) && !@unlink($p . self::SUFFIX)) {
                    throw new UpgradeException(FsUtil::lastError());
                }
            } catch (UpgradeException $e) {
                throw new UpgradeException("prune backup $name: {$e->getMessage()}", 0, $e);
            }
        }
    }

    /** @return list<array{0: Semver, 1: string}> */
    private function dirs(): array
    {
        $dir = $this->schemaDir();
        if (!is_dir($dir)) {
            return [];
        }
        $names = @scandir($dir);
        if ($names === false) {
            throw new UpgradeException('read backup dir: ' . FsUtil::lastError());
        }
        $out = [];
        foreach ($names as $n) {
            if (str_starts_with($n, 'pre-') && is_dir("$dir/$n")) {
                $out[] = [Semver::parse(substr($n, 4)), $n];
            }
        }

        return $out;
    }
}
