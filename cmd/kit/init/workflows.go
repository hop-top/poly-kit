// Package kitinit — workflows.go renders the consumer-side
// `.github/workflows/*-caller.yml` stubs that reference reusable workflows
// hosted at `hop-top/.github/.github/workflows/<name>.yml@<ref>`.
//
// Implements T-0772. Contract: docs/contracts/kit-init-pr-wiring.md
// (Sections 1, 6, 8). Caller stubs are NEVER inlined copies of reusable
// workflows — every job `uses:` the upstream reference. Generation is
// gated behind `--with-github-workflows` (default true).
//
// Conflict policy (Section 6):
//   - Never overwrite existing files.
//   - If the on-disk hash matches the manifest entry, refresh in place.
//   - Else write a `<path>.kit-suggested` sibling and leave the original
//     untouched. Suggestion cleanup: if an existing `.kit-suggested`
//     sibling now equals the live file, delete the sibling before
//     writing a new one.
//   - Track every generated file in `.kit/generated.json`.
package kitinit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// workflowCallerRef is the default git ref for `uses:` lines pointing at
// hop-top/.github reusable workflows. Pinned to `v0` to match the rest of
// the poly-kit consumers (see .github/workflows/publish.yml). Adopters
// can rebump by editing the rendered caller files.
const workflowCallerRef = "v0"

// manifestRelPath is the canonical location of the kit-init manifest in
// every adopter repo, per Section 6 of the contract.
const manifestRelPath = ".kit/generated.json"

// manifestVersion is the current manifest schema version. Bump only when
// the on-disk JSON shape changes incompatibly.
const manifestVersion = 1

// manifestGeneratedBy identifies the producing tool. Pinned per Section 6.
const manifestGeneratedBy = "kit-init"

// suggestedSuffix is the sibling suffix used when kit init cannot
// overwrite an existing file. Mirrors the engine's existing convention so
// adopters see a single `.kit-suggested` shape across all generators.
const suggestedSuffix = ".kit-suggested"

// Manifest is the on-disk shape of `.kit/generated.json`.
type Manifest struct {
	Version     int             `json:"version"`
	GeneratedBy string          `json:"generated_by"`
	Files       []ManifestEntry `json:"files"`
}

// ManifestEntry tracks one generated file.
type ManifestEntry struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	GeneratedAt string `json:"generatedAt"`
}

// WorkflowAction reports what kit init did (or would do, in dry-run) for
// a single generated path. Schema mirrors Section 6 of the contract.
type WorkflowAction struct {
	Path          string `json:"path"`
	Action        string `json:"action"` // write | skip-unchanged | suggest-sibling | manifest-update
	SuggestedPath string `json:"suggested_path,omitempty"`
	Reason        string `json:"reason,omitempty"` // user-edited | new | refresh | manifest-only | convergence
}

// workflowSpec describes one caller stub to render: the output file name
// (under `.github/workflows/`), the upstream reusable workflow filename
// referenced by `uses:`, and a free-form note rendered as a top-of-file
// comment. `Trigger` distinguishes release-on-tag callers (push:tags)
// from test callers (push/pull_request).
type workflowSpec struct {
	OutFile  string // e.g. "release-go-caller.yml"
	Upstream string // e.g. "publish-on-tag.yml"
	Trigger  string // "release" | "test"
	Notes    []string
	// UpstreamTODO is set when the upstream filename is a best-guess
	// and the reviewer should confirm/pin it. Rendered as a TODO comment.
	UpstreamTODO string
}

