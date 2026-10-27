package consent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Runner for contracts/consent-v1. Strict: unknown fields, a version
// other than v1 and a corpus file no runner reads all fail the run.

const corpusDir = "../../../contracts/consent-v1"

var corpusFiles = map[string]bool{
	"README.md":         true,
	"capabilities.json": true,
	"records.json":      true,
	"scenarios.json":    true,
	"migration.json":    true,
	"delivery.json":     true,
}

// corpusTool is the tool every runner uses for per-tool state.
const corpusTool = "mytool"

func loadCorpus(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpusDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

func checkVersion(t *testing.T, name, v string) {
	t.Helper()
	if v != "v1" {
		t.Fatalf("%s: version %q, want v1", name, v)
	}
}

func TestConformanceConsentCorpusFiles(t *testing.T) {
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !corpusFiles[e.Name()] {
			t.Errorf("corpus file %s has no runner", e.Name())
		}
	}
}

type idCase struct {
	ID        *string `json:"id"`
	Subject   *string `json:"subject"`
	OK        bool    `json:"ok"`
	ErrorKind string  `json:"error_kind"`
}

func TestConformanceConsentCapabilities(t *testing.T) {
	var doc struct {
		Version      string   `json:"version"`
		Description  string   `json:"description"`
		Capabilities []idCase `json:"capabilities"`
		Subjects     []idCase `json:"subjects"`
	}
	loadCorpus(t, "capabilities.json", &doc)
	checkVersion(t, "capabilities.json", doc.Version)
	for _, c := range doc.Capabilities {
		assertKind(t, "capability "+*c.ID, ValidateCapability(*c.ID), c.OK, c.ErrorKind)
	}
	for _, c := range doc.Subjects {
		assertKind(t, "subject "+*c.Subject, ValidateSubject(*c.Subject), c.OK, c.ErrorKind)
	}
}

func assertKind(t *testing.T, what string, err error, ok bool, kind string) {
	t.Helper()
	if ok {
		if err != nil {
			t.Errorf("%s: unexpected error %v", what, err)
		}
		return
	}
	if err == nil {
		t.Errorf("%s: want error kind %s, got nil", what, kind)
		return
	}
	if got := KindOf(err); string(got) != kind {
		t.Errorf("%s: error kind %q, want %q (%v)", what, got, kind, err)
	}
}

