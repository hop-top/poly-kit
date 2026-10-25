package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hop.top/kit/go/runtime/bus"
)

// Runs the shared corpus in contracts/statemachine-v1. The vectors are
// the source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

const smConfDir = "../../../contracts/statemachine-v1"

// smConfFiles is every vector file this runner consumes; the corpus
// directory must match it exactly.
var smConfFiles = map[string]bool{"statemachine.json": true}

type smConfTopics struct {
	PreTransitioned  string `json:"pre_transitioned"`
	PostTransitioned string `json:"post_transitioned"`
}

type smConfFile struct {
	Schema        string             `json:"$schema"`
	ID            string             `json:"$id"`
	Version       string             `json:"version"`
	Description   string             `json:"description"`
	Source        string             `json:"source"`
	DefaultTopics smConfTopics       `json:"default_topics"`
	Rules         map[State][]State  `json:"rules"`
	Transitions   []smConfTransition `json:"transitions"`
	Errors        []struct {
		Name    string  `json:"name"`
		From    State   `json:"from"`
		To      State   `json:"to"`
		Allowed []State `json:"allowed"`
		Error   string  `json:"error"`
	} `json:"transition_errors"`
	Vetoes []struct {
		Name  string `json:"name"`
		Veto  string `json:"veto"`
		Error string `json:"error"`
	} `json:"vetoes"`
	Payloads []struct {
		Name  string `json:"name"`
		From  State  `json:"from"`
		To    State  `json:"to"`
		Force bool   `json:"force"`
		JSON  string `json:"json"`
	} `json:"payloads"`
	Prefixes []struct {
		Name             string `json:"name"`
		Prefix           string `json:"prefix"`
		PreTransitioned  string `json:"pre_transitioned"`
		PostTransitioned string `json:"post_transitioned"`
		Panic            string `json:"panic"`
	} `json:"topic_prefixes"`
	Overrides []struct {
		Name             string `json:"name"`
		PreTransitioned  string `json:"pre_transitioned"`
		PostTransitioned string `json:"post_transitioned"`
		WantPre          string `json:"want_pre"`
		WantPost         string `json:"want_post"`
	} `json:"topic_overrides"`
}

type smConfTransition struct {
	Name    string `json:"name"`
	From    State  `json:"from"`
	To      State  `json:"to"`
	Force   bool   `json:"force"`
	Error   string `json:"error"`
	Payload string `json:"payload"`
}

func loadSMConf(t *testing.T) smConfFile {
	t.Helper()
	dir := filepath.FromSlash(smConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && !smConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "statemachine.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f smConfFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("parse statemachine.json: %v", err)
	}
	if f.Version != "v1" {
		t.Fatalf("version = %q, want v1", f.Version)
	}
	return f
}

// smConfEvent is one observed publish, the payload as Go's JSON bytes.
type smConfEvent struct{ topic, source, payload string }

// smConfRecorder records every publish on a channel.
type smConfRecorder chan smConfEvent

func (r smConfRecorder) Publish(_ context.Context, topic, source string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	r <- smConfEvent{topic, source, string(b)}
	return nil
}

func (r smConfRecorder) next(t *testing.T, name string) (smConfEvent, bool) {
	t.Helper()
	select {
	case e := <-r:
		return e, true
	case <-time.After(2 * time.Second):
		t.Errorf("%s: timed out waiting for a publish", name)
		return smConfEvent{}, false
	}
}

func TestConformanceStateMachineDefaults(t *testing.T) {
	f := loadSMConf(t)
	d := DefaultStateMachineTopics
	if string(d.PreTransitioned) != f.DefaultTopics.PreTransitioned ||
		string(d.PostTransitioned) != f.DefaultTopics.PostTransitioned {
		t.Errorf("default topics = %+v, want %+v", d, f.DefaultTopics)
	}
	sm := NewStateMachine(f.Rules, nil)
	if sm.topics != d {
		t.Errorf("new machine topics = %+v, want defaults", sm.topics)
	}
}

func TestConformanceStateMachineTransitions(t *testing.T) {
	f := loadSMConf(t)
	if len(f.Transitions) == 0 {
		t.Fatal("transitions section is empty")
	}
	for _, c := range f.Transitions {
		rec := make(smConfRecorder, 2)
		sm := NewStateMachine(f.Rules, rec)
		err := sm.Transition(context.Background(), c.From, c.To, c.Force)
		if c.Error != "" {
			if err == nil || err.Error() != c.Error || !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("%s: err = %v, want invalid transition %q", c.Name, err, c.Error)
			}
			time.Sleep(10 * time.Millisecond)
			if len(rec) != 0 {
				t.Errorf("%s: a rejected transition published %d events", c.Name, len(rec))
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.Name, err)
			continue
		}
		want := []string{f.DefaultTopics.PreTransitioned, f.DefaultTopics.PostTransitioned}
		for _, topic := range want {
			e, ok := rec.next(t, c.Name)
			if !ok {
				break
			}
			if e.topic != topic || e.source != f.Source || e.payload != c.Payload {
				t.Errorf("%s: published %+v, want {%s %s %s}", c.Name, e, topic, f.Source, c.Payload)
			}
		}
	}
}