// runtimeWorkflows maps each runtime tag (as used by Inputs.Runtime) to
// its release + test caller specs. Names follow the contract:
// `<verb>-<runtime>-caller.yml`. Where a per-runtime reusable workflow
// does not exist upstream (Go, PHP), we point at the unified
// `publish-on-tag.yml` for release and pin a TODO for the reviewer.
//
// Test callers point at `ci.yml` (the only language-agnostic CI
// reusable currently published) and carry a TODO because
// `hop-top/.github` does not yet host a per-language test reusable.
var runtimeWorkflows = map[string][]workflowSpec{
	"go": {
		{
			OutFile:      "release-go-caller.yml",
			Upstream:     "publish-on-tag.yml",
			Trigger:      "release",
			Notes:        []string{"Publishes Go module tags via the unified hop-top/.github publish-on-tag pipeline."},
			UpstreamTODO: "hop-top/.github exposes the unified publish-on-tag.yml; no dedicated publish-go.yml exists yet. Confirm the input shape with the maintainer before opting in.",
		},
		{
			OutFile:      "test-go-caller.yml",
			Upstream:     "ci.yml",
			Trigger:      "test",
			Notes:        []string{"Runs the hop-top/.github ci reusable workflow (actionlint + meta checks)."},
			UpstreamTODO: "hop-top/.github does not yet host a per-language test-go.yml; this caller targets the generic ci.yml. Replace `Upstream` once a dedicated reusable lands.",
		},
	},
	"rs": {
		{
			OutFile:  "release-rs-caller.yml",
			Upstream: "publish-rs.yml",
			Trigger:  "release",
			Notes:    []string{"Publishes the crate via the hop-top/.github publish-rs reusable workflow."},
		},
		{
			OutFile:      "test-rs-caller.yml",
			Upstream:     "ci.yml",
			Trigger:      "test",
			Notes:        []string{"Runs the hop-top/.github ci reusable workflow."},
			UpstreamTODO: "hop-top/.github does not yet host a per-language test-rs.yml; this caller targets the generic ci.yml. Replace `Upstream` once a dedicated reusable lands.",
		},
	},
	"ts": {
		{
			OutFile:  "release-ts-caller.yml",
			Upstream: "publish-ts.yml",
			Trigger:  "release",
			Notes:    []string{"Publishes the npm package via the hop-top/.github publish-ts reusable workflow."},
		},
		{
			OutFile:      "test-ts-caller.yml",
			Upstream:     "ci.yml",
			Trigger:      "test",
			Notes:        []string{"Runs the hop-top/.github ci reusable workflow."},
			UpstreamTODO: "hop-top/.github does not yet host a per-language test-ts.yml; this caller targets the generic ci.yml. Replace `Upstream` once a dedicated reusable lands.",
		},
	},
	"php": {
		{
			OutFile:      "release-php-caller.yml",
			Upstream:     "publish-on-tag.yml",
			Trigger:      "release",
			Notes:        []string{"Publishes the Packagist package via the unified hop-top/.github publish-on-tag pipeline."},
			UpstreamTODO: "hop-top/.github does not yet host a dedicated publish-php.yml; the unified publish-on-tag.yml currently handles PHP via the `php` ecosystem entry. Confirm the input shape with the maintainer before opting in.",
		},
		{
			OutFile:      "test-php-caller.yml",
			Upstream:     "ci.yml",
			Trigger:      "test",
			Notes:        []string{"Runs the hop-top/.github ci reusable workflow."},
			UpstreamTODO: "hop-top/.github does not yet host a per-language test-php.yml; this caller targets the generic ci.yml. Replace `Upstream` once a dedicated reusable lands.",
		},
	},
	"py": {
		{
			OutFile:  "release-py-caller.yml",
			Upstream: "publish-py.yml",
			Trigger:  "release",
			Notes:    []string{"Publishes the PyPI package via the hop-top/.github publish-py reusable workflow."},
		},
		{
			OutFile:      "test-py-caller.yml",
			Upstream:     "ci.yml",
			Trigger:      "test",
			Notes:        []string{"Runs the hop-top/.github ci reusable workflow."},
			UpstreamTODO: "hop-top/.github does not yet host a per-language test-py.yml; this caller targets the generic ci.yml. Replace `Upstream` once a dedicated reusable lands.",
		},
	},
}

