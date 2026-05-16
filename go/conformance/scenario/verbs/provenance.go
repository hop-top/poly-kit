package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"hop.top/kit/go/console/cli/conformance/harness/diff"
)

// provenance_present: { paths?: []string }
//
// Inspects the on-step's stdout for the kit/provenance envelope
// (Render output with ModeStrict or ModeWarn). The envelope has the
// shape:
//
//	{
//	  "data": {...},
//	  "provenance": {
//	    "<JSON-pointer-path>": { "schema_version": "1", "source": "...", ... },
//	    ...
//	  }
//	}
//
// Pass: envelope present and (if paths: is set) every requested
// path has a non-empty provenance entry.

func init() {
	register(&Entry{
		Kind:     KindProvenancePresent,
		Validate: validateProvenancePresent,
		Evaluate: evalProvenancePresent,
	})
	register(&Entry{
		Kind:     KindProvenanceMatchesCassette,
		Validate: nil,
		Evaluate: evalProvenanceMatchesCassette,
	})
}

func validateProvenancePresent(args map[string]any) []string {
	if raw, ok := args["paths"]; ok {
		list, ok := raw.([]any)
		if !ok {
			return []string{"paths must be a list of strings"}
		}
		for i, v := range list {
			if _, ok := v.(string); !ok {
				return []string{fmt.Sprintf("paths[%d] is not a string", i)}
			}
		}
	}
	return nil
}

// envelope is the minimal wire shape provenance.Render emits.
type envelope struct {
	Data       json.RawMessage            `json:"data"`
	Provenance map[string]json.RawMessage `json:"provenance"`
}

func decodeEnvelope(stdout []byte) (*envelope, error) {
	if len(stdout) == 0 {
		return nil, fmt.Errorf("stdout empty")
	}
	var env envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		return nil, fmt.Errorf("stdout not JSON: %w", err)
	}
	if env.Provenance == nil {
		return nil, fmt.Errorf("envelope missing provenance key — adopter must run --strict or set prov.SetMode(ModeWarn)")
	}
	return &env, nil
}

func evalProvenancePresent(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	env, err := decodeEnvelope(vctx.Capture.Stdout)
	if err != nil {
		return Fail(nil, "provenance envelope present", err.Error())
	}
	rawPaths, _ := spec.Args["paths"].([]any)
	if len(rawPaths) == 0 {
		// All declared entries must be non-empty.
		var missing []string
		for k, v := range env.Provenance {
			if len(v) == 0 || string(v) == "null" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return Fail(missing, "non-empty entries",
				fmt.Sprintf("%d provenance entries are empty", len(missing)))
		}
		return EvalResult{Status: StatusPass, Observed: len(env.Provenance), Expected: "all entries non-empty"}
	}
	var missing []string
	for _, p := range rawPaths {
		s, _ := p.(string)
		if s == "" {
			continue
		}
		v, ok := env.Provenance[s]
		if !ok || len(v) == 0 || string(v) == "null" {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		return Fail(missing, "all paths have provenance",
			fmt.Sprintf("paths missing provenance: %v", missing))
	}
	return EvalResult{Status: StatusPass, Expected: "paths have provenance"}
}

// provEntry is the minimal provenance shape we inspect for the
// matches-cassette check. We only need URL.
type provEntry struct {
	URL string `json:"url"`
}

// evalProvenanceMatchesCassette cross-references declared
// provenance URLs against URLs recorded in the cassette.
// For each provenance entry with a non-empty URL, we walk the
// cassette for an interaction whose canonical identifier matches.
func evalProvenanceMatchesCassette(_ context.Context, _ AssertionSpec, vctx VerbContext) EvalResult {
	env, err := decodeEnvelope(vctx.Capture.Stdout)
	if err != nil {
		return Fail(nil, "envelope present", err.Error())
	}
	items, ierr := diff.List(vctx.Capture.CassetteDir)
	if ierr != nil {
		return Ungradable("provenance_matches_cassette: " + ierr.Error())
	}
	// Index cassette URLs/identifiers by adapter.
	urls := collectCassetteURLs(items)

	var unmatched []string
	for path, raw := range env.Provenance {
		var p provEntry
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.URL == "" {
			continue
		}
		if !urlMatchesAny(p.URL, urls) {
			unmatched = append(unmatched, fmt.Sprintf("%s=%s", path, p.URL))
		}
	}
	if len(unmatched) > 0 {
		return Fail(unmatched, "all URLs in cassette",
			fmt.Sprintf("declared provenance URL(s) not in cassette: %v", unmatched))
	}
	return EvalResult{Status: StatusPass, Expected: "URLs match cassette"}
}

// collectCassetteURLs builds a set of (adapter, identifier) strings
// the cassette recorded. Per design §9 v1 conventions:
//   - http: req.url
//   - sql: req.dsn (DB-level) — falls back to "sql://"
//   - exec: "exec://" + argv[0]
//   - redis: "redis://" + req.addr (best-effort)
//   - grpc: "grpc://" + service + "/" + method
func collectCassetteURLs(items []diff.Interaction) []string {
	out := []string{}
	for _, it := range items {
		switch it.Adapter {
		case "http":
			if u, _ := it.ReqPayload["url"].(string); u != "" {
				out = append(out, u)
			}
		case "sql":
			if dsn, _ := it.ReqPayload["dsn"].(string); dsn != "" {
				out = append(out, dsn)
			}
			out = append(out, "sql://")
		case "exec":
			if argv, ok := it.ReqPayload["argv"].([]any); ok && len(argv) > 0 {
				out = append(out, "exec://"+fmt.Sprintf("%v", argv[0]))
			}
		case "redis":
			if addr, _ := it.ReqPayload["addr"].(string); addr != "" {
				out = append(out, "redis://"+addr)
			}
			out = append(out, "redis://")
		case "grpc":
			svc, _ := it.ReqPayload["service"].(string)
			mth, _ := it.ReqPayload["method"].(string)
			if svc != "" || mth != "" {
				out = append(out, fmt.Sprintf("grpc://%s/%s", svc, mth))
			}
		}
	}
	return out
}

// urlMatchesAny reports whether want appears in the set, using
// prefix match for adapter-scheme URLs so that a declared
// "exec://kubectl" matches a recorded "exec://kubectl" or longer.
func urlMatchesAny(want string, set []string) bool {
	for _, u := range set {
		if u == want {
			return true
		}
		if strings.HasPrefix(want, u) || strings.HasPrefix(u, want) {
			return true
		}
	}
	return false
}
