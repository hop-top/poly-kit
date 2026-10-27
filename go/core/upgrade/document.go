package upgrade

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DocStep moves a JSON document from version From to To (To > From).
// It mirrors config.Migration (From, To, Apply, Note) so YAML config and
// JSON documents share one model; Verify follows Migration.Verify.
//
// Apply gets a private copy of the document and may mutate it in place;
// it returns the migrated document. The version field is set to To
// after Apply, so Apply need not touch it.
type DocStep struct {
	From   int
	To     int
	Apply  func(doc map[string]any) (map[string]any, error)
	Verify func(doc map[string]any) error // optional, runs on Apply's result
	Note   string
}

// DocOptions configures MigrateDocument.
type DocOptions struct {
	// VersionField is the dotted path of the integer version; "version"
	// when empty.
	VersionField string
	// Latest is the version this build writes; 0 means the highest To.
	Latest int
	// MissingIsZero reads a missing or non-integer version as 0 instead
	// of failing with ErrDocVersion.
	MissingIsZero bool
}

// DocApplied is one step a migration applied.
type DocApplied struct {
	From int    `json:"from"`
	To   int    `json:"to"`
	Note string `json:"note"`
}

// DocReport says what MigrateDocument did.
type DocReport struct {
	From    int          `json:"from"`
	To      int          `json:"to"`
	Applied []DocApplied `json:"applied"`
}

// Document migration error kinds, for errors.Is. A failed Verify is
// ErrVerify.
var (
	ErrDocNewer   = errors.New("upgrade: document newer than known")
	ErrDocVersion = errors.New("upgrade: document has no integer version")
	ErrDocGap     = errors.New("upgrade: migration chain has a gap")
	ErrDocStep    = errors.New("upgrade: migration step failed")
	ErrDocInvalid = errors.New("upgrade: invalid document or step")
)

type docError struct {
	kind  error
	msg   string
	cause error
}

func (e *docError) Error() string        { return e.msg }
func (e *docError) Unwrap() error        { return e.cause }
func (e *docError) Is(target error) bool { return target == e.kind }

func docErr(kind error, cause error, format string, args ...any) error {
	return &docError{kind: kind, msg: fmt.Sprintf(format, args...), cause: cause}
}

// MigrateDocument migrates one JSON document in memory: no I/O. When the
// document is already at the latest version the input bytes are returned
// as they are; otherwise the result is re-encoded, with keys sorted
// (encoding/json does not keep their order) and numbers kept exact. On
// error it returns nil and the input is untouched.
func MigrateDocument(doc []byte, steps []DocStep, opts DocOptions) ([]byte, DocReport, error) {
	m, err := decodeDocument(doc)
	if err != nil {
		return nil, DocReport{}, err
	}
	out, report, err := migrateDocument(m, steps, opts)
	if err != nil {
		return nil, report, err
	}
	if len(report.Applied) == 0 {
		return doc, report, nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, report, docErr(ErrDocInvalid, err, "encode document: %v", err)
	}
	return b, report, nil
}

// MigrateDocumentMap is MigrateDocument on a decoded document. The
// caller's map is never modified; steps work on a deep copy, whose
// numbers are json.Number (exact). Steps receive that form too.
func MigrateDocumentMap(doc map[string]any, steps []DocStep, opts DocOptions) (map[string]any, DocReport, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, DocReport{}, docErr(ErrDocInvalid, err, "copy document: %v", err)
	}
	m, err := decodeDocument(raw)
	if err != nil {
		return nil, DocReport{}, err
	}
	return migrateDocument(m, steps, opts)
}

func decodeDocument(doc []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, docErr(ErrDocInvalid, err, "parse document: %v", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, docErr(ErrDocInvalid, nil, "document is not a JSON object")
	}
	return m, nil
}

func versionField(opts DocOptions) string {
	if opts.VersionField == "" {
		return "version"
	}
	return opts.VersionField
}

// docVersion reads an integer literal at path.
func docVersion(doc map[string]any, path string) (int, bool) {
	var cur any = doc
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		if cur, ok = m[seg]; !ok {
			return 0, false
		}
	}
	switch n := cur.(type) {
	case json.Number:
		v, err := strconv.Atoi(n.String())
		return v, err == nil
	case int:
		return n, true
	}
	return 0, false
}

func setDocVersion(doc map[string]any, path string, v int) {
	segs := strings.Split(path, ".")
	cur := doc
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = json.Number(strconv.Itoa(v))
}

// migrateDocument runs the chain on doc, which it owns.
func migrateDocument(doc map[string]any, steps []DocStep, opts DocOptions) (map[string]any, DocReport, error) {
	field := versionField(opts)
	cur, ok := docVersion(doc, field)
	if !ok {
		if !opts.MissingIsZero {
			return nil, DocReport{}, docErr(ErrDocVersion, nil, "document has no integer version at %q", field)
		}
		cur = 0
	}
	latest := opts.Latest
	if latest == 0 {
		for _, s := range steps {
			latest = max(latest, s.To)
		}
	}
	report := DocReport{From: cur, To: cur, Applied: []DocApplied{}}
	for _, s := range steps {
		switch {
		case s.To <= s.From:
			return nil, report, docErr(ErrDocInvalid, nil, "migration %d -> %d does not move forward", s.From, s.To)
		case s.To > latest:
			return nil, report, docErr(ErrDocInvalid, nil, "migration %d -> %d goes past the latest known %d", s.From, s.To, latest)
		}
	}
	if cur > latest {
		return nil, report, docErr(ErrDocNewer, nil, "document version %d is newer than the latest known %d", cur, latest)
	}
	for cur < latest {
		step := findDocStep(steps, cur)
		if step == nil {
			return nil, report, docErr(ErrDocGap, nil,
				"no migration from version %d (highest target is %d); chain has a gap", cur, latest)
		}
		out, err := step.Apply(doc)
		if err != nil {
			return nil, report, docErr(ErrDocStep, err, "migration %d -> %d: %v", step.From, step.To, err)
		}
		if out == nil {
			return nil, report, docErr(ErrDocStep, nil, "migration %d -> %d returned nil map", step.From, step.To)
		}
		if step.Verify != nil {
			if err := step.Verify(out); err != nil {
				return nil, report, &docError{kind: ErrVerify, cause: err,
					msg: fmt.Sprintf("migration %d -> %d: verify: %v", step.From, step.To, err)}
			}
		}
		doc = out
		setDocVersion(doc, field, step.To)
		cur = step.To
		report.To = cur
		report.Applied = append(report.Applied, DocApplied{From: step.From, To: step.To, Note: step.Note})
	}
	return doc, report, nil
}

// findDocStep picks the step leaving from, wherever it was given: the
// chain follows versions, not the order of steps.
func findDocStep(steps []DocStep, from int) *DocStep {
	for i := range steps {
		if steps[i].From == from {
			return &steps[i]
		}
	}
	return nil
}
