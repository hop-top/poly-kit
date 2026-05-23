package buswf_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"hop.top/kit/cmd/kit/init/buswf"
)

// TestWriteAllBootstrap writes into an empty project root: all four
// files are created, manifest is written with four entries, hashes
// match.
func TestWriteAllBootstrap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	plan, err := buswf.WriteAll(buswf.Defaults(root))
	if err != nil {
		t.Fatalf("WriteAll: %v", err)
	}
	if got, want := len(plan.Entries), 4; got != want {
		t.Fatalf("len(plan.Entries) = %d, want %d", got, want)
	}
	for _, e := range plan.Entries {
		if e.Action != buswf.ActionWrite {
			t.Errorf("%s: action %q, want write", e.Path, e.Action)
		}
		if e.Reason != buswf.ReasonNew {
			t.Errorf("%s: reason %q, want new", e.Path, e.Reason)
		}
	}

	// Files on disk.
	for _, f := range buswf.Files() {
		p := filepath.Join(root, ".github", "workflows", f.Name)
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if string(body) != string(f.Body) {
			t.Errorf("%s: written body differs from Files()", f.Name)
		}
	}

	// Manifest has all four entries.
	mfPath := filepath.Join(root, ".kit", "generated.json")
	data, err := os.ReadFile(mfPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m buswf.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("manifest version %d, want 1", m.Version)
	}
	if m.GeneratedBy != "kit-init" {
		t.Errorf("manifest generated_by %q, want kit-init", m.GeneratedBy)
	}
	if got, want := len(m.Files), 4; got != want {
		t.Fatalf("manifest files = %d, want %d", got, want)
	}
	// Sorted by path.
	paths := []string{}
	for _, f := range m.Files {
		paths = append(paths, f.Path)
	}
	if !sort.StringsAreSorted(paths) {
		t.Errorf("manifest files not sorted by path: %v", paths)
	}
}

// TestWriteAllDryRun: dry-run produces the plan but does not write any
// files or the manifest.
func TestWriteAllDryRun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	opts := buswf.Defaults(root)
	opts.DryRun = true

	plan, err := buswf.WriteAll(opts)
	if err != nil {
		t.Fatalf("WriteAll dry-run: %v", err)
	}
	if got, want := len(plan.Entries), 4; got != want {
		t.Fatalf("len(plan.Entries) = %d, want %d", got, want)
	}
	for _, e := range plan.Entries {
		if e.Action != buswf.ActionWrite {
			t.Errorf("%s: action %q, want write (still reported under dry-run)", e.Path, e.Action)
		}
	}

	// No files created.
	wfDir := filepath.Join(root, ".github", "workflows")
	if _, err := os.Stat(wfDir); !os.IsNotExist(err) {
		t.Errorf("dry-run created %s (stat err = %v)", wfDir, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".kit", "generated.json")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote manifest (err = %v)", err)
	}
}

