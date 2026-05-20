package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// withFreshXDG points XDG_STATE_HOME at a fresh t.TempDir for the
// duration of the test. The adrg/xdg lib calls Reload() inside every
// xdg.StateFile call (via our wrapper), so no cache-purge dance is
// required — env-var changes take effect on the next resolve.
func withFreshXDG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	// Defense-in-depth: clear adjacent vars that adrg/xdg may consult
	// for fallback resolution on some platforms.
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "_data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "_cache"))
	return dir
}

func TestInstallationID_FirstRunGenerates(t *testing.T) {
	stateHome := withFreshXDG(t)

	first, err := InstallationID()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("hex length: got %d, want 64", len(first))
	}
	if first != strings.ToLower(first) {
		t.Fatalf("hex must be lowercase: %q", first)
	}

	second, err := InstallationID()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first != second {
		t.Fatalf("id changed between calls: %q vs %q", first, second)
	}

	path, err := InstallIDPath()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if !strings.HasPrefix(path, stateHome) {
		t.Fatalf("path %q not under XDG_STATE_HOME %q", path, stateHome)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != int64(installIDSize) {
		t.Fatalf("size: got %d, want %d", info.Size(), installIDSize)
	}
	if perm := info.Mode().Perm(); perm != installIDFilePerm {
		t.Fatalf("file perm: got %o, want %o", perm, installIDFilePerm)
	}

	parentInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if perm := parentInfo.Mode().Perm(); perm != installIDDirPerm {
		t.Fatalf("parent perm: got %o, want %o", perm, installIDDirPerm)
	}
}

func TestInstallationID_StableAcrossCalls(t *testing.T) {
	withFreshXDG(t)

	want, err := InstallationID()
	if err != nil {
		t.Fatalf("seed call: %v", err)
	}
	for i := 0; i < 10; i++ {
		got, err := InstallationID()
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("call %d returned %q, want %q", i, got, want)
		}
	}
}

func TestRotate_ProducesDifferentID(t *testing.T) {
	withFreshXDG(t)

	before, err := InstallationID()
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	after, err := Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if before == after {
		t.Fatalf("Rotate returned same hex: %q", after)
	}

	// Confirm subsequent reads see the rotated value.
	post, err := InstallationID()
	if err != nil {
		t.Fatalf("post-rotate read: %v", err)
	}
	if post != after {
		t.Fatalf("rotated id not persisted: read %q, expected %q", post, after)
	}

	path, _ := InstallIDPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat post-rotate: %v", err)
	}
	if info.Size() != int64(installIDSize) {
		t.Fatalf("size post-rotate: got %d, want %d", info.Size(), installIDSize)
	}
}

func TestConcurrentFirstRun(t *testing.T) {
	withFreshXDG(t)

	const n = 8
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			id, err := InstallationID()
			results[idx] = id
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	want := results[0]
	if want == "" || len(want) != 64 {
		t.Fatalf("invalid hex from goroutine 0: %q", want)
	}
	for i, got := range results {
		if got != want {
			t.Fatalf("goroutine %d saw %q, expected %q (race converged wrong)", i, got, want)
		}
	}
}

func TestMalformedFile_ReturnsError(t *testing.T) {
	withFreshXDG(t)

	path, err := InstallIDPath()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	// InstallIDPath ensures the parent dir exists via xdg.StateFile;
	// write a short file to simulate corruption/operator sentinel.
	if err := os.WriteFile(path, []byte("only-sixteen!!!!"), installIDFilePerm); err != nil {
		t.Fatalf("seed malformed: %v", err)
	}

	_, err = InstallationID()
	if err == nil {
		t.Fatalf("expected error for malformed file")
	}
	if !strings.Contains(err.Error(), "size") {
		t.Fatalf("error %q should mention 'size'", err.Error())
	}
}

func TestInstallIDPath(t *testing.T) {
	stateHome := withFreshXDG(t)

	path, err := InstallIDPath()
	if err != nil {
		t.Fatalf("InstallIDPath: %v", err)
	}
	wantSuffix := filepath.Join("kit", "telemetry", "installation_id")
	if !strings.HasSuffix(path, wantSuffix) {
		t.Fatalf("path %q does not end with %q", path, wantSuffix)
	}
	if !strings.HasPrefix(path, stateHome) {
		t.Fatalf("path %q not under XDG_STATE_HOME %q", path, stateHome)
	}
}

func TestResetForTest(t *testing.T) {
	withFreshXDG(t)

	first, err := InstallationID()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := ResetForTest(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	path, _ := InstallIDPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed after ResetForTest, stat err = %v", err)
	}

	second, err := InstallationID()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second == first {
		t.Fatalf("expected new id after reset; got same %q", second)
	}

	// ResetForTest on a missing file is a no-op (idempotent).
	if err := ResetForTest(); err != nil {
		t.Fatalf("reset idempotent: %v", err)
	}
}
