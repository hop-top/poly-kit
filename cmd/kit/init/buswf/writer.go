// Augment-mode writer for the kit-bus workflow files. Implements the
// conflict policy pinned in docs/contracts/kit-init-pr-wiring.md §6:
//
//   - file absent on disk         → write, manifest entry
//   - on-disk hash matches manifest → write (refresh in place), manifest entry
//   - on-disk hash differs from manifest, OR file present without
//     manifest entry → write to <path>.kit-suggested sibling, leave
//     original untouched
//   - byte-identical .kit-suggested sibling → remove (the user
//     effectively accepted)
//
// Dry-run mirrors the same decision shape but does no writes.
package buswf

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Action is the dry-run / report verb the writer would (or did) take
// for a single file. Values mirror the spec wording so JSON output
// shows the contract phrases directly.
type Action string

const (
	ActionWrite          Action = "write"
	ActionSkipUnchanged  Action = "skip-unchanged"
	ActionSuggestSibling Action = "suggest-sibling"
	ActionManifestUpdate Action = "manifest-update"
)

// Reason annotates an Action with the "why" (spec §6 dry-run JSON).
type Reason string

const (
	ReasonNew          Reason = "new"
	ReasonRefresh      Reason = "refresh"
	ReasonUserEdited   Reason = "user-edited"
	ReasonManifestOnly Reason = "manifest-only"
)

// PlanEntry is what dry-run reports per file. It mirrors the §6 dry-run
// JSON shape exactly so callers can serialise it without translation.
type PlanEntry struct {
	Path          string `json:"path"`
	Action        Action `json:"action"`
	SuggestedPath string `json:"suggested_path,omitempty"`
	Reason        Reason `json:"reason"`
}

// Plan describes the would-be effect of WriteAll on the working tree
// (one PlanEntry per generated file). NewManifest is the manifest the
// writer would persist when DryRun is false.
type Plan struct {
	Entries     []PlanEntry
	NewManifest Manifest
}

// WriteOpts toggles dry-run and points at the workflow target root.
// Root is the project root (e.g. cwd); WorkflowDir is appended to get
// the actual write dir. Both default-form callers can use Defaults().
type WriteOpts struct {
	Root        string
	WorkflowDir string // typically ".github/workflows"
	DryRun      bool
}

// Defaults returns a WriteOpts pointing at .github/workflows under
// root, with DryRun=false.
func Defaults(root string) WriteOpts {
	return WriteOpts{
		Root:        root,
		WorkflowDir: ".github/workflows",
		DryRun:      false,
	}
}

// WriteAll renders and applies the four kit-bus workflow files
// according to the augment policy. Returns the Plan describing the
// outcome (or would-be outcome under DryRun).
func WriteAll(opts WriteOpts) (Plan, error) {
	if opts.Root == "" {
		return Plan{}, fmt.Errorf("buswf: WriteAll: Root is required")
	}
	if opts.WorkflowDir == "" {
		opts.WorkflowDir = ".github/workflows"
	}

	manifest, err := ReadManifest(opts.Root)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{}
	updates := []ManifestFile{}
	now := nowUTC()

	for _, f := range Files() {
		rel := filepath.ToSlash(filepath.Join(opts.WorkflowDir, f.Name))
		abs := filepath.Join(opts.Root, filepath.FromSlash(rel))
		hash := SHA256(f.Body)

		entry := PlanEntry{Path: rel}
		mfEntry, hadEntry := manifest.Lookup(rel)
		onDisk, statErr := os.ReadFile(abs)
		fileExists := statErr == nil
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return Plan{}, fmt.Errorf("read %s: %w", abs, statErr)
		}

		switch {
		case !fileExists:
			// New file: write + manifest entry.
			entry.Action = ActionWrite
			entry.Reason = ReasonNew
			if !opts.DryRun {
				if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
					return Plan{}, fmt.Errorf("mkdir %s: %w", filepath.Dir(abs), err)
				}
				if err := os.WriteFile(abs, f.Body, 0o644); err != nil {
					return Plan{}, fmt.Errorf("write %s: %w", abs, err)
				}
			}
			updates = append(updates, ManifestFile{Path: rel, SHA256: hash, GeneratedAt: now})

		case hadEntry && SHA256(onDisk) == mfEntry.SHA256:
			// Hash matches manifest → kit refresh in place.
			if hash == mfEntry.SHA256 {
				// Nothing changed; only the timestamp would tick.
				entry.Action = ActionSkipUnchanged
				entry.Reason = ReasonRefresh
				// Carry forward the existing entry to preserve
				// generatedAt; this is a true no-op refresh.
				updates = append(updates, mfEntry)
			} else {
				entry.Action = ActionWrite
				entry.Reason = ReasonRefresh
				if !opts.DryRun {
					if err := os.WriteFile(abs, f.Body, 0o644); err != nil {
						return Plan{}, fmt.Errorf("write %s: %w", abs, err)
					}
				}
				updates = append(updates, ManifestFile{Path: rel, SHA256: hash, GeneratedAt: now})
			}

		default:
			// Diverged from manifest, OR present without manifest
			// entry → suggest sibling. The original is left
			// untouched and the manifest is not updated for that
			// path (it still reflects what kit-init originally
			// wrote).
			//
			// Distinguish the two "why" sub-cases so the spec §6
			// dry-run JSON reason enum is accurate:
			//
			//   - hadEntry==false && fileExists==true → the manifest
			//     is missing the entry (reason: manifest-only).
			//   - hadEntry==true  && hash differs    → the file was
			//     edited after generation (reason: user-edited).
			suggested := abs + ".kit-suggested"
			suggestedRel := rel + ".kit-suggested"
			entry.Action = ActionSuggestSibling
			entry.SuggestedPath = suggestedRel
			if !hadEntry {
				entry.Reason = ReasonManifestOnly
			} else {
				entry.Reason = ReasonUserEdited
			}

			// Suggestion cleanup: if an existing .kit-suggested is
			// byte-identical to the live file, the user has
			// effectively accepted the previous suggestion — drop
			// the stale sibling. Done before re-writing so the
			// path-on-disk reflects "no more pending suggestion"
			// rather than "stale .kit-suggested replaced by the
			// same body".
			if existing, eerr := os.ReadFile(suggested); eerr == nil {
				if string(existing) == string(onDisk) {
					if !opts.DryRun {
						_ = os.Remove(suggested)
					}
				}
			}

			if !opts.DryRun {
				if err := os.MkdirAll(filepath.Dir(suggested), 0o750); err != nil {
					return Plan{}, fmt.Errorf("mkdir %s: %w", filepath.Dir(suggested), err)
				}
				if err := os.WriteFile(suggested, f.Body, 0o644); err != nil {
					return Plan{}, fmt.Errorf("write %s: %w", suggested, err)
				}
			}
			// Preserve any existing manifest entry untouched.
			if hadEntry {
				updates = append(updates, mfEntry)
			}
		}

		plan.Entries = append(plan.Entries, entry)
	}

	newManifest := manifest.MergeFiles(updates)
	plan.NewManifest = newManifest

	if !opts.DryRun {
		if err := WriteManifest(opts.Root, newManifest); err != nil {
			return Plan{}, err
		}
	}

	return plan, nil
}
