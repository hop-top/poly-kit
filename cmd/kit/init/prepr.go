// Package kitinit — prepr.go scaffolds the before-PR git hook
// (.githooks/pre-pr) along with the .kit/generated.json manifest entry
// that lets future kit-init runs refresh it non-destructively.
//
// Contract: docs/contracts/kit-init-pr-wiring.md
//   - Section 4: scratchpad location and <project-id> slug algorithm.
//   - Section 6: augment-mode conflict policy + .kit/generated.json
//     manifest shape (version=1, files[] with path/sha256/generatedAt).
//   - Section 7: hook semantics — lint → test → scratchpad cleanup gates,
//     non-zero exit blocks PR creation, --no-verify is the only bypass.
//   - Section 8: --with-githook-pre-pr / --without-githook-pre-pr flags.
//
// Why this lives outside the tmpl engine: the hook script is a
// kit-init-owned scaffold, not a per-template artefact. Adopters who use
// any template still get the same hook content. Routing it through the
// engine would force every template's manifest to declare the hook, with
// no way to keep the manifest entry (.kit/generated.json) in sync with
// arbitrary template output.
package kitinit

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

//go:embed prepr_assets/pre-pr.sh
var preprAssets embed.FS

// PrePrHookPath is the repo-relative path of the generated hook script.
// POSIX form (forward slashes) per the manifest contract (Section 6).
const PrePrHookPath = ".githooks/pre-pr"

// GeneratedManifestPath is the repo-relative path of the kit-init
// manifest. Pinned by Section 6 of the contract.
const GeneratedManifestPath = ".kit/generated.json"

// GeneratedManifestVersion is the current manifest schema version.
// Bump on incompatible shape changes.
const GeneratedManifestVersion = 1

// GeneratedManifestProducer is the value written to `generated_by`.
const GeneratedManifestProducer = "kit-init"

// GeneratedManifest models .kit/generated.json (Section 6).
type GeneratedManifest struct {
	Version     int                     `json:"version"`
	GeneratedBy string                  `json:"generated_by"`
	Files       []GeneratedManifestFile `json:"files"`
}