// renderWorkflowCaller produces the textual content of a single caller
// stub. The body is deliberately small (a `uses:` line is the whole
// point) so we build it from a string template rather than text/template
// to keep dependencies minimal and rendering deterministic.
func renderWorkflowCaller(spec workflowSpec) string {
	var b strings.Builder
	b.WriteString("# Generated by `kit init`. Edits will not be overwritten;\n")
	b.WriteString("# kit will surface conflicting refreshes as `.kit-suggested` siblings.\n")
	for _, n := range spec.Notes {
		b.WriteString("# ")
		b.WriteString(n)
		b.WriteString("\n")
	}
	if spec.UpstreamTODO != "" {
		b.WriteString("# TODO: ")
		b.WriteString(spec.UpstreamTODO)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	switch spec.Trigger {
	case "release":
		b.WriteString("name: ")
		b.WriteString(strings.TrimSuffix(spec.OutFile, ".yml"))
		b.WriteString("\n\n")
		b.WriteString("on:\n")
		b.WriteString("  push:\n")
		b.WriteString("    tags: ['*/v*', 'v*']\n\n")
	default: // "test" and anything else
		b.WriteString("name: ")
		b.WriteString(strings.TrimSuffix(spec.OutFile, ".yml"))
		b.WriteString("\n\n")
		b.WriteString("on:\n")
		b.WriteString("  pull_request:\n")
		b.WriteString("  push:\n")
		b.WriteString("    branches: [main]\n\n")
	}

	b.WriteString("permissions:\n")
	b.WriteString("  contents: read\n")
	if spec.Trigger == "release" {
		b.WriteString("  id-token: write\n")
	}
	b.WriteString("\n")

	b.WriteString("jobs:\n")
	b.WriteString("  ")
	b.WriteString(strings.ReplaceAll(spec.Trigger, "_", "-"))
	b.WriteString(":\n")
	b.WriteString("    uses: hop-top/.github/.github/workflows/")
	b.WriteString(spec.Upstream)
	b.WriteString("@")
	b.WriteString(workflowCallerRef)
	b.WriteString("\n")
	// Secrets pass-through for release callers — adopters typically need
	// at least one publish token. Use `inherit` so individual secret names
	// stay opaque to the caller stub.
	if spec.Trigger == "release" {
		b.WriteString("    secrets: inherit\n")
	}
	return b.String()
}

// plannedWorkflow is the result of resolving one spec against the
// adopter's repo state — used by both bootstrap and augment paths.
type plannedWorkflow struct {
	RelPath     string // repo-relative POSIX path (e.g. ".github/workflows/release-go-caller.yml")
	AbsPath     string // absolute on-disk path
	Content     string // would-be content
	ContentHash string // sha256 of Content
}

// planWorkflows resolves the selected runtimes into plannedWorkflow
// entries. Unknown runtimes are silently skipped (only the runtimes for
// which a generator is registered render output). Output is sorted by
// RelPath for deterministic ordering.
func planWorkflows(target string, runtimes []string) []plannedWorkflow {
	seen := make(map[string]struct{})
	var plans []plannedWorkflow
	for _, rt := range runtimes {
		specs, ok := runtimeWorkflows[strings.ToLower(rt)]
		if !ok {
			continue
		}
		for _, spec := range specs {
			rel := filepath.ToSlash(filepath.Join(".github", "workflows", spec.OutFile))
			if _, dup := seen[rel]; dup {
				continue
			}
			seen[rel] = struct{}{}
			content := renderWorkflowCaller(spec)
			plans = append(plans, plannedWorkflow{
				RelPath:     rel,
				AbsPath:     filepath.Join(target, filepath.FromSlash(rel)),
				Content:     content,
				ContentHash: sha256Hex([]byte(content)),
			})
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].RelPath < plans[j].RelPath })
	return plans
}

// renderWorkflows applies the planned workflow stubs to disk and updates
// `.kit/generated.json`. Honors Inputs.DryRun (no on-disk side effects).
// Returns one WorkflowAction per planned path so the caller can fold
// them into the summary / JSON output.
//
// Behavior follows Section 6:
//   - new path → action "write", reason "new"
//   - existing path with hash == manifest hash and content == planned → "skip-unchanged"
//   - existing path with hash == manifest hash and content != planned → refresh in place ("write", reason "refresh")
//   - existing path with hash != manifest hash → write `<path>.kit-suggested` ("suggest-sibling", reason "user-edited")
//   - manifest-only side effect (every path matched + content unchanged) is currently
//     covered by "skip-unchanged" entries; "manifest-update" is reserved for a future
//     pure-timestamp refresh that does not change any file body.
func renderWorkflows(target string, runtimes []string, in Inputs, now func() time.Time) ([]WorkflowAction, error) {
	if now == nil {
		now = time.Now
	}
	plans := planWorkflows(target, runtimes)
	if len(plans) == 0 {
		return nil, nil
	}

	manifestPath := filepath.Join(target, filepath.FromSlash(manifestRelPath))
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("kit init: read manifest %q: %w", manifestPath, err)
	}
	index := manifest.index()

	actions := make([]WorkflowAction, 0, len(plans))
	for _, p := range plans {
		action, err := applyWorkflow(p, manifest, index, in.DryRun, now)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}

	// Persist manifest. In dry-run we computed actions and projected
	// manifest mutations onto the in-memory copy but never wrote the file.
	if !in.DryRun {
		if err := writeWorkflowManifest(manifestPath, manifest); err != nil {
			return nil, fmt.Errorf("kit init: write manifest %q: %w", manifestPath, err)
		}
	}
	return actions, nil
}