func TestConformanceStateMachineErrors(t *testing.T) {
	f := loadSMConf(t)
	if len(f.Errors) == 0 {
		t.Fatal("transition_errors section is empty")
	}
	for _, c := range f.Errors {
		te := &TransitionError{From: c.From, To: c.To, Allowed: c.Allowed}
		if got := te.Error(); got != c.Error {
			t.Errorf("%s:\n got %q\nwant %q", c.Name, got, c.Error)
		}
		if !errors.Is(te, ErrInvalidTransition) {
			t.Errorf("%s: not ErrInvalidTransition", c.Name)
		}
	}
}

func TestConformanceStateMachineVetoes(t *testing.T) {
	f := loadSMConf(t)
	if len(f.Vetoes) == 0 {
		t.Fatal("vetoes section is empty")
	}
	for _, c := range f.Vetoes {
		veto := errors.New(c.Veto)
		pub := &smConfVeto{err: veto}
		sm := NewStateMachine(f.Rules, pub)
		err := sm.Transition(context.Background(), "open", "closed", false)
		if err == nil || err.Error() != c.Error || !errors.Is(err, veto) {
			t.Errorf("%s: err = %v, want %q wrapping the veto", c.Name, err, c.Error)
		}
		if errors.Is(err, ErrInvalidTransition) {
			t.Errorf("%s: a veto is not an invalid transition", c.Name)
		}
	}
}

type smConfVeto struct{ err error }

func (p *smConfVeto) Publish(context.Context, string, string, any) error { return p.err }

func TestConformanceStateMachinePayloads(t *testing.T) {
	f := loadSMConf(t)
	if len(f.Payloads) == 0 {
		t.Fatal("payloads section is empty")
	}
	for _, c := range f.Payloads {
		b, err := json.Marshal(TransitionPayload{From: c.From, To: c.To, Force: c.Force})
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.JSON {
			t.Errorf("%s:\n got %s\nwant %s", c.Name, b, c.JSON)
		}
	}
}

func TestConformanceStateMachineTopicPrefixes(t *testing.T) {
	f := loadSMConf(t)
	if len(f.Prefixes) == 0 {
		t.Fatal("topic_prefixes section is empty")
	}
	for _, c := range f.Prefixes {
		got, panicked := smConfPrefix(c.Prefix)
		switch {
		case c.Panic != "":
			if !panicked || got.(string) != c.Panic {
				t.Errorf("%s:\n got panic=%v %v\nwant panic %q", c.Name, panicked, got, c.Panic)
			}
		case panicked:
			t.Errorf("%s: unexpected panic %v", c.Name, got)
		default:
			tp := got.(StateMachineTopics)
			if string(tp.PreTransitioned) != c.PreTransitioned ||
				string(tp.PostTransitioned) != c.PostTransitioned {
				t.Errorf("%s: topics = %+v", c.Name, tp)
			}
		}
	}
}

// smConfPrefix applies WithSMTopicPrefix, returning the topics or the
// recovered panic value.
func smConfPrefix(prefix string) (out any, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			out, panicked = r, true
		}
	}()
	sm := NewStateMachine(nil, nil, WithSMTopicPrefix(prefix))
	return sm.topics, false
}

func TestConformanceStateMachineTopicOverrides(t *testing.T) {
	f := loadSMConf(t)
	if len(f.Overrides) == 0 {
		t.Fatal("topic_overrides section is empty")
	}
	for _, c := range f.Overrides {
		rec := make(smConfRecorder, 2)
		sm := NewStateMachine(f.Rules, rec, WithSMTopics(StateMachineTopics{
			PreTransitioned:  bus.Topic(c.PreTransitioned),
			PostTransitioned: bus.Topic(c.PostTransitioned),
		}))
		if err := sm.Transition(context.Background(), "open", "closed", false); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		var got []string
		for range 2 {
			if e, ok := rec.next(t, c.Name); ok {
				got = append(got, e.topic)
			}
		}
		if fmt.Sprint(got) != fmt.Sprint([]string{c.WantPre, c.WantPost}) {
			t.Errorf("%s: topics = %v, want [%s %s]", c.Name, got, c.WantPre, c.WantPost)
		}
	}
}
