// Package validator implements the three-tier story validator per
// design.md §6:
//
//	Tier 1   schema validity (always on)
//	Tier 1.5 metadata key denylist (always on)
//	Tier 2   referential validity (default on; --strict-toolspec
//	         escalates the warn-on-missing-toolspec finding to error)
//	Tier 3   toolspec semantic validity (opt-in via --strict-toolspec)
//
// Style warnings (banned-vocabulary heuristics on intent fields) fire
// as Severity=warn findings and do NOT change the exit code. They
// show in human output and in JSON output as `"severity": "warn"`.
//
// The validator is pure: it consumes already-parsed stories
// (parser.ParsedStory) and pre-loaded scenario rules
// (*scenariorules.Document); it does not read files itself. The CLI
// leaf in console/cli/conformance/verify_stories.go owns I/O and
// flag-to-options translation.
package validator

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/conformance/scenariorules"
	"hop.top/kit/go/conformance/story/parser"
	"hop.top/kit/go/conformance/story/schema"
	"hop.top/kit/go/conformance/story/toolspec"
)

// Severity classifies a finding. Only "error" findings produce a
// non-zero exit. "warn" findings are advisory.
type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
)

// Finding is one validator output. The shape is intentionally a
// superset of verify-no-leak's Finding so JSON consumers can parse
// either tool with the same struct (fields where they overlap have
// the same name + type).
type Finding struct {
	// File is the path the story was read from. "<bytes>" for
	// in-memory inputs (tests).
	File string `json:"file"`
	// Line is the YAML node line (1-based) the finding refers to.
	// 0 when no precise location is available.
	Line int `json:"line,omitempty"`
	// Rule is the rule id (see findingRules below). Stable across
	// kit versions; adopter docs reference these by id.
	Rule string `json:"rule"`
	// Severity is "error" (counts toward exit) or "warn" (advisory).
	Severity Severity `json:"severity"`
	// Message is the human-readable failure description.
	Message string `json:"message"`
	// MatchedToken is the offending token where applicable
	// (denylisted metadata key, banned vocabulary word, etc.).
	MatchedToken string `json:"matched_token,omitempty"`
	// SuggestedFix is a kit-curated nudge per error class.
	SuggestedFix string `json:"suggested_fix,omitempty"`
}

// Options controls which tiers run.
type Options struct {
	// StrictToolspec enables tier 3 + escalates the missing-toolspec
	// warn to an error.
	StrictToolspec bool
	// ToolspecOverride is the explicit path passed via --toolspec.
	// When set, takes precedence over per-story toolspec_ref.
	ToolspecOverride string
	// RepoRoot is the repository root used to resolve toolspec_ref
	// paths (which must be relative + within the root). Defaults to
	// the cwd when empty.
	RepoRoot string
	// MaxSteps caps the steps[] length. Defaults to 50 when zero.
	MaxSteps int
	// Rules is the loaded scenario-rules document; required for the
	// metadata-key denylist (tier 1.5).
	Rules *scenariorules.Document
}

const defaultMaxSteps = 50