// applyWorkflow handles one planned file. Mutates `manifest` in place to
// reflect the would-be (or actually-applied) state.
func applyWorkflow(p plannedWorkflow, manifest *Manifest, index map[string]int,
	dryRun bool, now func() time.Time,
) (WorkflowAction, error) {
	// Suggestion cleanup: if the live file already matches an existing
	// `.kit-suggested` sibling, the user accepted the suggestion → drop
	// the stale sibling before evaluating the new render.
	suggested := p.AbsPath + suggestedSuffix
	if !dryRun {
		if err := pruneAcceptedSuggestion(p.AbsPath, suggested); err != nil {
			return WorkflowAction{}, err
		}
	}

	existingBytes, statErr := os.ReadFile(p.AbsPath)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return WorkflowAction{}, fmt.Errorf("kit init: stat %q: %w", p.AbsPath, statErr)
	}
	existingHash := ""
	if exists {
		existingHash = sha256Hex(existingBytes)
	}

	manifestEntry, hasManifestEntry := lookupManifest(manifest, index, p.RelPath)

	switch {
	case !exists:
		// New file path. Always written (or projected, in dry-run).
		if !dryRun {
			if err := writeWorkflowFile(p.AbsPath, p.Content); err != nil {
				return WorkflowAction{}, err
			}
		}
		upsertManifest(manifest, index, ManifestEntry{
			Path:        p.RelPath,
			SHA256:      p.ContentHash,
			GeneratedAt: now().UTC().Format(time.RFC3339),
		})
		return WorkflowAction{Path: p.RelPath, Action: "write", Reason: "new"}, nil

	case hasManifestEntry && existingHash == manifestEntry.SHA256 && existingHash == p.ContentHash:
		// Identical content; only the manifest timestamp would shift.
		// Skip-unchanged per Section 6's grammar.
		upsertManifest(manifest, index, ManifestEntry{
			Path:        p.RelPath,
			SHA256:      p.ContentHash,
			GeneratedAt: manifestEntry.GeneratedAt, // preserve original timestamp
		})
		return WorkflowAction{Path: p.RelPath, Action: "skip-unchanged"}, nil

	case hasManifestEntry && existingHash == manifestEntry.SHA256 && existingHash != p.ContentHash:
		// File matches manifest → kit can refresh in place.
		if !dryRun {
			if err := writeWorkflowFile(p.AbsPath, p.Content); err != nil {
				return WorkflowAction{}, err
			}
		}
		upsertManifest(manifest, index, ManifestEntry{
			Path:        p.RelPath,
			SHA256:      p.ContentHash,
			GeneratedAt: now().UTC().Format(time.RFC3339),
		})
		return WorkflowAction{Path: p.RelPath, Action: "write", Reason: "refresh"}, nil

	default:
		// Either no manifest entry (user-authored) or the on-disk hash
		// diverged from the manifest (user-edited). Surface a sibling.
		// Skip the sibling write when the existing file already matches
		// what we'd render — there's nothing to suggest, and we should
		// reclaim the path as kit-managed so future runs don't keep
		// suggesting against a stale manifest hash.
		if existingHash == p.ContentHash {
			// Convergence reclaim per Section 6: the user-edited file
			// (or untracked file) now equals the planner's render.
			// Update the manifest entry to the current hash and remove
			// any byte-identical `.kit-suggested` sibling. Both side
			// effects are gated on !dryRun so dry-run only projects.
			if !dryRun {
				if err := removeFileIfExists(suggested); err != nil {
					return WorkflowAction{}, err
				}
			}
			upsertManifest(manifest, index, ManifestEntry{
				Path:        p.RelPath,
				SHA256:      p.ContentHash,
				GeneratedAt: now().UTC().Format(time.RFC3339),
			})
			return WorkflowAction{
				Path:   p.RelPath,
				Action: "manifest-update",
				Reason: "convergence",
			}, nil
		}
		if !dryRun {
			if err := writeWorkflowFile(suggested, p.Content); err != nil {
				return WorkflowAction{}, err
			}
		}
		// Manifest is NOT updated for sibling writes: the live file is
		// still user-owned and we don't claim it.
		return WorkflowAction{
			Path:          p.RelPath,
			Action:        "suggest-sibling",
			SuggestedPath: p.RelPath + suggestedSuffix,
			Reason:        "user-edited",
		}, nil
	}
}

