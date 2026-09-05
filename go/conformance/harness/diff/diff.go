// Package diff implements the cassette directory diff helper used
// by harness.PlanApplyReplay.
//
// Two cassette dirs are equal iff:
//
//   - the set of (adapter, fingerprint) pairs is the same in both,
//     AND
//   - for each shared pair, the resp envelopes compare equal under
//     YAML semantic equality modulo the recorded_at noise field.
//
// xrr cassettes are flat files named {adapter}-{fingerprint}.req.yaml
// and {adapter}-{fingerprint}.resp.yaml. Same-fingerprint writes
// overwrite (last-writer-wins), so each (adapter, fingerprint) is
// represented by at most one pair on disk. Diff therefore operates
// on a set, not a multiset.
package diff

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind classifies a single diff entry.
type Kind string

const (
	// KindAdded means the entry is present in B but not in A
	// (the canonical "second apply did more than first").
	KindAdded Kind = "added"
	// KindRemoved means the entry is present in A but not in B.
	KindRemoved Kind = "removed"
	// KindModified means the same (adapter, fingerprint) appears in
	// both but the resp envelope payload differs.
	KindModified Kind = "modified"
)

// Entry is one observation in the diff between two cassette dirs.
type Entry struct {
	Kind        Kind
	Adapter     string
	Fingerprint string
	// Summary is a one-line human-readable description of the
	// interaction (verb + URL, query first line, redis cmd…).
	Summary string
	// ALine and BLine carry the conflicting payload values for
	// KindModified; empty for added/removed. These are the YAML
	// representations of the resp payload, truncated for display.
	ALine, BLine string
}

// Diff is the ordered list of entries plus the cassette dir paths.
// The order is added → removed → modified, then alphabetical by
// (adapter, fingerprint) within each kind.
type Diff struct {
	A, B    string
	Entries []Entry
}

// Empty reports whether the diff has zero entries.
func (d Diff) Empty() bool { return len(d.Entries) == 0 }

// Format renders the diff as the multi-line text described in
// design §1's failure-message shape. classifyRead is consulted to
// flag Read-class modified entries as "informational only".
func (d Diff) Format(classifyRead func(adapter string, req map[string]any) bool) string {
	if d.Empty() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cassette diff non-empty (%d diffs)\n", len(d.Entries))
	for _, e := range d.Entries {
		switch e.Kind {
		case KindAdded:
			fmt.Fprintf(&b, "\n  + %s %s\n", e.Adapter, e.Summary)
		case KindRemoved:
			fmt.Fprintf(&b, "\n  - %s %s\n", e.Adapter, e.Summary)
		case KindModified:
			info := ""
			if classifyRead != nil && classifyRead(e.Adapter, nil) {
				info = " (informational only — read-class payload)"
			}
			fmt.Fprintf(&b, "\n  ~ %s %s%s\n", e.Adapter, e.Summary, info)
			if e.ALine != "" {
				fmt.Fprintf(&b, "      apply #1: %s\n", e.ALine)
			}
			if e.BLine != "" {
				fmt.Fprintf(&b, "      apply #2: %s\n", e.BLine)
			}
		}
	}
	fmt.Fprintf(&b, "\ncassettes-1: %s\ncassettes-2: %s\n", d.A, d.B)
	return b.String()
}

// envelope is the minimal subset of xrr's on-disk envelope the
// diff needs to read. recorded_at is stripped before comparison.
type envelope struct {
	Adapter     string         `yaml:"adapter"`
	Fingerprint string         `yaml:"fingerprint"`
	Error       string         `yaml:"error,omitempty"`
	Payload     map[string]any `yaml:"payload"`
}

// pair groups the req/resp envelopes for one cassette key.
type pair struct {
	req  *envelope
	resp *envelope
}