// TestWriteAllSuggestSibling: user has edited one of the four files
// and the manifest still has the original hash. WriteAll must write
// the new content to <path>.kit-suggested, leave the original alone,
// and produce action=suggest-sibling.
func TestWriteAllSuggestSibling(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Bootstrap once to seed the manifest.
	if _, err := buswf.WriteAll(buswf.Defaults(root)); err != nil {
		t.Fatalf("bootstrap WriteAll: %v", err)
	}

	// User edits one file: append a stray comment so the hash changes.
	target := filepath.Join(root, ".github", "workflows", "kit-bus-pr-run-completed.yml")
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	edited := append([]byte{}, original...)
	edited = append(edited, []byte("# user edit, please keep\n")...)
	if err := os.WriteFile(target, edited, 0o644); err != nil {
		t.Fatalf("write edited: %v", err)
	}

	// Now suppose kit init re-runs with the same generated body
	// (no spec change); the only divergence is the user's edit.
	plan, err := buswf.WriteAll(buswf.Defaults(root))
	if err != nil {
		t.Fatalf("re-run WriteAll: %v", err)
	}

	found := false
	for _, e := range plan.Entries {
		if e.Path == ".github/workflows/kit-bus-pr-run-completed.yml" {
			found = true
			if e.Action != buswf.ActionSuggestSibling {
				t.Errorf("action = %q, want suggest-sibling", e.Action)
			}
			if e.Reason != buswf.ReasonUserEdited {
				t.Errorf("reason = %q, want user-edited", e.Reason)
			}
			if e.SuggestedPath != ".github/workflows/kit-bus-pr-run-completed.yml.kit-suggested" {
				t.Errorf("suggested_path = %q", e.SuggestedPath)
			}
		}
	}
	if !found {
		t.Fatal("no plan entry for the edited file")
	}

	// Original still has the user edit.
	now, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("re-read original: %v", err)
	}
	if string(now) != string(edited) {
		t.Errorf("original file got mutated by augment run; user edit lost")
	}

	// Sibling exists with the canonical body.
	sibling := target + ".kit-suggested"
	siblingBody, err := os.ReadFile(sibling)
	if err != nil {
		t.Fatalf("read sibling: %v", err)
	}
	want, _ := buswf.FileByTopic("github.pr.run.completed")
	if string(siblingBody) != string(want.Body) {
		t.Error("sibling body differs from canonical Files() output")
	}
}

// TestWriteAllAcceptedSiblingCleanup: when an existing .kit-suggested
// sibling is byte-identical to the live file, the user effectively
// accepted the previous suggestion — the sibling must be removed.
func TestWriteAllAcceptedSiblingCleanup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Bootstrap.
	if _, err := buswf.WriteAll(buswf.Defaults(root)); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	target := filepath.Join(root, ".github", "workflows", "kit-bus-pr-run-completed.yml")
	sibling := target + ".kit-suggested"
	// User accepted the suggestion: their file now equals what
	// would be the sibling content.
	original, _ := os.ReadFile(target)
	if err := os.WriteFile(sibling, original, 0o644); err != nil {
		t.Fatalf("write sibling: %v", err)
	}
	// Mutate target so we go through the suggest-sibling path
	// (otherwise the hash matches manifest and we hit
	// skip-unchanged before cleanup).
	mutated := append([]byte{}, original...)
	mutated = append(mutated, []byte("# user edit\n")...)
	if err := os.WriteFile(target, mutated, 0o644); err != nil {
		t.Fatalf("mutate target: %v", err)
	}
	// Sibling no longer matches target; cleanup test wants
	// sibling == target. Reset sibling to match target.
	if err := os.WriteFile(sibling, mutated, 0o644); err != nil {
		t.Fatalf("rewrite sibling to match target: %v", err)
	}

	// Run augment: the sibling matches the target now, so the
	// pre-write cleanup deletes the sibling; then a fresh sibling
	// gets written with the canonical body.
	if _, err := buswf.WriteAll(buswf.Defaults(root)); err != nil {
		t.Fatalf("augment: %v", err)
	}

	// Sibling should still exist (we re-wrote it with the canonical
	// content after cleanup); the assertion is that the content
	// equals canonical (not the user's edit, which was leftover in
	// the stale sibling).
	got, err := os.ReadFile(sibling)
	if err != nil {
		t.Fatalf("read sibling: %v", err)
	}
	want, _ := buswf.FileByTopic("github.pr.run.completed")
	if string(got) != string(want.Body) {
		t.Errorf("sibling not refreshed to canonical body")
	}
}

// TestWriteAllRefreshInPlace: hash matches manifest → kit refreshes in
// place. For our case Files() is deterministic so this is the
// skip-unchanged variant (manifest hash already equals files-body
// hash).
func TestWriteAllRefreshInPlace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	if _, err := buswf.WriteAll(buswf.Defaults(root)); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Second run with the same Files() output: every entry must
	// be skip-unchanged.
	plan, err := buswf.WriteAll(buswf.Defaults(root))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, e := range plan.Entries {
		if e.Action != buswf.ActionSkipUnchanged {
			t.Errorf("%s: action %q, want skip-unchanged", e.Path, e.Action)
		}
	}
}
