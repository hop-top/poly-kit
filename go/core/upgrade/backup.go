package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hop.top/kit/go/core/xdg"
)

const defaultRetention = 5

// backupOrchestrator manages backup lifecycle for a single schema.
type backupOrchestrator struct {
	tool      string
	schema    string
	retention int
	base      string // data dir; "" resolves xdg.DataDir("hop/<tool>")
}

func newBackupOrchestrator(tool, schema string, retention int) *backupOrchestrator {
	if retention <= 0 {
		retention = defaultRetention
	}
	return &backupOrchestrator{
		tool:      tool,
		schema:    schema,
		retention: retention,
	}
}

// dataDir returns the explicit base, or xdg.DataDir("hop/<tool>").
func (o *backupOrchestrator) dataDir() (string, error) {
	if o.base != "" {
		return o.base, nil
	}
	base, err := xdg.DataDir(filepath.Join("hop", o.tool))
	if err != nil {
		return "", fmt.Errorf("resolve data dir: %w", err)
	}
	return base, nil
}

// backupDir returns the backup directory for a specific version.
// Path: xdg.DataDir("hop/<tool>")/backups/<schema>/pre-<version>/
func (o *backupOrchestrator) backupDir(version string) (string, error) {
	base, err := o.dataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "backups", o.schema, "pre-"+version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	return dir, nil
}

// preRunSuffix names the file beside a backup directory that holds the
// version the run was at when the backup was taken: pre-<v>.version.
const preRunSuffix = ".version"

// savePreRun records, beside the backup directory, the version the run
// that took it started at ("" is recorded as an empty file).
func savePreRun(backupPath, version string) error {
	if err := os.WriteFile(backupPath+preRunSuffix, []byte(version), 0o600); err != nil {
		return fmt.Errorf("write pre-run version: %w", err)
	}
	return nil
}

// readPreRun returns the version saved beside backupPath; recorded is
// false for a backup taken before it was saved.
func readPreRun(backupPath string) (version string, recorded bool, err error) {
	data, err := os.ReadFile(backupPath + preRunSuffix)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read pre-run version: %w", err)
	}
	return strings.TrimSpace(string(data)), true, nil
}

// latestBackup returns the path to the most recent backup directory.
func (o *backupOrchestrator) latestBackup() (string, error) {
	base, err := o.dataDir()
	if err != nil {
		return "", err
	}
	schemaDir := filepath.Join(base, "backups", o.schema)
	entries, err := os.ReadDir(schemaDir)
	if err != nil {
		return "", fmt.Errorf("read backup dir: %w", err)
	}

	type versionedDir struct {
		name string
		ver  semver
	}
	var dirs []versionedDir
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "pre-") {
			continue
		}
		raw := strings.TrimPrefix(e.Name(), "pre-")
		raw = strings.TrimPrefix(raw, "v") // handle "pre-v1.2.3" dirs
		v := parseSemver(raw)
		dirs = append(dirs, versionedDir{name: e.Name(), ver: v})
	}
	if len(dirs) == 0 {
		return "", fmt.Errorf("no backups found for %s", o.schema)
	}
	sort.Slice(dirs, func(i, j int) bool {
		return compareSemver(dirs[i].ver, dirs[j].ver) > 0
	})
	return filepath.Join(schemaDir, dirs[0].name), nil
}

// prune keeps only the N most recent backups (by semver), removing the rest.
func (o *backupOrchestrator) prune() error {
	base, err := o.dataDir()
	if err != nil {
		return err
	}
	schemaDir := filepath.Join(base, "backups", o.schema)

	entries, err := os.ReadDir(schemaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read backup dir: %w", err)
	}

	// Collect dirs with "pre-" prefix.
	type versionedDir struct {
		name string
		ver  semver
	}
	var dirs []versionedDir
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "pre-") {
			continue
		}
		raw := strings.TrimPrefix(name, "pre-")
		raw = strings.TrimPrefix(raw, "v") // handle "pre-v1.2.3" dirs
		v := parseSemver(raw)
		dirs = append(dirs, versionedDir{name: name, ver: v})
	}

	// Sort ascending by semver.
	sort.Slice(dirs, func(i, j int) bool {
		return compareSemver(dirs[i].ver, dirs[j].ver) < 0
	})

	// Remove oldest, keeping retention count.
	if len(dirs) <= o.retention {
		return nil
	}
	toRemove := dirs[:len(dirs)-o.retention]
	for _, d := range toRemove {
		p := filepath.Join(schemaDir, d.name)
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("prune backup %s: %w", d.name, err)
		}
		if err := os.Remove(p + preRunSuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("prune backup %s: %w", d.name, err)
		}
	}
	return nil
}