// GeneratedManifestFile is one entry in GeneratedManifest.Files.
//
// Path is repo-relative POSIX (forward slashes, no leading "./").
// SHA256 is hex-encoded SHA-256 of the file contents at generation time.
// GeneratedAt is RFC 3339 UTC timestamp.
type GeneratedManifestFile struct {
	Path        string    `json:"path"`
	SHA256      string    `json:"sha256"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// PrePrAction describes the side effect kit init applied (or would
// apply, in dry-run) for one file the generator owns. Mirrors the
// JSON `action` field in Section 6 dry-run output.
type PrePrAction string

const (
	// ActionWrite — file did not exist; bytes were written.
	ActionWrite PrePrAction = "write"
	// ActionSkipUnchanged — file exists with identical content; no-op.
	ActionSkipUnchanged PrePrAction = "skip-unchanged"
	// ActionSuggestSibling — file diverges from manifest; .kit-suggested
	// sibling was written, original untouched.
	ActionSuggestSibling PrePrAction = "suggest-sibling"
	// ActionManifestUpdate — only the manifest entry's generatedAt
	// changes; no on-disk content under the path was touched.
	ActionManifestUpdate PrePrAction = "manifest-update"
)

// PrePrReason mirrors Section 6's `reason` enum.
type PrePrReason string

const (
	// ReasonNew — target path absent on disk before this run.
	ReasonNew PrePrReason = "new"
	// ReasonRefresh — on-disk hash matched manifest; refreshed in place.
	ReasonRefresh PrePrReason = "refresh"
	// ReasonUserEdited — on-disk hash diverged from manifest (or path
	// missing from manifest); routed to .kit-suggested sibling.
	ReasonUserEdited PrePrReason = "user-edited"
	// ReasonManifestOnly — only the manifest entry needed updating.
	ReasonManifestOnly PrePrReason = "manifest-only"
)

// PrePrFileReport is one row of dry-run / JSON output.
type PrePrFileReport struct {
	Path          string      `json:"path"`
	Action        PrePrAction `json:"action"`
	SuggestedPath string      `json:"suggested_path,omitempty"`
	Reason        PrePrReason `json:"reason"`
}

// PrePrResult captures everything the bootstrap/augment caller needs to
// surface in the Summary.
type PrePrResult struct {
	Files []PrePrFileReport `json:"files"`
}

// GeneratePrePrHook scaffolds .githooks/pre-pr in the adopter repo
// rooted at projectRoot, refreshing or suggesting per the manifest
// policy in Section 6.
//
//   - dryRun=true: compute the file list and manifest delta, write
//     nothing to disk.
//   - now: timestamp injected for deterministic tests; production
//     callers pass time.Now().UTC().
//
// Returns the per-file report (one row per managed path). Callers are
// expected to fold these rows into the run's Summary.
//
// The manifest is read from <projectRoot>/.kit/generated.json. Missing
// manifest is treated as a fresh run (all files are "new" or
// "user-edited", depending on whether the target path exists).
func GeneratePrePrHook(projectRoot string, dryRun bool, now time.Time) (PrePrResult, error) {
	hookBytes, err := loadPrePrHookBytes()
	if err != nil {
		return PrePrResult{}, fmt.Errorf("kit init: pre-pr hook asset: %w", err)
	}

	// ReadGeneratedManifest already converts the safe cases (missing
	// file, malformed JSON) into (zero, nil); a non-nil error here is a
	// genuine I/O failure (permissions, broken FS) that the caller has
	// no other way to learn about, so we surface it.
	manifest, err := ReadGeneratedManifest(projectRoot)
	if err != nil {
		return PrePrResult{}, err
	}

	hookReport, err := scaffoldPrePrFile(projectRoot, PrePrHookPath, hookBytes, manifest, dryRun, 0o755)
	if err != nil {
		return PrePrResult{}, err
	}

	// Update / insert the manifest entry — but only when we actually
	// wrote (or would write, in dry-run) the on-disk file. For
	// suggest-sibling we leave the original entry alone so future
	// runs continue to surface the conflict against the same baseline.
	switch hookReport.Action {
	case ActionWrite, ActionSkipUnchanged:
		manifest = upsertManifestEntry(manifest, GeneratedManifestFile{
			Path:        PrePrHookPath,
			SHA256:      hashBytes(hookBytes),
			GeneratedAt: now.UTC(),
		})
	}
	// In all cases we (re)write the manifest so version/producer
	// converge even on a project that pre-dated the manifest.
	manifest.Version = GeneratedManifestVersion
	manifest.GeneratedBy = GeneratedManifestProducer
	sortManifest(&manifest)

	manifestReport, err := writeManifest(projectRoot, manifest, dryRun)
	if err != nil {
		return PrePrResult{}, err
	}

	return PrePrResult{Files: []PrePrFileReport{hookReport, manifestReport}}, nil
}

// loadPrePrHookBytes returns the embedded hook script body.
func loadPrePrHookBytes() ([]byte, error) {
	return fs.ReadFile(preprAssets, "prepr_assets/pre-pr.sh")
}

// scaffoldPrePrFile applies the Section 6 conflict policy to one path.
//
// Rules, in order:
//  1. Cleanup: a byte-identical .kit-suggested sibling next to <path>
//     is removed before any write decision — the user effectively
//     accepted the suggestion (Section 6).
//  2. If <path> does not exist: write it; reason=new.
//  3. If on-disk bytes match the would-be content: skip-unchanged.
//  4. If on-disk bytes match the manifest entry for <path>: refresh
//     in place (overwrite + update manifest).
//  5. Otherwise: write <path>.kit-suggested with the would-be content,
//     leave <path> alone; reason=user-edited.
func scaffoldPrePrFile(
	projectRoot, relPath string,
	content []byte,
	manifest GeneratedManifest,
	dryRun bool,
	mode os.FileMode,
) (PrePrFileReport, error) {
	abs := filepath.Join(projectRoot, filepath.FromSlash(relPath))
	suggested := abs + ".kit-suggested"
	report := PrePrFileReport{Path: relPath}

	// Step 1: cleanup an accepted suggestion.
	if existing, sErr := os.ReadFile(suggested); sErr == nil {
		if onDisk, dErr := os.ReadFile(abs); dErr == nil && bytesEqual(existing, onDisk) {
			if !dryRun {
				_ = os.Remove(suggested)
			}
		}
	}

	existing, statErr := os.ReadFile(abs)
	switch {
	case os.IsNotExist(statErr):
		// Step 2: brand-new file.
		report.Action = ActionWrite
		report.Reason = ReasonNew
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return report, fmt.Errorf("kit init: mkdir %s: %w", filepath.Dir(relPath), err)
			}
			if err := os.WriteFile(abs, content, mode); err != nil {
				return report, fmt.Errorf("kit init: write %s: %w", relPath, err)
			}
		}
		return report, nil

	case statErr != nil:
		return report, fmt.Errorf("kit init: stat %s: %w", relPath, statErr)
	}

	// File exists. Step 3: identical content.
	if bytesEqual(existing, content) {
		report.Action = ActionSkipUnchanged
		report.Reason = ReasonRefresh
		return report, nil
	}

	// Step 4: existing hash matches manifest → safe refresh.
	if manifestHashMatches(manifest, relPath, existing) {
		report.Action = ActionWrite
		report.Reason = ReasonRefresh
		if !dryRun {
			if err := os.WriteFile(abs, content, mode); err != nil {
				return report, fmt.Errorf("kit init: refresh %s: %w", relPath, err)
			}
		}
		return report, nil
	}

	// Step 5: user-edited (or absent from manifest entirely). Write
	// .kit-suggested sibling, leave original untouched.
	report.Action = ActionSuggestSibling
	report.Reason = ReasonUserEdited
	report.SuggestedPath = relPath + ".kit-suggested"
	if !dryRun {
		if err := os.MkdirAll(filepath.Dir(suggested), 0o755); err != nil {
			return report, fmt.Errorf("kit init: mkdir %s: %w", filepath.Dir(relPath), err)
		}
		if err := os.WriteFile(suggested, content, mode); err != nil {
			return report, fmt.Errorf("kit init: write %s: %w", report.SuggestedPath, err)
		}
	}
	return report, nil
}

// writeManifest serialises manifest to .kit/generated.json. Always
// reports either manifest-update (when bytes diverge) or
// skip-unchanged (when bytes match an existing manifest verbatim).
func writeManifest(projectRoot string, manifest GeneratedManifest, dryRun bool) (PrePrFileReport, error) {
	abs := filepath.Join(projectRoot, filepath.FromSlash(GeneratedManifestPath))
	report := PrePrFileReport{Path: GeneratedManifestPath}

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return report, fmt.Errorf("kit init: marshal manifest: %w", err)
	}
	encoded = append(encoded, '\n')

	existing, statErr := os.ReadFile(abs)
	if statErr == nil && bytesEqual(existing, encoded) {
		report.Action = ActionSkipUnchanged
		report.Reason = ReasonManifestOnly
		return report, nil
	}

	report.Action = ActionManifestUpdate
	report.Reason = ReasonManifestOnly
	if !dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return report, fmt.Errorf("kit init: mkdir %s: %w", filepath.Dir(GeneratedManifestPath), err)
		}
		if err := os.WriteFile(abs, encoded, 0o644); err != nil {
			return report, fmt.Errorf("kit init: write %s: %w", GeneratedManifestPath, err)
		}
	}
	return report, nil
}

// ReadGeneratedManifest loads .kit/generated.json from projectRoot.
// Missing file or malformed JSON returns a zero-value manifest and
// nil error — callers treat absent/invalid manifest as "no prior
// generated state" per Section 6.
func ReadGeneratedManifest(projectRoot string) (GeneratedManifest, error) {
	abs := filepath.Join(projectRoot, filepath.FromSlash(GeneratedManifestPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return GeneratedManifest{}, nil
		}
		return GeneratedManifest{}, fmt.Errorf("kit init: read manifest: %w", err)
	}
	var m GeneratedManifest
	if jerr := json.Unmarshal(data, &m); jerr != nil {
		// Treat as missing so the run still completes; the next write
		// will replace the corrupt file with a valid manifest.
		return GeneratedManifest{}, nil
	}
	return m, nil
}

// upsertManifestEntry inserts entry if absent, else updates the
// matching path in place. Returns the (possibly mutated) manifest.
func upsertManifestEntry(m GeneratedManifest, entry GeneratedManifestFile) GeneratedManifest {
	for i := range m.Files {
		if m.Files[i].Path == entry.Path {
			m.Files[i] = entry
			return m
		}
	}
	m.Files = append(m.Files, entry)
	return m
}

func sortManifest(m *GeneratedManifest) {
	sort.SliceStable(m.Files, func(i, j int) bool {
		return m.Files[i].Path < m.Files[j].Path
	})
}

// manifestHashMatches returns true iff the manifest carries an entry
// for relPath and that entry's SHA-256 matches the SHA-256 of bytes.
func manifestHashMatches(m GeneratedManifest, relPath string, body []byte) bool {
	want := ""
	for _, f := range m.Files {
		if f.Path == relPath {
			want = f.SHA256
			break
		}
	}
	if want == "" {
		return false
	}
	return strings.EqualFold(want, hashBytes(body))
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// bytesEqual is a small wrapper kept for readability; tests do not
// import bytes.Equal directly.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- project-id slug + scratchpad path (used by tests and any Go
// callers that need parity with the shell hook). The hook itself
// implements the same algorithm inline so it has no runtime Go
// dependency. ----------------------------------------------------------

var slugBadRune = regexp.MustCompile(`[^a-z0-9-]+`)
var slugCollapse = regexp.MustCompile(`-+`)

// ProjectIDSlug derives the slug pinned by Section 4 of the contract.
//
// Algorithm (mirror of pre-pr.sh project_id):
//  1. Read `git config --get remote.origin.url`.
//  2. If origin is set: strip leading git@host: or scheme://host[:port]/,
//     strip trailing .git, lowercase, non-[a-z0-9-]→-, collapse, trim.
//  3. Else: derive from absolute repo root path with the same rules.
//  4. Empty result → "kit-init".
//
// Callers that already know the origin URL can call DeriveSlugFromOrigin
// or DeriveSlugFromPath directly to avoid shelling out to git.
func ProjectIDSlug(repoRoot string) string {
	if origin := readGitOrigin(repoRoot); origin != "" {
		if slug := DeriveSlugFromOrigin(origin); slug != "" {
			return slug
		}
	}
	if slug := DeriveSlugFromPath(repoRoot); slug != "" {
		return slug
	}
	return "kit-init"
}

// DeriveSlugFromOrigin applies the Section 4 slug rules to a git remote
// URL. Returns "" only when the input simplifies to no usable runes.
func DeriveSlugFromOrigin(origin string) string {
	s := strings.TrimSpace(origin)
	// Strip `git@host:` shorthand. Leaves `host:path`.
	if strings.HasPrefix(s, "git@") {
		s = strings.TrimPrefix(s, "git@")
	}
	// Strip `scheme://[user@]host[:port]/`.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
	}
	// Strip trailing .git.
	s = strings.TrimSuffix(s, ".git")
	// Normalise `:` (path separator in git@host:path) to `/` so the
	// host and path segments both contribute to the slug.
	s = strings.ReplaceAll(s, ":", "/")
	return slugify(s)
}

