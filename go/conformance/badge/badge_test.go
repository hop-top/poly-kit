package badge_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"hop.top/kit/go/conformance/badge"
)

// fullReport returns a Report with all 12 factors populated. allPass
// short-circuits each factor's status to Pass; for tailored mixes,
// mutate the returned slice before calling Verdict / WriteJSON.
func fullReport(allPass bool) badge.Report {
	names := []string{
		"Capability Introspection",
		"Intent Clarity",
		"Structured I/O",
		"Corrective Error Model",
		"Explicit Contracts",
		"Previewability",
		"Idempotency",
		"State Transparency",
		"Contextual Guidance",
		"Delegation Safety",
		"Provenance",
		"Evolution Guarantees",
	}
	// Tier assignments mirror the spec's MUST/SHOULD split.
	tiers := []badge.Tier{
		badge.Must, badge.Must, badge.Must, badge.Should,
		badge.Must, badge.Should, badge.Must, badge.Must,
		badge.Should, badge.Must, badge.Should, badge.Must,
	}
	rep := badge.Report{Factors: make([]badge.Factor, 12)}
	for i := range rep.Factors {
		st := badge.Pass
		if !allPass {
			st = badge.Fail
		}
		rep.Factors[i] = badge.Factor{
			N:      i + 1,
			Name:   names[i],
			Tier:   tiers[i],
			Status: st,
		}
	}
	return rep
}

func TestVerdict_AllPass_Brightgreen(t *testing.T) {
	label, msg, color := badge.Verdict(fullReport(true))
	if label != "12-factor AI-CLI" {
		t.Errorf("label = %q, want %q", label, "12-factor AI-CLI")
	}
	if msg != "12/12 pass" {
		t.Errorf("message = %q, want %q", msg, "12/12 pass")
	}
	if color != "brightgreen" {
		t.Errorf("color = %q, want brightgreen", color)
	}
}

func TestVerdict_OneMustFails_Red(t *testing.T) {
	rep := fullReport(true)
	rep.Factors[0].Status = badge.Fail // Factor 1 is MUST
	_, msg, color := badge.Verdict(rep)
	if color != "red" {
		t.Errorf("color = %q, want red", color)
	}
	if !strings.Contains(msg, "MUST fail") {
		t.Errorf("message = %q, want it to mention MUST fail", msg)
	}
}

func TestVerdict_OneShouldFails_DegradeNotRed(t *testing.T) {
	rep := fullReport(true)
	// Factor 4 is SHOULD per fullReport's tier slice.
	rep.Factors[3].Status = badge.Fail
	_, msg, color := badge.Verdict(rep)
	if color == "red" {
		t.Errorf("color = red, want degraded (green/yellow) for SHOULD failure")
	}
	if msg != "11/12 pass" {
		t.Errorf("message = %q, want 11/12 pass", msg)
	}
}

func TestVerdict_TenPassAllMustPass_Green(t *testing.T) {
	rep := fullReport(true)
	// Fail two SHOULD factors (idx 3=F4, 8=F9). All MUST remain pass.
	rep.Factors[3].Status = badge.Fail
	rep.Factors[8].Status = badge.Fail
	_, msg, color := badge.Verdict(rep)
	if color != "green" {
		t.Errorf("color = %q, want green", color)
	}
	if msg != "10/12 pass" {
		t.Errorf("message = %q, want 10/12 pass", msg)
	}
}

func TestVerdict_EightPassNoMustFail_Yellow(t *testing.T) {
	rep := fullReport(true)
	// Fail four SHOULD factors so pass=8, no MUST regression.
	// Tier slice has SHOULD at idx 3, 5, 8, 10.
	for _, i := range []int{3, 5, 8, 10} {
		rep.Factors[i].Status = badge.Fail
	}
	_, msg, color := badge.Verdict(rep)
	if color != "yellow" {
		t.Errorf("color = %q, want yellow", color)
	}
	if msg != "8/12 pass" {
		t.Errorf("message = %q, want 8/12 pass", msg)
	}
}