// storyIDRegex matches the canonical dotted-slug form
// (^[a-z][a-z0-9-]*(\.[a-z0-9-]+)+$, length 3..128). Used by tier 1.
var storyIDRegex = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z0-9-]+)+$`)

// stepIDRegex matches the canonical step-id form
// (^[a-z][a-z0-9_]*$). Used by tier 1.
var stepIDRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// bannedVocabRegex flags assertion-shaped vocabulary in intent
// fields. Whole-word, case-insensitive. Fires Severity=warn only —
// curated by design.md §6 "Style warnings".
var bannedVocabRegex = regexp.MustCompile(`(?i)\b(should|must|will|assert|expect|exit code)\b`)

// extraDenylistKeys are the metadata keys explicitly forbidden in
// addition to the union of scenariorules.Verbs + TopLevelKeys.
// design.md §6 calls these out for the cassette_* family which is
// not in TopLevelKeys (it lives in compound_rules.R3.keys).
var extraDenylistKeys = []string{
	"cassette_must_contain",
	"cassette_must_not_contain",
}

// ValidateOne runs tiers 1/1.5/2/3 against a single parsed story.
// Returns a flat list of findings; an empty list means clean.
// ValidateOne does not enforce uniqueness — the multi-story
// uniqueness check lives in ValidateAll which has access to the
// cross-file index.
func ValidateOne(p *parser.ParsedStory, opts Options) []Finding {
	if p == nil || p.Story == nil {
		return nil
	}
	if opts.MaxSteps == 0 {
		opts.MaxSteps = defaultMaxSteps
	}
	var findings []Finding
	findings = append(findings, tier1(p, opts)...)
	findings = append(findings, tier1Half(p, opts)...)
	findings = append(findings, tier2Local(p, opts)...)
	if opts.StrictToolspec {
		findings = append(findings, tier3(p, opts)...)
	}
	findings = append(findings, styleWarnings(p)...)
	return findings
}

// ValidateAll runs ValidateOne across every parsed story and adds
// the cross-story story_id uniqueness check. Returns the combined
// finding list, ordered by input order then within-story order.
func ValidateAll(stories []*parser.ParsedStory, opts Options) []Finding {
	if len(stories) == 0 {
		return nil
	}
	// Build the id → first-path index for the uniqueness check.
	seen := make(map[string]string, len(stories))
	var dup []Finding
	for _, p := range stories {
		if p == nil || p.Story == nil {
			continue
		}
		id := p.Story.StoryID
		if id == "" {
			continue
		}
		if prev, ok := seen[id]; ok {
			dup = append(dup, Finding{
				File:         p.Path,
				Rule:         "duplicate-story-id",
				Severity:     SeverityError,
				Message:      fmt.Sprintf("story_id %q is also declared in %s", id, prev),
				MatchedToken: id,
				SuggestedFix: "rename one of the stories' story_id to a unique dotted slug",
			})
			continue
		}
		seen[id] = p.Path
	}
	var out []Finding
	for _, p := range stories {
		out = append(out, ValidateOne(p, opts)...)
	}
	out = append(out, dup...)
	return out
}

// ----- tier 1: schema validity -----

func tier1(p *parser.ParsedStory, opts Options) []Finding {
	s := p.Story
	var out []Finding

	if s.SchemaVersion == "" {
		out = append(out, errFinding(p, "missing-schema-version",
			"schema_version is required", "",
			`add: schema_version: "1"`))
	} else if s.SchemaVersion != schema.SchemaVersionV1 {
		out = append(out, errFinding(p, "unsupported-schema-version",
			fmt.Sprintf("schema_version %q is not supported by this kit binary (expected %q)", s.SchemaVersion, schema.SchemaVersionV1),
			s.SchemaVersion,
			"upgrade kit, or set schema_version: \"1\""))
	}

	if s.StoryID == "" {
		out = append(out, errFinding(p, "missing-story-id",
			"story_id is required", "",
			"add a dotted slug, e.g. story_id: spaced.launch.dry-run"))
	} else if l := len(s.StoryID); l < 3 || l > 128 {
		out = append(out, errFinding(p, "story-id-length",
			fmt.Sprintf("story_id length %d is outside [3,128]", l),
			s.StoryID,
			"shorten or lengthen to a 3-128 char dotted slug"))
	} else if !storyIDRegex.MatchString(s.StoryID) {
		out = append(out, errFinding(p, "story-id-shape",
			fmt.Sprintf("story_id %q does not match dotted-slug pattern", s.StoryID),
			s.StoryID,
			"use lowercase letters, digits, hyphens, separated by dots (e.g. ns.domain.journey)"))
	}

	if t := strings.TrimSpace(s.Title); t == "" {
		out = append(out, errFinding(p, "missing-title", "title is required", "", "add a 1-80 char title"))
	} else if l := len(s.Title); l > 80 {
		out = append(out, errFinding(p, "title-length", fmt.Sprintf("title length %d exceeds 80", l), "", "trim title to <=80 chars"))
	}

	if i := strings.TrimSpace(s.Intent); i == "" {
		out = append(out, errFinding(p, "missing-intent", "intent is required", "", "describe what the user is trying to do in 40-2000 chars"))
	} else {
		l := len(s.Intent)
		if l < 40 {
			out = append(out, errFinding(p, "intent-length", fmt.Sprintf("intent length %d is below the 40-char minimum", l), "", "expand intent to 40-2000 chars of descriptive prose"))
		} else if l > 2000 {
			out = append(out, errFinding(p, "intent-length", fmt.Sprintf("intent length %d exceeds 2000", l), "", "trim intent to <=2000 chars"))
		}
	}

	if b := strings.TrimSpace(s.Binary); b == "" {
		out = append(out, errFinding(p, "missing-binary", "binary is required", "", "add binary: <tool-name>"))
	} else if l := len(s.Binary); l > 64 {
		out = append(out, errFinding(p, "binary-length", fmt.Sprintf("binary length %d exceeds 64", l), s.Binary, "trim binary to <=64 chars"))
	}

	if len(s.Steps) == 0 {
		out = append(out, errFinding(p, "missing-steps", "steps is required and must contain at least 1 entry", "", "add at least one step"))
	} else if len(s.Steps) > opts.MaxSteps {
		out = append(out, errFinding(p, "too-many-steps", fmt.Sprintf("%d steps exceeds the cap of %d", len(s.Steps), opts.MaxSteps), "", "split the story, or raise --max-steps if appropriate"))
	}

	for i, st := range s.Steps {
		out = append(out, validateStepShape(p, i, st)...)
	}
	for i, r := range s.References {
		out = append(out, validateReferenceShape(p, i, r)...)
	}
	return out
}

func validateStepShape(p *parser.ParsedStory, idx int, st schema.Step) []Finding {
	var out []Finding
	loc := fmt.Sprintf("steps[%d]", idx)
	if st.ID == "" {
		out = append(out, errFinding(p, "missing-step-id", loc+".id is required", "", "add an id matching ^[a-z][a-z0-9_]*$"))
	} else if !stepIDRegex.MatchString(st.ID) {
		out = append(out, errFinding(p, "step-id-shape", fmt.Sprintf("%s.id %q does not match ^[a-z][a-z0-9_]*$", loc, st.ID), st.ID, "use lowercase letters / digits / underscores; start with a letter"))
	}
	if st.Intent != "" {
		if l := len(st.Intent); l < 40 || l > 500 {
			out = append(out, errFinding(p, "step-intent-length", fmt.Sprintf("%s.intent length %d outside [40,500]", loc, l), "", "describe the step in 40-500 chars of prose"))
		}
	}
	if len(st.Invoke) == 0 {
		out = append(out, errFinding(p, "missing-invoke", loc+".invoke is required and must be a non-empty argv array", "", `add: invoke: ["<binary>", "<subcommand>", ...]`))
	} else if len(st.Invoke) > 32 {
		out = append(out, errFinding(p, "invoke-too-long", fmt.Sprintf("%s.invoke length %d exceeds 32", loc, len(st.Invoke)), "", "split the step or trim the argv"))
	}
	for ci, c := range st.Capture {
		if _, ok := schema.AllowedCaptures[c]; !ok {
			out = append(out, errFinding(p, "unknown-capture",
				fmt.Sprintf("%s.capture[%d] value %q is not in {exit_code, stdout, stderr, duration_ms}", loc, ci, c),
				c,
				"use only exit_code, stdout, stderr, or duration_ms"))
		}
	}
	return out
}

func validateReferenceShape(p *parser.ParsedStory, idx int, r schema.Reference) []Finding {
	var out []Finding
	loc := fmt.Sprintf("references[%d]", idx)
	if strings.TrimSpace(r.Title) == "" {
		out = append(out, errFinding(p, "missing-ref-title", loc+".title is required", "", "add a 1-120 char title"))
	} else if l := len(r.Title); l > 120 {
		out = append(out, errFinding(p, "ref-title-length", fmt.Sprintf("%s.title length %d exceeds 120", loc, l), "", "trim title"))
	}
	if strings.TrimSpace(r.URL) == "" {
		out = append(out, errFinding(p, "missing-ref-url", loc+".url is required", "", "add an RFC-3986 URL"))
	} else if _, err := url.Parse(r.URL); err != nil {
		out = append(out, errFinding(p, "invalid-ref-url", fmt.Sprintf("%s.url %q does not parse as RFC-3986: %v", loc, r.URL, err), r.URL, "supply a valid URL"))
	}
	return out
}

// ----- tier 1.5: metadata-key denylist -----

func tier1Half(p *parser.ParsedStory, opts Options) []Finding {
	if opts.Rules == nil || len(p.Story.Metadata) == 0 {
		return nil
	}
	deny := make(map[string]struct{})
	for _, v := range opts.Rules.Verbs {
		deny[v] = struct{}{}
	}
	for _, k := range opts.Rules.TopLevelKeys {
		deny[k] = struct{}{}
	}
	for _, k := range extraDenylistKeys {
		deny[k] = struct{}{}
	}
	var out []Finding
	for k := range p.Story.Metadata {
		if _, bad := deny[k]; bad {
			out = append(out, Finding{
				File:         p.Path,
				Line:         metadataKeyLine(p.Root, k),
				Rule:         "forbidden-metadata-key",
				Severity:     SeverityError,
				Message:      fmt.Sprintf("metadata.%s is reserved for scenario rubrics; stories must not carry it", k),
				MatchedToken: k,
				SuggestedFix: fmt.Sprintf("remove the metadata.%s key from this file; assertion vocabulary belongs in the private grader scenario", k),
			})
		}
	}
	return out
}

// ----- tier 2: referential validity (local; cross-story handled by ValidateAll) -----

func tier2Local(p *parser.ParsedStory, opts Options) []Finding {
	s := p.Story
	var out []Finding

	// Step id uniqueness within this story.
	seen := map[string]bool{}
	for i, st := range s.Steps {
		if st.ID == "" {
			continue
		}
		if seen[st.ID] {
			out = append(out, errFinding(p, "duplicate-step-id",
				fmt.Sprintf("steps[%d].id %q is duplicated within this story", i, st.ID),
				st.ID,
				"give each step a unique id"))
		}
		seen[st.ID] = true
	}

	// invoke[0] must equal binary OR a path ending in /binary.
	if s.Binary != "" {
		for i, st := range s.Steps {
			if len(st.Invoke) == 0 {
				continue
			}
			head := st.Invoke[0]
			if !invokeHeadMatchesBinary(head, s.Binary) {
				out = append(out, errFinding(p, "invoke-binary-mismatch",
					fmt.Sprintf("steps[%d].invoke[0] %q does not match binary %q (allowed forms: %q, ./%q, /path/to/%q, ./bin/%q)", i, head, s.Binary, s.Binary, s.Binary, s.Binary, s.Binary),
					head,
					fmt.Sprintf("change invoke[0] to %q or a path ending in /%s", s.Binary, s.Binary)))
			}
		}
	}
	return out
}

// invokeHeadMatchesBinary checks the bare-name + path-ending-in-name
// cases enumerated in design.md §2.
func invokeHeadMatchesBinary(head, binary string) bool {
	if head == binary {
		return true
	}
	// Reject empty / suspicious heads early.
	if binary == "" || head == "" {
		return false
	}
	// Path forms: clean + check basename.
	cleaned := filepath.Clean(head)
	return filepath.Base(cleaned) == binary
}

// ----- tier 3: toolspec semantic validity -----

func tier3(p *parser.ParsedStory, opts Options) []Finding {
	s := p.Story
	tsPath := resolveToolspecPath(s, opts)
	if tsPath == "" {
		return []Finding{errFinding(p, "missing-toolspec",
			fmt.Sprintf("no toolspec resolvable for binary %q (--strict-toolspec is set)", s.Binary),
			"",
			fmt.Sprintf("add toolspec_ref to the story, pass --toolspec, or place %s.toolspec.yaml at the repo root", s.Binary))}
	}
	ts, err := toolspec.LoadFromPath(tsPath)
	if err != nil {
		return []Finding{errFinding(p, "toolspec-load-failed",
			fmt.Sprintf("could not load toolspec %s: %v", tsPath, err),
			tsPath,
			"verify the file exists and is valid YAML")}
	}
	if ts.Name != "" && s.Binary != "" && ts.Name != s.Binary {
		return []Finding{errFinding(p, "toolspec-name-mismatch",
			fmt.Sprintf("toolspec.name %q does not match story binary %q", ts.Name, s.Binary),
			ts.Name,
			"point toolspec_ref at the binary's own toolspec, or correct the binary field")}
	}

	var out []Finding
	for i, st := range s.Steps {
		// Skip invoke[0] (the binary itself) when walking.
		tokens := st.Invoke
		if len(tokens) > 0 {
			tokens = tokens[1:]
		}
		cmd, ok := ts.ResolveCommand(tokens)
		if !ok {
			out = append(out, errFinding(p, "unknown-command",
				fmt.Sprintf("steps[%d].invoke command path is not declared in toolspec %s: %v", i, tsPath, st.Invoke),
				strings.Join(st.Invoke, " "),
				"check the toolspec for the correct subcommand name(s)"))
			continue
		}
		// Walk flag tokens for unknowns.
		for _, tok := range tokens {
			if len(tok) == 0 || tok[0] != '-' {
				continue
			}
			if !toolspec.ResolveFlag(cmd, tok) {
				out = append(out, errFinding(p, "unknown-flag",
					fmt.Sprintf("steps[%d].invoke flag %q is not declared on the matched command in toolspec %s", i, tok, tsPath),
					tok,
					"check the toolspec's flags[] for the matched command, or use a documented global flag"))
			}
		}
	}
	return out
}

// resolveToolspecPath implements the design.md §8 resolution order:
//  1. opts.ToolspecOverride if set
//  2. story.ToolspecRef if set (rejected if it escapes RepoRoot)
//  3. <binary>.toolspec.yaml at RepoRoot
//  4. "" (caller treats as missing-toolspec)
func resolveToolspecPath(s *schema.Story, opts Options) string {
	if opts.ToolspecOverride != "" {
		return opts.ToolspecOverride
	}
	root := opts.RepoRoot
	if root == "" {
		root = "."
	}
	if s.ToolspecRef != "" {
		cleaned := filepath.Clean(s.ToolspecRef)
		if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
			// Path escape — refuse silently here; tier 1 catches
			// the misconfiguration shape via err-finding below if
			// we ever wire one up. For now, fall through to the
			// repo-root default so the validator stays robust.
			return ""
		}
		return filepath.Join(root, cleaned)
	}
	if s.Binary != "" {
		candidate := filepath.Join(root, s.Binary+".toolspec.yaml")
		return candidate
	}
	return ""
}

// ----- style warnings (always on, never fail) -----

func styleWarnings(p *parser.ParsedStory) []Finding {
	var out []Finding
	if loc := bannedVocabRegex.FindString(p.Story.Intent); loc != "" {
		out = append(out, Finding{
			File:         p.Path,
			Rule:         "intent-banned-vocabulary",
			Severity:     SeverityWarn,
			Message:      fmt.Sprintf("intent uses assertion vocabulary (%q); consider rephrasing as descriptive prose", loc),
			MatchedToken: loc,
			SuggestedFix: "rewrite the intent in plain-English descriptive prose (no should/must/will/assert/expect/exit code)",
		})
	}
	return out
}

// ----- helpers -----

// errFinding is the boilerplate-light helper for tier 1/2/3 error
// findings. line=0 means "no precise location available"; the
// formatter will print "file:" rather than "file:0:".
func errFinding(p *parser.ParsedStory, rule, msg, token, fix string) Finding {
	return Finding{
		File:         p.Path,
		Rule:         rule,
		Severity:     SeverityError,
		Message:      msg,
		MatchedToken: token,
		SuggestedFix: fix,
	}
}

// metadataKeyLine walks the parsed yaml.Node tree looking for the
// `metadata` mapping and returns the line of the offending key, or
// 0 when not found.
func metadataKeyLine(root *yaml.Node, key string) int {
	if root == nil {
		return 0
	}
	doc := root
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return 0
		}
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return 0
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k := doc.Content[i]
		if k.Kind != yaml.ScalarNode || k.Value != "metadata" {
			continue
		}
		md := doc.Content[i+1]
		if md.Kind != yaml.MappingNode {
			return 0
		}
		for j := 0; j+1 < len(md.Content); j += 2 {
			mk := md.Content[j]
			if mk.Kind == yaml.ScalarNode && mk.Value == key {
				return mk.Line
			}
		}
	}
	return 0
}

// ContentSHA256 is exposed by go/conformance/story (see hash.go) so
// scenarios can pin stories by content. Kept out of the validator
// to avoid making the validator depend on it.