// loadDir walks dir and returns a map keyed by "<adapter>-<fp>".
// Files that don't match the xrr naming convention are skipped.
func loadDir(dir string) (map[string]*pair, error) {
	out := map[string]*pair{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		// Expected: {adapter}-{fp}.{req|resp}.yaml
		stem := strings.TrimSuffix(name, ".yaml")
		var kind string
		switch {
		case strings.HasSuffix(stem, ".req"):
			kind = "req"
			stem = strings.TrimSuffix(stem, ".req")
		case strings.HasSuffix(stem, ".resp"):
			kind = "resp"
			stem = strings.TrimSuffix(stem, ".resp")
		default:
			continue
		}
		dash := strings.IndexByte(stem, '-')
		if dash < 0 {
			continue
		}
		key := stem // adapter-fp serves as the bundle key
		_ = dash
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("diff: read %s: %w", name, err)
		}
		var env envelope
		if err := yaml.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("diff: parse %s: %w", name, err)
		}
		p, ok := out[key]
		if !ok {
			p = &pair{}
			out[key] = p
		}
		if kind == "req" {
			p.req = &env
		} else {
			p.resp = &env
		}
	}
	return out, nil
}

// Cassettes computes the diff between two cassette directories. A
// missing directory is treated as an empty dir; callers needing
// stricter behavior should pre-stat.
func Cassettes(a, b string) (Diff, error) {
	pa, err := loadDir(a)
	if err != nil {
		return Diff{}, err
	}
	pb, err := loadDir(b)
	if err != nil {
		return Diff{}, err
	}
	d := Diff{A: a, B: b}

	// Added (in B, not in A).
	for k, pb := range pb {
		if _, ok := pa[k]; !ok {
			d.Entries = append(d.Entries, entryFromPair(KindAdded, k, pb))
		}
	}
	// Removed (in A, not in B).
	for k, pa := range pa {
		if _, ok := pb[k]; !ok {
			d.Entries = append(d.Entries, entryFromPair(KindRemoved, k, pa))
		}
	}
	// Modified (same key, different resp payload modulo recorded_at).
	for k, paA := range pa {
		paB, ok := pb[k]
		if !ok {
			continue
		}
		if !payloadEqual(paA.resp, paB.resp) {
			e := entryFromPair(KindModified, k, paA)
			e.ALine = oneLineYAML(payloadOf(paA.resp))
			e.BLine = oneLineYAML(payloadOf(paB.resp))
			d.Entries = append(d.Entries, e)
		}
	}
	sort.SliceStable(d.Entries, func(i, j int) bool {
		if d.Entries[i].Kind != d.Entries[j].Kind {
			return kindOrder(d.Entries[i].Kind) < kindOrder(d.Entries[j].Kind)
		}
		if d.Entries[i].Adapter != d.Entries[j].Adapter {
			return d.Entries[i].Adapter < d.Entries[j].Adapter
		}
		return d.Entries[i].Fingerprint < d.Entries[j].Fingerprint
	})
	return d, nil
}

func kindOrder(k Kind) int {
	switch k {
	case KindAdded:
		return 0
	case KindRemoved:
		return 1
	case KindModified:
		return 2
	}
	return 99
}

// entryFromPair constructs the basic Entry; Kind, ALine, BLine are
// filled by the caller.
func entryFromPair(k Kind, key string, p *pair) Entry {
	adapter, fp := splitKey(key)
	if p != nil && p.req != nil {
		adapter = nonEmpty(p.req.Adapter, adapter)
		fp = nonEmpty(p.req.Fingerprint, fp)
	}
	return Entry{
		Kind:        k,
		Adapter:     adapter,
		Fingerprint: fp,
		Summary:     summarize(adapter, payloadOf(p.req)),
	}
}

func splitKey(s string) (adapter, fp string) {
	dash := strings.LastIndexByte(s, '-')
	if dash < 0 {
		return s, ""
	}
	return s[:dash], s[dash+1:]
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func payloadOf(e *envelope) map[string]any {
	if e == nil {
		return nil
	}
	return e.Payload
}

// payloadEqual compares two resp envelopes ignoring recorded_at
// (which is never in Payload; it's a top-level field stripped at
// load time). Comparison is on the payload map plus the error
// field — both must agree.
func payloadEqual(a, b *envelope) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Error != b.Error {
		return false
	}
	return deepEqual(a.Payload, b.Payload)
}

