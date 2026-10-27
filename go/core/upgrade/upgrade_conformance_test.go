package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Runs the shared corpus in contracts/upgrade-v1. The vectors are the
// source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

const upgradeConfDir = "../../../contracts/upgrade-v1"

// upgradeConfFiles is every vector file this runner consumes; the
// corpus directory must match it exactly.
var upgradeConfFiles = map[string]bool{"versions.json": true, "migrator.json": true}

const upgradeConfTool = "conftool"

type upgradeConfHeader struct {
	Schema      string `json:"$schema"`
	ID          string `json:"$id"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

type upgradeConfVersions struct {
	upgradeConfHeader
	Compare []struct {
		Name string `json:"name"`
		A    string `json:"a"`
		B    string `json:"b"`
		Cmp  int    `json:"cmp"`
	} `json:"compare"`
	Pending []struct {
		Name       string `json:"name"`
		Migrations []struct {
			Schema  string `json:"schema"`
			Version string `json:"version"`
		} `json:"migrations"`
		Schema  string   `json:"schema"`
		From    string   `json:"from"`
		To      string   `json:"to"`
		Pending []string `json:"pending"`
	} `json:"pending"`
}

type upgradeConfDriver struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	BackupFail     string `json:"backup_fail"`
	RestoreFail    string `json:"restore_fail"`
	VersionFail    string `json:"version_fail"`
	SetVersionFail string `json:"set_version_fail"`
}

type upgradeConfMigration struct {
	Schema     string `json:"schema"`
	Version    string `json:"version"`
	Fail       string `json:"fail"`
	Down       bool   `json:"down"`
	DownFail   string `json:"down_fail"`
	Verify     bool   `json:"verify"`
	VerifyFail string `json:"verify_fail"`
	Cancel     bool   `json:"cancel"`
}

type upgradeConfEvent struct {
	Topic   string         `json:"topic"`
	Payload map[string]any `json:"payload"`
}

type upgradeConfStatus struct {
	Schema  string  `json:"schema"`
	Current string  `json:"current"`
	Target  string  `json:"target"`
	Pending int     `json:"pending"`
	Error   *string `json:"error"`
}

type upgradeConfStep struct {
	Op       string               `json:"op"`
	Canceled bool                 `json:"canceled"`
	Events   *[]upgradeConfEvent  `json:"events"`
	Warnings []string             `json:"warnings"`
	Error    *string              `json:"error"`
	Calls    *[]string            `json:"calls"`
	History  *[]AppliedMigration  `json:"history"`
	Versions map[string]string    `json:"versions"`
	Backups  map[string][]string  `json:"backups"`
	Status   *[]upgradeConfStatus `json:"status"`
}

type upgradeConfMigrator struct {
	upgradeConfHeader
	Scenarios []struct {
		Name              string                 `json:"name"`
		Target            string                 `json:"target"`
		ManualRollback    bool                   `json:"manual_rollback"`
		Retention         int                    `json:"retention"`
		Drivers           []upgradeConfDriver    `json:"drivers"`
		Migrations        []upgradeConfMigration `json:"migrations"`
		Backups           map[string][]string    `json:"backups"`
		LockedBackups     map[string][]string    `json:"locked_backups"`
		FinalVerify       bool                   `json:"final_verify"`
		FinalVerifyFail   string                 `json:"final_verify_fail"`
		DataDir           bool                   `json:"data_dir"`
		Registry          string                 `json:"registry"`
		DefaultMigrations []upgradeConfMigration `json:"default_migrations"`
		Steps             []upgradeConfStep      `json:"steps"`
	} `json:"scenarios"`
}

func loadUpgradeConf(t *testing.T, name string, into any) {
	t.Helper()
	dir := filepath.FromSlash(upgradeConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && !upgradeConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var h upgradeConfHeader
	_ = json.Unmarshal(raw, &h)
	if h.Version != "v1" {
		t.Fatalf("%s: version = %q, want v1", name, h.Version)
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func TestConformanceUpgradeCompare(t *testing.T) {
	var f upgradeConfVersions
	loadUpgradeConf(t, "versions.json", &f)
	if len(f.Compare) == 0 {
		t.Fatal("compare section is empty")
	}
	for _, c := range f.Compare {
		a := parseSemver(strings.TrimPrefix(c.A, "v"))
		b := parseSemver(strings.TrimPrefix(c.B, "v"))
		if got := sign(compareSemver(a, b)); got != c.Cmp {
			t.Errorf("%s: compare(%q, %q) = %d, want %d", c.Name, c.A, c.B, got, c.Cmp)
		}
		if got := sign(compareSemver(b, a)); got != -c.Cmp {
			t.Errorf("%s: compare(%q, %q) = %d, want %d", c.Name, c.B, c.A, got, -c.Cmp)
		}
	}
}

func TestConformanceUpgradePending(t *testing.T) {
	var f upgradeConfVersions
	loadUpgradeConf(t, "versions.json", &f)
	if len(f.Pending) == 0 {
		t.Fatal("pending section is empty")
	}
	noop := func(context.Context) error { return nil }
	for _, c := range f.Pending {
		resetRegistry()
		for _, m := range c.Migrations {
			RegisterMigration(Migration{Version: m.Version, Schema: m.Schema, Up: noop})
		}
		got := []string{}
		for _, m := range pendingMigrations(c.Schema, c.From, c.To) {
			got = append(got, m.Version)
		}
		if !reflect.DeepEqual(got, c.Pending) {
			t.Errorf("%s: pending = %q, want %q", c.Name, got, c.Pending)
		}
	}
	resetRegistry()
}

// recordingDriver logs every state-changing call into a log it shares
// with the scenario's migrations.
type recordingDriver struct {
	upgradeConfDriver
	record func(string)
}

type publisherFunc func(ctx context.Context, topic, source string, payload any) error

func (f publisherFunc) Publish(ctx context.Context, topic, source string, payload any) error {
	return f(ctx, topic, source, payload)
}

func failIf(msg string) error {
	if msg == "" {
		return nil
	}
	return errors.New(msg)
}

func (d *recordingDriver) Name() string { return d.upgradeConfDriver.Name }

func (d *recordingDriver) Version() (string, error) {
	if d.VersionFail != "" {
		return "", errors.New(d.VersionFail)
	}
	return d.upgradeConfDriver.Version, nil
}

func (d *recordingDriver) SetVersion(v string) error {
	d.record(d.Name() + " set_version " + v)
	if d.SetVersionFail != "" {
		return errors.New(d.SetVersionFail)
	}
	d.upgradeConfDriver.Version = v
	return nil
}

func (d *recordingDriver) Backup(dest string) error {
	d.record(d.Name() + " backup " + filepath.Base(dest))
	return failIf(d.BackupFail)
}

func (d *recordingDriver) Restore(src string) error {
	d.record(d.Name() + " restore " + filepath.Base(src))
	return failIf(d.RestoreFail)
}

func listBackups(t *testing.T, base, schema string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(base, "backups", schema))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}
	}
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestConformanceUpgradeMigrator(t *testing.T) {
	var f upgradeConfMigrator
	loadUpgradeConf(t, "migrator.json", &f)
	if len(f.Scenarios) == 0 {
		t.Fatal("scenarios section is empty")
	}
	for _, sc := range f.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			resetRegistry()
			defer resetRegistry()
			tmp := t.TempDir()
			t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "xdg"))
			xdgBase := filepath.Join(tmp, "xdg", "hop", upgradeConfTool)
			base := xdgBase
			if sc.DataDir {
				base = filepath.Join(tmp, "custom")
			}

			mkBackups := func(m map[string][]string, locked bool) {
				for schema, dirs := range m {
					if err := os.MkdirAll(filepath.Join(base, "backups", schema), 0o750); err != nil {
						t.Fatal(err)
					}
					for _, d := range dirs {
						dir := filepath.Join(base, "backups", schema, d)
						if !locked {
							if err := os.MkdirAll(dir, 0o750); err != nil {
								t.Fatal(err)
							}
							continue
						}
						inner := filepath.Join(dir, "locked")
						if err := os.MkdirAll(inner, 0o750); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(inner, "f"), nil, 0o600); err != nil {
							t.Fatal(err)
						}
						if err := os.Chmod(inner, 0o500); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = os.Chmod(inner, 0o750) })
					}
				}
			}
			mkBackups(sc.Backups, false)
			mkBackups(sc.LockedBackups, true)

			var mu sync.Mutex
			var calls []string
			record := func(e string) { mu.Lock(); calls = append(calls, e); mu.Unlock() }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			build := func(mc upgradeConfMigration) Migration {
				mig := Migration{Version: mc.Version, Schema: mc.Schema,
					Up: func(context.Context) error {
						record(mc.Schema + " up " + mc.Version)
						if mc.Cancel {
							cancel()
						}
						return failIf(mc.Fail)
					},
				}
				if mc.Down || mc.DownFail != "" {
					mig.Down = func(context.Context) error {
						record(mc.Schema + " down " + mc.Version)
						return failIf(mc.DownFail)
					}
				}
				if mc.Verify || mc.VerifyFail != "" {
					mig.Verify = func(context.Context) error {
						record(mc.Schema + " verify " + mc.Version)
						return failIf(mc.VerifyFail)
					}
				}
				return mig
			}

			var opts []MigratorOption
			reg := DefaultRegistry()
			if sc.Registry == "own" {
				reg = NewRegistry()
				opts = append(opts, WithRegistry(reg))
				for _, mc := range sc.DefaultMigrations {
					RegisterMigration(build(mc))
				}
			}
			for _, mc := range sc.Migrations {
				reg.Register(build(mc))
			}
			if sc.ManualRollback {
				opts = append(opts, WithManualRollback())
			}
			if sc.Retention != 0 {
				opts = append(opts, WithBackupRetention(sc.Retention))
			}
			if sc.DataDir {
				opts = append(opts, WithDataDir(base))
			}
			if sc.FinalVerify || sc.FinalVerifyFail != "" {
				opts = append(opts, WithVerify(func(_ context.Context, schema, version string) error {
					record(schema + " final_verify " + version)
					return failIf(sc.FinalVerifyFail)
				}))
			}
			var events []upgradeConfEvent
			opts = append(opts, WithEventPublisher(publisherFunc(func(_ context.Context, topic, source string, payload any) error {
				if source != "core.upgrade" {
					t.Errorf("source = %q", source)
				}
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				var m map[string]any
				if err := json.Unmarshal(raw, &m); err != nil {
					t.Fatal(err)
				}
				if d, ok := m["duration_ms"]; ok {
					if f, isNum := d.(float64); !isNum || f < 0 || f != float64(int64(f)) {
						t.Errorf("%s: duration_ms = %v", topic, d)
					}
					delete(m, "duration_ms")
				}
				events = append(events, upgradeConfEvent{Topic: topic, Payload: m})
				return nil
			})))
			m := NewMigrator(upgradeConfTool, sc.Target, opts...)
			for _, dc := range sc.Drivers {
				m.AddDriver(&recordingDriver{upgradeConfDriver: dc, record: record})
			}

			var logBuf bytes.Buffer
			log.SetOutput(&logBuf)
			flags := log.Flags()
			log.SetFlags(0)
			defer func() { log.SetOutput(os.Stderr); log.SetFlags(flags) }()

			for i, st := range sc.Steps {
				calls, events = nil, nil
				logBuf.Reset()
				runCtx := ctx
				if st.Canceled {
					c, stop := context.WithCancel(ctx)
					stop()
					runCtx = c
				}
				var err error
				switch st.Op {
				case "run":
					err = m.Run(runCtx)
				case "rollback_latest":
					err = m.RollbackLatest()
				case "status":
					got := []upgradeConfStatus{}
					for _, s := range m.Status() {
						cs := upgradeConfStatus{Schema: s.Schema, Current: s.Current, Target: s.Target, Pending: s.Pending}
						if s.Err != nil {
							msg := s.Err.Error()
							cs.Error = &msg
						}
						got = append(got, cs)
					}
					if st.Status == nil {
						t.Fatalf("step %d: status op without status", i)
					}
					if !reflect.DeepEqual(got, *st.Status) {
						t.Errorf("step %d: status = %+v, want %+v", i, got, *st.Status)
					}
				default:
					t.Fatalf("step %d: unknown op %q", i, st.Op)
				}

				switch {
				case st.Error == nil && err != nil:
					t.Errorf("step %d: unexpected error %q", i, err)
				case st.Error != nil && err == nil:
					t.Errorf("step %d: no error, want %q", i, *st.Error)
				case st.Error != nil && err.Error() != *st.Error:
					t.Errorf("step %d: error %q, want %q", i, err, *st.Error)
				}
				if st.Calls != nil {
					got := append([]string{}, calls...)
					if !reflect.DeepEqual(got, *st.Calls) {
						t.Errorf("step %d: calls = %q, want %q", i, got, *st.Calls)
					}
				}
				if st.History != nil {
					got := append([]AppliedMigration{}, m.History()...)
					if !reflect.DeepEqual(got, *st.History) {
						t.Errorf("step %d: history = %+v, want %+v", i, got, *st.History)
					}
				}
				if st.Events != nil {
					got := append([]upgradeConfEvent{}, events...)
					if !reflect.DeepEqual(got, *st.Events) {
						gj, _ := json.Marshal(got)
						wj, _ := json.Marshal(*st.Events)
						t.Errorf("step %d: events =\n%s\nwant\n%s", i, gj, wj)
					}
				}
				if st.Warnings != nil {
					lines := strings.Split(strings.TrimRight(logBuf.String(), "\n"), "\n")
					if logBuf.Len() == 0 {
						lines = nil
					}
					if len(lines) != len(st.Warnings) {
						t.Errorf("step %d: logged %q, want prefixes %q", i, lines, st.Warnings)
					} else {
						for j, w := range st.Warnings {
							if !strings.HasPrefix(lines[j], w) {
								t.Errorf("step %d: logged %q, want prefix %q", i, lines[j], w)
							}
						}
					}
				}
				for schema, want := range st.Versions {
					got, err := ReadVersionFileIn(base, schema)
					if err != nil {
						t.Fatal(err)
					}
					if got != want {
						t.Errorf("step %d: %s version file = %q, want %q", i, schema, got, want)
					}
				}
				for schema, want := range st.Backups {
					if got := listBackups(t, base, schema); !reflect.DeepEqual(got, want) {
						t.Errorf("step %d: %s backups = %q, want %q", i, schema, got, want)
					}
				}
				if sc.DataDir {
					if _, err := os.Stat(xdgBase); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("step %d: XDG data dir touched despite WithDataDir: %v", i, err)
					}
				}
			}
		})
	}
}