// DeriveSlugFromPath applies the Section 4 slug rules to a filesystem
// path.
func DeriveSlugFromPath(p string) string {
	return slugify(p)
}

func slugify(s string) string {
	out := strings.ToLower(s)
	out = slugBadRune.ReplaceAllString(out, "-")
	out = slugCollapse.ReplaceAllString(out, "-")
	out = strings.Trim(out, "-")
	return out
}

// readGitOrigin runs `git -C <repoRoot> config --get remote.origin.url`
// and returns the trimmed output, or "" on any error.
func readGitOrigin(repoRoot string) string {
	cmd := exec.Command("git", "-C", repoRoot, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ScratchpadPath returns the per-OS scratchpad location for slug, per
// Section 4. env is the process environment (production: os.Getenv);
// goos is runtime.GOOS by default (overridable for cross-platform tests).
//
// Linux:   $XDG_RUNTIME_DIR/<slug>.scratchpad if set; else $TMPDIR; else /tmp.
// macOS:   $TMPDIR/<slug>.scratchpad.
// Windows: %LOCALAPPDATA%\Temp\<slug>.scratchpad.
func ScratchpadPath(slug, goos string, env func(string) string) string {
	if env == nil {
		env = os.Getenv
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	leaf := slug + ".scratchpad"
	switch goos {
	case "windows":
		// LOCALAPPDATA points at `...\AppData\Local` so we still need to
		// append the Temp segment. TEMP and the C:\Windows\Temp hard
		// fallback already end in Temp; appending another segment would
		// produce `...\Temp\Temp\<leaf>`.
		if base := env("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "Temp", leaf)
		}
		if base := env("TEMP"); base != "" {
			return filepath.Join(base, leaf)
		}
		return filepath.Join(`C:\Windows\Temp`, leaf)
	case "linux":
		if v := env("XDG_RUNTIME_DIR"); v != "" {
			return filepath.Join(v, leaf)
		}
		if v := env("TMPDIR"); v != "" {
			return filepath.Join(v, leaf)
		}
		return filepath.Join("/tmp", leaf)
	default: // darwin and other POSIX systems
		if v := env("TMPDIR"); v != "" {
			return filepath.Join(v, leaf)
		}
		return filepath.Join("/tmp", leaf)
	}
}

// --- scratchpad detection (used by Go-side tests; the hook implements
// the same scan in shell). --------------------------------------------

// ScratchpadPatterns are the conventional ephemeral-planning markers
// the hook scans for in tracked files. Picked to be:
//   - rare in real code (each contains a non-identifier hyphen or
//     follows a colon-ending lead-in)
//   - explicit about their planning intent
//   - matched verbatim in pre-pr.sh's grep -E expression
//
// Keep this list in sync with the regex in prepr_assets/pre-pr.sh
// (SCRATCH_PATTERNS_RE).
var ScratchpadPatterns = []string{
	"SCRATCH:",
	"FIXME-PLAN:",
	"AGENT-NOTE:",
	"KIT-SCRATCH:",
}

// scratchpadRE is the compiled form of ScratchpadPatterns. Anchored to
// the start of each marker (no word boundary needed; the patterns are
// distinctive enough that false positives are negligible).
var scratchpadRE = regexp.MustCompile(`(SCRATCH|FIXME-PLAN|AGENT-NOTE|KIT-SCRATCH):`)

// ScanScratchpad scans the given file contents and returns true if any
// of ScratchpadPatterns appears. Used by the Go-side tests to verify
// parity with the shell scanner. Production runs delegate to the hook
// script.
func ScanScratchpad(content []byte) bool {
	return scratchpadRE.Match(content)
}