// writeWorkflowFile writes b to path, creating any missing parent
// directories. Mode 0o644 matches the rest of cmd/kit/init.
func writeWorkflowFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("kit init: mkdir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("kit init: write %q: %w", path, err)
	}
	return nil
}

// removeFileIfExists removes path, treating a missing file as success.
// All other I/O errors propagate so the caller sees real problems.
func removeFileIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("kit init: remove %q: %w", path, err)
	}
	return nil
}

// pruneAcceptedSuggestion deletes `.kit-suggested` siblings that the user
// has effectively accepted (live file already byte-identical). Missing
// siblings are a silent no-op.
func pruneAcceptedSuggestion(livePath, suggested string) error {
	suggestedBytes, err := os.ReadFile(suggested)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("kit init: read suggestion %q: %w", suggested, err)
	}
	liveBytes, err := os.ReadFile(livePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("kit init: read live file %q: %w", livePath, err)
	}
	if sha256Hex(liveBytes) != sha256Hex(suggestedBytes) {
		return nil
	}
	if err := os.Remove(suggested); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("kit init: prune suggestion %q: %w", suggested, err)
	}
	return nil
}

// sha256Hex returns the lowercase hex SHA-256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// readManifest loads `.kit/generated.json` from path. Missing file
// yields an empty manifest. Malformed JSON returns an error so the user
// can repair (kit does not silently discard user-visible state).
func readManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Manifest{Version: manifestVersion, GeneratedBy: manifestGeneratedBy}, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	// Default missing (zero/empty) fields, but reject any explicitly-set
	// value that disagrees with the pinned schema. A future kit-init
	// bumping these constants will rewrite existing manifests in place;
	// the current build must fail fast rather than silently mis-interpret
	// a newer or third-party manifest.
	switch m.Version {
	case 0:
		m.Version = manifestVersion
	case manifestVersion:
		// ok
	default:
		return nil, fmt.Errorf("unexpected manifest version %d (expected %d)", m.Version, manifestVersion)
	}
	switch m.GeneratedBy {
	case "":
		m.GeneratedBy = manifestGeneratedBy
	case manifestGeneratedBy:
		// ok
	default:
		return nil, fmt.Errorf("unexpected manifest generated_by %q (expected %q)", m.GeneratedBy, manifestGeneratedBy)
	}
	return &m, nil
}

// writeWorkflowManifest persists the manifest as pretty JSON. Atomic write via
// temp+rename so a partial write never leaves a half-baked manifest.
func writeWorkflowManifest(path string, m *Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	// Sort entries for stable diffs across runs.
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	// os.Rename overwrites silently on POSIX but fails on Windows when
	// the destination already exists or a handle is held. Remove the
	// destination first (missing-file is fine) so the rename is
	// reliable on every platform.
	if err := removeFileIfExists(path); err != nil {
		// Best-effort: clean up the temp file so we don't leave litter.
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// index returns a map from RelPath to position in m.Files for O(1)
// lookup + in-place updates.
func (m *Manifest) index() map[string]int {
	idx := make(map[string]int, len(m.Files))
	for i, f := range m.Files {
		idx[f.Path] = i
	}
	return idx
}

// lookupManifest returns the ManifestEntry for relPath when present.
func lookupManifest(m *Manifest, idx map[string]int, relPath string) (ManifestEntry, bool) {
	if i, ok := idx[relPath]; ok {
		return m.Files[i], true
	}
	return ManifestEntry{}, false
}

// upsertManifest inserts or updates the manifest entry for entry.Path,
// keeping idx in sync.
func upsertManifest(m *Manifest, idx map[string]int, entry ManifestEntry) {
	if i, ok := idx[entry.Path]; ok {
		m.Files[i] = entry
		return
	}
	idx[entry.Path] = len(m.Files)
	m.Files = append(m.Files, entry)
}