// grantJSON is the corpus shape of a Grant.
type grantJSON struct {
	Capability     string `json:"capability"`
	Subject        string `json:"subject"`
	Decision       string `json:"decision"`
	Duration       string `json:"duration"`
	DecidedAt      string `json:"decided_at,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	DecisionSource string `json:"decision_source"`
	PromptVersion  int    `json:"prompt_version"`
}

func (g grantJSON) toGrant(t *testing.T) Grant {
	t.Helper()
	out := Grant{
		Capability:     Capability(g.Capability),
		Subject:        g.Subject,
		Decision:       Effect(g.Decision),
		Duration:       Duration(g.Duration),
		DecisionSource: DecisionSource(g.DecisionSource),
		PromptVersion:  g.PromptVersion,
	}
	out.DecidedAt = mustTime(t, g.DecidedAt)
	out.ExpiresAt = mustTime(t, g.ExpiresAt)
	return out
}

func fromGrant(g Grant) grantJSON {
	out := grantJSON{
		Capability:     string(g.Capability),
		Subject:        g.Subject,
		Decision:       string(g.Decision),
		Duration:       string(g.Duration),
		DecisionSource: string(g.DecisionSource),
		PromptVersion:  g.PromptVersion,
	}
	if !g.DecidedAt.IsZero() {
		out.DecidedAt = g.DecidedAt.UTC().Format(time.RFC3339)
	}
	if !g.ExpiresAt.IsZero() {
		out.ExpiresAt = g.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return out
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	if s == "" {
		return time.Time{}
	}
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("corpus time %q: %v", s, err)
	}
	return v
}

// corpusStore seeds a FileStore for corpusTool under fresh XDG dirs.
func corpusStore(t *testing.T, configYAML, legacyYAML, grantsYAML *string) *FileStore {
	t.Helper()
	_, dir := newTestStore(t)
	s, err := NewFileStoreFor(corpusTool)
	if err != nil {
		t.Fatal(err)
	}
	write := func(p string, body *string) {
		if body == nil {
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(*body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "kit", "config.yaml"), configYAML)
	write(filepath.Join(dir, "kit", "telemetry.yaml"), legacyYAML)
	write(s.grantsPath, grantsYAML)
	return s
}

func TestConformanceConsentRecords(t *testing.T) {
	var doc struct {
		Version     string `json:"version"`
		Description string `json:"description"`
		Cases       []struct {
			Name       string      `json:"name"`
			ConfigYAML *string     `json:"config_yaml"`
			GrantsYAML *string     `json:"grants_yaml"`
			OK         bool        `json:"ok"`
			Grants     []grantJSON `json:"grants"`
			ErrorKind  string      `json:"error_kind"`
		} `json:"cases"`
	}
	loadCorpus(t, "records.json", &doc)
	checkVersion(t, "records.json", doc.Version)
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			s := corpusStore(t, c.ConfigYAML, nil, c.GrantsYAML)
			got, err := s.Grants(context.Background())
			if !c.OK {
				assertKind(t, c.Name, err, false, c.ErrorKind)
				return
			}
			if err != nil {
				t.Fatalf("Grants: %v", err)
			}
			assertGrants(t, got, c.Grants)
		})
	}
}

func assertGrants(t *testing.T, got []Grant, want []grantJSON) {
	t.Helper()
	gj := make([]grantJSON, 0, len(got))
	for _, g := range got {
		gj = append(gj, fromGrant(g))
	}
	if want == nil {
		want = []grantJSON{}
	}
	if !reflect.DeepEqual(gj, want) {
		t.Fatalf("grants:\n got %+v\nwant %+v", gj, want)
	}
}

type refusedJSON struct {
	Reason     string `json:"reason"`
	Code       string `json:"code"`
	ExitCode   int    `json:"exit_code"`
	Transience string `json:"transience"`
}

type expectJSON struct {
	Allowed   *bool        `json:"allowed"`
	Reason    string       `json:"reason"`
	OK        *bool        `json:"ok"`
	Prompted  *bool        `json:"prompted"`
	Refused   *refusedJSON `json:"refused"`
	ErrorKind string       `json:"error_kind"`
	Removed   *bool        `json:"removed"`
	Grants    []grantJSON  `json:"grants"`
}

type stepJSON struct {
	Op         string      `json:"op"`
	Grant      *grantJSON  `json:"grant"`
	Capability string      `json:"capability"`
	Subject    string      `json:"subject"`
	Source     string      `json:"source"`
	Seconds    int         `json:"seconds"`
	Prompter   string      `json:"prompter"`
	Answer     *answerJSON `json:"answer"`
	Expect     *expectJSON `json:"expect"`
}

type answerJSON struct {
	Decision  string `json:"decision"`
	Duration  string `json:"duration"`
	ExpiresAt string `json:"expires_at"`
}

type scenarioJSON struct {
	Name          string            `json:"name"`
	Now           string            `json:"now"`
	Env           map[string]string `json:"env"`
	PromptVersion *int              `json:"prompt_version"`
	ConfigYAML    *string           `json:"config_yaml"`
	LegacyYAML    *string           `json:"legacy_yaml"`
	GrantsYAML    *string           `json:"grants_yaml"`
	Steps         []stepJSON        `json:"steps"`
	Audit         []json.RawMessage `json:"audit"`
	GrantsFile    *json.RawMessage  `json:"grants_file"`
	ConfigFile    *json.RawMessage  `json:"config_file"`
	LegacyFile    *json.RawMessage  `json:"legacy_file"`
}

// presence records whether config_file / legacy_file keys appear at
// all, since a JSON null there means "the file must not exist".
type presence struct {
	GrantsFile json.RawMessage `json:"grants_file"`
	ConfigFile json.RawMessage `json:"config_file"`
	LegacyFile json.RawMessage `json:"legacy_file"`
}

func TestConformanceConsentScenarios(t *testing.T) { runScenarioFile(t, "scenarios.json") }
func TestConformanceConsentMigration(t *testing.T) { runScenarioFile(t, "migration.json") }

func runScenarioFile(t *testing.T, name string) {
	var doc struct {
		Version     string         `json:"version"`
		Description string         `json:"description"`
		Scenarios   []scenarioJSON `json:"scenarios"`
	}
	loadCorpus(t, name, &doc)
	checkVersion(t, name, doc.Version)
	var raw struct {
		Scenarios []presence `json:"scenarios"`
	}
	b, _ := os.ReadFile(filepath.Join(corpusDir, name))
	_ = json.Unmarshal(b, &raw)
	for i, sc := range doc.Scenarios {
		t.Run(sc.Name, func(t *testing.T) { runScenario(t, sc, raw.Scenarios[i]) })
	}
}

func runScenario(t *testing.T, sc scenarioJSON, pres presence) {
	ctx := context.Background()
	store := corpusStore(t, sc.ConfigYAML, sc.LegacyYAML, sc.GrantsYAML)
	now := mustTime(t, sc.Now)
	var audit []AuditEvent
	pv := 1
	if sc.PromptVersion != nil {
		pv = *sc.PromptVersion
	}
	open := func() *Grants {
		return NewGrants(store,
			WithClock(func() time.Time { return now }),
			WithEnv(MapEnv(sc.Env)),
			WithPromptVersion(pv),
			WithAuditor(AuditorFunc(func(_ context.Context, e AuditEvent) error {
				audit = append(audit, e)
				return nil
			})),
		)
	}
	g := open()
	for i, st := range sc.Steps {
		runStep(t, ctx, i, st, &g, open, &now)
	}

	gotAudit := make([]json.RawMessage, 0, len(audit))
	for _, e := range audit {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		gotAudit = append(gotAudit, b)
	}
	assertJSONEqual(t, "audit", mustJSON(t, gotAudit), mustJSON(t, nonNil(sc.Audit)))

	if len(pres.GrantsFile) > 0 {
		assertFile(t, "grants_file", store.grantsPath, pres.GrantsFile)
	}
	if len(pres.ConfigFile) > 0 {
		assertFile(t, "config_file", store.path, pres.ConfigFile)
	}
	if len(pres.LegacyFile) > 0 {
		assertFile(t, "legacy_file", store.legacyPath, pres.LegacyFile)
	}
}

func nonNil(v []json.RawMessage) []json.RawMessage {
	if v == nil {
		return []json.RawMessage{}
	}
	return v
}

func runStep(t *testing.T, ctx context.Context, i int, st stepJSON, g **Grants, open func() *Grants, now *time.Time) {
	t.Helper()
	ex := st.Expect
	if ex == nil {
		ex = &expectJSON{}
	}
	where := func(s string) string { return fmt.Sprintf("step %d %s", i, s) }
	switch st.Op {
	case "decide":
		gr := st.Grant.toGrant(t)
		err := (*g).Decide(ctx, gr)
		expectErr(t, where("decide"), err, ex.ErrorKind)
	case "check":
		v, err := (*g).Check(ctx, Capability(st.Capability), st.Subject)
		if ex.ErrorKind != "" {
			expectErr(t, where("check"), err, ex.ErrorKind)
			return
		}
		if err != nil {
			t.Fatalf("%s: %v", where("check"), err)
		}
		if v.Allowed != *ex.Allowed || string(v.Reason) != ex.Reason {
			t.Fatalf("%s: got allowed=%v reason=%s, want allowed=%v reason=%s",
				where("check"), v.Allowed, v.Reason, *ex.Allowed, ex.Reason)
		}
	case "use":
		_, err := (*g).Use(ctx, Capability(st.Capability), st.Subject)
		expectOutcome(t, where("use"), err, ex)
	case "request":
		prompted := false
		var p Prompter
		switch {
		case st.Prompter == "none":
		case st.Prompter == "error":
			p = PrompterFunc(func(context.Context, Prompt) (Answer, error) {
				prompted = true
				return Answer{}, errors.New("prompt: no tty")
			})
		case st.Answer != nil:
			a := Answer{
				Decision:  Effect(st.Answer.Decision),
				Duration:  Duration(st.Answer.Duration),
				ExpiresAt: mustTime(t, st.Answer.ExpiresAt),
			}
			p = PrompterFunc(func(context.Context, Prompt) (Answer, error) {
				prompted = true
				return a, nil
			})
		default:
			t.Fatalf("%s: request needs prompter or answer", where("request"))
		}
		_, err := (*g).Request(ctx, Capability(st.Capability), st.Subject, p)
		expectOutcome(t, where("request"), err, ex)
		if ex.Prompted != nil && prompted != *ex.Prompted {
			t.Fatalf("%s: prompted=%v, want %v", where("request"), prompted, *ex.Prompted)
		}
	case "revoke":
		removed, err := (*g).Revoke(ctx, Capability(st.Capability), st.Subject, DecisionSource(st.Source))
		if ex.ErrorKind != "" {
			expectErr(t, where("revoke"), err, ex.ErrorKind)
			return
		}
		if err != nil {
			t.Fatalf("%s: %v", where("revoke"), err)
		}
		if removed != *ex.Removed {
			t.Fatalf("%s: removed=%v, want %v", where("revoke"), removed, *ex.Removed)
		}
	case "list":
		got, err := (*g).List(ctx)
		if err != nil {
			t.Fatalf("%s: %v", where("list"), err)
		}
		assertGrants(t, got, ex.Grants)
	case "advance":
		*now = now.Add(time.Duration(st.Seconds) * time.Second)
	case "end_session":
		(*g).EndSession()
	case "reopen":
		*g = open()
	default:
		t.Fatalf("unknown op %q", st.Op)
	}
}