// deepEqual walks two YAML-decoded values recursively. Maps with
// equal key sets and equal values match regardless of order; slices
// compare element-wise; scalars compare by ==.
func deepEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !deepEqual(v, bv[k]) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func oneLineYAML(v any) string {
	if v == nil {
		return ""
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	s := strings.TrimSpace(string(out))
	s = strings.ReplaceAll(s, "\n", " ; ")
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}

// summarize renders a compact one-line description of the request
// payload for use in the failure message.
func summarize(adapter string, payload map[string]any) string {
	if payload == nil {
		return ""
	}
	switch adapter {
	case "http":
		method, _ := payload["method"].(string)
		url, _ := payload["url"].(string)
		return fmt.Sprintf("%s %s", method, url)
	case "sql":
		q, _ := payload["query"].(string)
		q = strings.TrimSpace(q)
		if i := strings.IndexByte(q, '\n'); i > 0 {
			q = q[:i]
		}
		if len(q) > 120 {
			q = q[:117] + "..."
		}
		return q
	case "redis":
		cmd, _ := payload["command"].(string)
		args, _ := payload["args"].([]any)
		parts := []string{strings.ToUpper(cmd)}
		for _, a := range args {
			parts = append(parts, fmt.Sprintf("%v", a))
		}
		return strings.Join(parts, " ")
	case "grpc":
		svc, _ := payload["service"].(string)
		m, _ := payload["method"].(string)
		return fmt.Sprintf("%s/%s", svc, m)
	case "exec":
		argv, _ := payload["argv"].([]any)
		parts := make([]string, 0, len(argv))
		for _, a := range argv {
			parts = append(parts, fmt.Sprintf("%v", a))
		}
		return strings.Join(parts, " ")
	case "fs":
		op, _ := payload["op"].(string)
		path, _ := payload["path"].(string)
		return fmt.Sprintf("%s %s", op, path)
	}
	return ""
}

// LookupRequest exposes the parsed req payload for a given
// (adapter, fingerprint) in dir. Used by the harness to drive
// classification against the canonical request shape.
func LookupRequest(dir, adapter, fp string) (map[string]any, error) {
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.req.yaml", adapter, fp))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := yaml.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return env.Payload, nil
}

// ListInteractions enumerates the (adapter, fingerprint, payload)
// triples in dir. Useful for AssertDryRunNoMutation which iterates
// every interaction and asks the classifier whether it's Read.
type Interaction struct {
	Adapter     string
	Fingerprint string
	ReqPayload  map[string]any
	RespPayload map[string]any
	Error       string
	Summary     string
}

// List loads dir and returns one Interaction per (adapter, fp)
// pair. Files lacking either a req or resp half are skipped with a
// best-effort attempt to recover the half that is present.
func List(dir string) ([]Interaction, error) {
	pairs, err := loadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Interaction, 0, len(pairs))
	for key, p := range pairs {
		adapter, fp := splitKey(key)
		if p.req != nil {
			adapter = nonEmpty(p.req.Adapter, adapter)
			fp = nonEmpty(p.req.Fingerprint, fp)
		} else if p.resp != nil {
			adapter = nonEmpty(p.resp.Adapter, adapter)
			fp = nonEmpty(p.resp.Fingerprint, fp)
		}
		it := Interaction{
			Adapter:     adapter,
			Fingerprint: fp,
			ReqPayload:  payloadOf(p.req),
			RespPayload: payloadOf(p.resp),
			Summary:     summarize(adapter, payloadOf(p.req)),
		}
		if p.resp != nil {
			it.Error = p.resp.Error
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Adapter != out[j].Adapter {
			return out[i].Adapter < out[j].Adapter
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out, nil
}