func TestVerdict_SkipExcludedFromCounts(t *testing.T) {
	rep := fullReport(true)
	// Skipping a factor reduces total; remaining 11/11 should still be
	// considered all-pass for the spec but is not 12/12.
	rep.Factors[5].Status = badge.Skip
	_, msg, _ := badge.Verdict(rep)
	if msg != "11/11 pass" {
		t.Errorf("message = %q, want 11/11 pass (skip excluded from counts)", msg)
	}
}

func TestVerdict_InvalidReport_Ungradable(t *testing.T) {
	label, msg, color := badge.Verdict(badge.Report{}) // 0 factors
	if label != "12-factor AI-CLI" || msg != "ungradable" || color != "lightgrey" {
		t.Errorf("Verdict(empty) = (%q, %q, %q); want (12-factor AI-CLI, ungradable, lightgrey)",
			label, msg, color)
	}
}

func TestValidate_RejectsWrongFactorCount(t *testing.T) {
	rep := badge.Report{Factors: []badge.Factor{{N: 1, Name: "x"}}}
	if err := badge.Validate(rep); err == nil {
		t.Fatal("Validate accepted a report with 1 factor; want error")
	}
}

func TestValidate_RejectsDuplicateFactorN(t *testing.T) {
	rep := fullReport(true)
	rep.Factors[1].N = 1 // duplicate
	if err := badge.Validate(rep); err == nil {
		t.Fatal("Validate accepted duplicate N=1; want error")
	}
}

func TestValidate_RejectsOutOfRangeN(t *testing.T) {
	rep := fullReport(true)
	rep.Factors[0].N = 13
	if err := badge.Validate(rep); err == nil {
		t.Fatal("Validate accepted N=13; want error")
	}
}

func TestValidate_RejectsEmptyName(t *testing.T) {
	rep := fullReport(true)
	rep.Factors[0].Name = ""
	if err := badge.Validate(rep); err == nil {
		t.Fatal("Validate accepted empty Name; want error")
	}
}

func TestWriteJSON_SchemaMatchesShieldsEndpoint(t *testing.T) {
	var buf bytes.Buffer
	if err := badge.WriteJSON(&buf, fullReport(true)); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("emitted JSON not parseable: %v", err)
	}
	want := map[string]any{
		"schemaVersion": float64(1),
		"label":         "12-factor AI-CLI",
		"message":       "12/12 pass",
		"color":         "brightgreen",
		"labelColor":    "555",
		"namedLogo":     "robotframework",
		"cacheSeconds":  float64(300),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("JSON[%s] = %v, want %v", k, got[k], v)
		}
	}
}

func TestWriteJSON_EndsWithNewline(t *testing.T) {
	var buf bytes.Buffer
	if err := badge.WriteJSON(&buf, fullReport(true)); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "}\n") {
		t.Errorf("WriteJSON output does not end with `}\\n`; got tail %q",
			out[max(0, len(out)-10):])
	}
}

func TestWriteJSON_InvalidReport_WritesUngradablePayload(t *testing.T) {
	var buf bytes.Buffer
	if err := badge.WriteJSON(&buf, badge.Report{}); err != nil {
		t.Fatalf("WriteJSON(empty) returned error %v; want graceful ungradable", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("emitted JSON not parseable: %v", err)
	}
	if got["message"] != "ungradable" || got["color"] != "lightgrey" {
		t.Errorf("ungradable payload = %v; want message=ungradable color=lightgrey", got)
	}
}

func TestWriteJSON_NilWriter_Errors(t *testing.T) {
	if err := badge.WriteJSON(nil, fullReport(true)); err == nil {
		t.Fatal("WriteJSON(nil) returned nil; want error")
	}
}

func TestWriteJSON_RespectsExplicitSchemaVersion(t *testing.T) {
	rep := fullReport(true)
	rep.SchemaVersion = 2 // future schema bump
	var buf bytes.Buffer
	if err := badge.WriteJSON(&buf, rep); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(buf.Bytes(), &got)
	if got["schemaVersion"] != float64(2) {
		t.Errorf("schemaVersion = %v, want 2", got["schemaVersion"])
	}
}
