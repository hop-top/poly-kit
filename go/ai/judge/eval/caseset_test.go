package eval_test

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/eval"
	"hop.top/kit/go/ai/judge/judgetest"
)

func sampleEntries() []eval.Entry {
	c := judge.Case{
		State: map[string]any{"subject": "hello", "n": "3"},
		Questions: []judge.Question{
			{Name: "urgent", Kind: judge.KindYesNo, Abstain: true},
			{Name: "tone", Kind: judge.KindChoice, Options: []judge.Option{{Value: "warm", Description: "friendly"}, {Value: "cold"}}},
			{Name: "quality", Kind: judge.KindScore, Anchors: []string{"bad", "good"}},
		},
	}
	v := judge.Verdict{
		Answers: map[string]judge.Answer{"urgent": judgetest.Choice(map[string]float64{"yes": .75, "no": .25}, "yes", "no")},
		Model:   "m", Backend: "b",
	}
	return []eval.Entry{
		{Case: c},
		{Case: c, Reference: &eval.Reference{Labels: map[string]string{"urgent": "yes", "tone": "warm"}}},
		{Case: c, Reference: &eval.Reference{Verdict: &v}},
	}
}

func TestJSONLRoundTrip(t *testing.T) {
	want := sampleEntries()
	var buf bytes.Buffer
	if err := eval.Save(&buf, want); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 3 {
		t.Errorf("lines = %d, want 3 (one object per line)", n)
	}
	got, err := eval.Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadSkipsBlankLines(t *testing.T) {
	var buf bytes.Buffer
	if err := eval.Save(&buf, sampleEntries()[:1]); err != nil {
		t.Fatal(err)
	}
	in := "\n  \n" + buf.String() + "\n"
	got, err := eval.Load(strings.NewReader(in))
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d entries, err %v", len(got), err)
	}
}

func TestLoadErrorNamesLine(t *testing.T) {
	in := `{"case":{"State":{},"Questions":[]}}` + "\n" + `{not json` + "\n"
	_, err := eval.Load(strings.NewReader(in))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("err = %v, want line 2", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	_, err := eval.Load(strings.NewReader(`{"case":{},"extra":{}}` + "\n"))
	if err == nil {
		t.Error("typo'd field accepted")
	}
}

func TestLoadLongLine(t *testing.T) {
	c := judge.Case{
		State:     map[string]any{"blob": strings.Repeat("x", 200_000)},
		Questions: []judge.Question{{Name: "q", Kind: judge.KindYesNo}},
	}
	var buf bytes.Buffer
	if err := eval.Save(&buf, []eval.Entry{{Case: c}}); err != nil {
		t.Fatal(err)
	}
	got, err := eval.Load(&buf)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d, err %v", len(got), err)
	}
}

func TestFileHelpers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "set.jsonl")
	want := sampleEntries()
	if err := eval.SaveFile(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := eval.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Error("file round trip mismatch")
	}
	if _, err := eval.LoadFile(path + ".missing"); err == nil {
		t.Error("missing file: want error")
	}
}

func TestRecord(t *testing.T) {
	cs := ynCases(3, false)
	j := byIndex(yn(.9), yn(.1), yn(.5))
	es, err := eval.Record(context.Background(), j, cs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 {
		t.Fatalf("entries = %d", len(es))
	}
	for i, e := range es {
		if e.Reference == nil || e.Reference.Verdict == nil {
			t.Fatalf("entry %d has no recorded verdict", i)
		}
	}
	if got := es[1].Reference.Verdict.Answers["q"].Probs["yes"]; got < .09 || got > .11 {
		t.Errorf("entry 1 recorded P(yes) = %v", got)
	}
	// recorded set replays with no ref judge
	rep, err := eval.Compare(context.Background(), nil, j, es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "agreement", question(t, rep, "q").Agreement, 1)
}
