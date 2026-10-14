package sqlstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Naming decides the file names BackupBeforeMigrate gives backups and
// recognizes them again, so WithKeep deletes only backups it made.
type Naming interface {
	// Name returns the file name (no directory) for a backup of dbPath
	// before migrating to version, taken at taken (UTC). attempt starts
	// at 0 and grows while the returned name is already taken; each
	// attempt must yield a distinct name.
	Name(dbPath string, version int, taken time.Time, attempt int) string

	// Parse reports whether fileName is a backup of dbPath that Name
	// produced, and when it was taken and at which attempt. WithKeep
	// keeps the newest by taken, then attempt.
	Parse(dbPath, fileName string) (taken time.Time, attempt int, ok bool)
}

// defaultTimeLayout is the UTC timestamp in a default backup name.
const defaultTimeLayout = "20060102-150405"

// defaultNaming names backups <basename>.pre-v<version>.<ts>.bak, with
// .<attempt> before .bak when attempt > 0.
type defaultNaming struct{}

func (defaultNaming) Name(dbPath string, version int, taken time.Time, attempt int) string {
	n := fmt.Sprintf("%s.pre-v%d.%s", dbStem(dbPath), version, taken.UTC().Format(defaultTimeLayout))
	if attempt > 0 {
		n += "." + strconv.Itoa(attempt)
	}
	return n + ".bak"
}

func (defaultNaming) Parse(dbPath, fileName string) (time.Time, int, bool) {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(dbStem(dbPath)) +
		`\.pre-v\d+\.(\d{8}-\d{6})(?:\.(\d+))?\.bak$`)
	m := re.FindStringSubmatch(fileName)
	if m == nil {
		return time.Time{}, 0, false
	}
	taken, err := time.Parse(defaultTimeLayout, m[1])
	if err != nil {
		return time.Time{}, 0, false
	}
	attempt := 0
	if m[2] != "" {
		if attempt, err = strconv.Atoi(m[2]); err != nil {
			return time.Time{}, 0, false
		}
	}
	return taken, attempt, true
}

// dbStem is the database file's base name without its extension.
func dbStem(dbPath string) string {
	base := filepath.Base(dbPath)
	return base[:len(base)-len(filepath.Ext(base))]
}

// freeBackupName returns the first path in dir the Naming yields for
// this backup that no file holds yet.
func freeBackupName(dir string, n Naming, dbPath string, version int, taken time.Time) (string, error) {
	for attempt := 0; attempt < maxNameAttempts; attempt++ {
		name := n.Name(dbPath, version, taken, attempt)
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
			return "", fmt.Errorf("invalid backup name %q: want a plain file name", name)
		}
		p := filepath.Join(dir, name)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p, nil
		} else if err != nil {
			return "", fmt.Errorf("stat %s: %w", p, err)
		}
	}
	return "", fmt.Errorf("no free backup name in %s after %d attempts", dir, maxNameAttempts)
}

// pruneBackups deletes all but the newest cfg.keep backups of dbPath in
// dir. Only regular files the Naming recognizes are counted or deleted.
func pruneBackups(dir string, cfg backupConfig, dbPath string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type backup struct {
		name    string
		taken   time.Time
		attempt int
	}
	var backups []backup
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		taken, attempt, ok := cfg.naming.Parse(dbPath, e.Name())
		if !ok {
			continue
		}
		backups = append(backups, backup{name: e.Name(), taken: taken, attempt: attempt})
	}
	// Newest first.
	sort.Slice(backups, func(i, j int) bool {
		a, b := backups[i], backups[j]
		if !a.taken.Equal(b.taken) {
			return a.taken.After(b.taken)
		}
		if a.attempt != b.attempt {
			return a.attempt > b.attempt
		}
		return a.name > b.name
	})
	var errs []error
	for i := cfg.keep; i < len(backups); i++ {
		if err := cfg.remove(filepath.Join(dir, backups[i].name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
