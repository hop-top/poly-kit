package breaker_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	charmlog "charm.land/log/v2"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/runtime/bus"
)

// Runs the shared corpus in contracts/breaker-v1. The vectors are the
// source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

const bkConfDir = "../../../contracts/breaker-v1"

// bkConfFiles is every vector file this runner consumes; the corpus
// directory must match it exactly.
var bkConfFiles = map[string]bool{"breaker.json": true}

type bkTopics struct {
	Tripped    string `json:"tripped"`
	Opened     string `json:"opened"`
	Closed     string `json:"closed"`
	HalfOpened string `json:"half_opened"`
}

type bkConfig struct {
	Circuit *struct {
		FailureThreshold uint  `json:"failure_threshold"`
		SuccessThreshold uint  `json:"success_threshold"`
		DelayMS          int64 `json:"delay_ms"`
	} `json:"circuit"`
	MaxOps   int64 `json:"max_ops"`
	MaxBytes int64 `json:"max_bytes"`
	Rate     *struct {
		Max      int   `json:"max"`
		PeriodMS int64 `json:"period_ms"`
	} `json:"rate"`
	OnTrip      string    `json:"on_trip"`
	Publisher   bool      `json:"publisher"`
	TopicPrefix string    `json:"topic_prefix"`
	Topics      *bkTopics `json:"topics"`
}

type bkStep struct {
	Op           string           `json:"op"`
	Want         string           `json:"want"`
	Success      bool             `json:"success"`
	N            int64            `json:"n"`
	MS           int64            `json:"ms"`
	Reason       string           `json:"reason"`
	Trips        uint64           `json:"trips"`
	LastReason   string           `json:"last_trip_reason"`
	LastTripAtMS *int64           `json:"last_trip_at_ms"`
	Counters     map[string]int64 `json:"counters"`
}

type bkLog struct {
	Level  string         `json:"level"`
	Msg    string         `json:"msg"`
	Fields map[string]any `json:"fields"`
}

type bkEvent struct {
	Topic   string `json:"topic"`
	Payload any    `json:"payload"`
}

type bkScenario struct {
	Name   string     `json:"name"`
	Config bkConfig   `json:"config"`
	Steps  []bkStep   `json:"steps"`
	Logs   []bkLog    `json:"logs"`
	Events *[]bkEvent `json:"events"`
}

type bkFile struct {
	Schema        string       `json:"$schema"`
	ID            string       `json:"$id"`
	Version       string       `json:"version"`
	Description   string       `json:"description"`
	Source        string       `json:"source"`
	DefaultTopics bkTopics     `json:"default_topics"`
	Scenarios     []bkScenario `json:"scenarios"`
	Prefixes      []struct {
		Name   string    `json:"name"`
		Prefix string    `json:"prefix"`
		Topics *bkTopics `json:"topics"`
		Panic  string    `json:"panic"`
	} `json:"topic_prefixes"`
}

func loadBreakerCorpus(t *testing.T) bkFile {
	t.Helper()
	entries, err := os.ReadDir(bkConfDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !bkConfFiles[e.Name()] {
			t.Fatalf("corpus file %s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(bkConfDir, "breaker.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f bkFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode breaker.json: %v", err)
	}
	if f.Version != "v1" {
		t.Fatalf("version %q, want v1", f.Version)
	}
	if f.Description == "" || len(f.Scenarios) == 0 {
		t.Fatal("corpus needs a description and scenarios")
	}
	return f
}

func TestConformanceBreakerConstants(t *testing.T) {
	f := loadBreakerCorpus(t)
	got := bkTopics{
		Tripped:    string(breaker.DefaultTopics.Tripped),
		Opened:     string(breaker.DefaultTopics.Opened),
		Closed:     string(breaker.DefaultTopics.Closed),
		HalfOpened: string(breaker.DefaultTopics.HalfOpened),
	}
	if got != f.DefaultTopics {
		t.Errorf("default topics %+v, want %+v", got, f.DefaultTopics)
	}
	pub := &recPub{}
	name := "conf-source"
	t.Cleanup(func() { breaker.Unregister(name) })
	breaker.New(name, breaker.WithPublisher(pub), breaker.Logger(discardLogger())).Trip("x")
	waitForCount(t, pub, 1)
	if src := pub.snapshot()[0].Source; src != f.Source {
		t.Errorf("source %q, want %q", src, f.Source)
	}
}

func TestConformanceBreakerTopicPrefixes(t *testing.T) {
	for _, v := range loadBreakerCorpus(t).Prefixes {
		t.Run(v.Name, func(t *testing.T) {
			var panicked string
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = fmt.Sprint(r)
					}
				}()
				breaker.WithTopicPrefix(v.Prefix)
			}()
			if v.Panic != "" {
				if panicked != v.Panic {
					t.Fatalf("panic %q, want %q", panicked, v.Panic)
				}
				return
			}
			if panicked != "" {
				t.Fatalf("unexpected panic %q", panicked)
			}
			pub := &recPub{}
			name := "conf-prefix-" + v.Name
			t.Cleanup(func() { breaker.Unregister(name) })
			b := breaker.New(name, breaker.WithPublisher(pub), breaker.Logger(discardLogger()),
				breaker.WithTopicPrefix(v.Prefix))
			b.Trip("x")
			waitForCount(t, pub, 2)
			got := map[string]bool{}
			for _, e := range pub.snapshot() {
				got[e.Topic] = true
			}
			if !got[v.Topics.Tripped] || !got[v.Topics.Opened] {
				t.Fatalf("topics %v, want %+v", got, v.Topics)
			}
		})
	}
}

func TestConformanceBreakerScenarios(t *testing.T) {
	for _, sc := range loadBreakerCorpus(t).Scenarios {
		t.Run(sc.Name, func(t *testing.T) { runBreakerScenario(t, sc) })
	}
}

func discardLogger() *charmlog.Logger {
	return charmlog.NewWithOptions(&bytes.Buffer{}, charmlog.Options{})
}

func runBreakerScenario(t *testing.T, sc bkScenario) {
	clk := newFakeClock()
	epoch := clk.Now()
	lg, buf := newCaptureLogger()
	pub := &recPub{}
	opts := []breaker.Option{breaker.WithClock(clk), breaker.Logger(lg)}
	c := sc.Config
	if c.Circuit != nil {
		opts = append(opts, breaker.WithCircuit(breaker.CircuitOpts{
			FailureThreshold: c.Circuit.FailureThreshold,
			SuccessThreshold: c.Circuit.SuccessThreshold,
			Delay:            time.Duration(c.Circuit.DelayMS) * time.Millisecond,
		}))
	}
	if c.MaxOps > 0 {
		opts = append(opts, breaker.MaxOps(c.MaxOps))
	}
	if c.MaxBytes > 0 {
		opts = append(opts, breaker.MaxBytes(c.MaxBytes))
	}
	if c.Rate != nil {
		opts = append(opts, breaker.MaxPerInterval(c.Rate.Max, time.Duration(c.Rate.PeriodMS)*time.Millisecond))
	}
	switch c.OnTrip {
	case "", "halt":
	case "warn":
		opts = append(opts, breaker.OnTrip(breaker.Warn))
	case "degrade":
		opts = append(opts, breaker.OnTrip(breaker.Degrade))
	default:
		t.Fatalf("unknown on_trip %q", c.OnTrip)
	}
	if c.Publisher {
		opts = append(opts, breaker.WithPublisher(pub))
	}
	if c.TopicPrefix != "" {
		opts = append(opts, breaker.WithTopicPrefix(c.TopicPrefix))
	}
	if c.Topics != nil {
		opts = append(opts, breaker.WithTopics(breaker.Topics{
			Tripped:    bus.Topic(c.Topics.Tripped),
			Opened:     bus.Topic(c.Topics.Opened),
			Closed:     bus.Topic(c.Topics.Closed),
			HalfOpened: bus.Topic(c.Topics.HalfOpened),
		}))
	}
	if c.Publisher != (sc.Events != nil) {
		t.Fatal("events is present exactly when publisher is true")
	}

	t.Cleanup(func() { breaker.Unregister(sc.Name) })
	b := breaker.New(sc.Name, opts...)

	for i, s := range sc.Steps {
		at := fmt.Sprintf("step %d (%s)", i, s.Op)
		switch s.Op {
		case "allow":
			got := "ok"
			if b.Allow() != nil {
				got = "broken"
			}
			if got != s.Want {
				t.Fatalf("%s: allow %s, want %s", at, got, s.Want)
			}
		case "record":
			b.Record(s.Success, s.N)
		case "advance":
			clk.Advance(time.Duration(s.MS) * time.Millisecond)
		case "state":
			if got := b.State().String(); got != s.Want {
				t.Fatalf("%s: state %s, want %s", at, got, s.Want)
			}
		case "trip":
			b.Trip(s.Reason)
		case "reset":
			b.Reset()
		case "stats":
			st := b.Stats()
			var atMS *int64
			if !st.LastTripAt.IsZero() {
				ms := st.LastTripAt.Sub(epoch).Milliseconds()
				atMS = &ms
			}
			if st.Trips != s.Trips || st.LastTripReason != s.LastReason ||
				!reflect.DeepEqual(atMS, s.LastTripAtMS) || !reflect.DeepEqual(st.Counters, s.Counters) {
				t.Fatalf("%s: stats {trips %d, reason %q, at %v, counters %v}, want {%d, %q, %v, %v}",
					at, st.Trips, st.LastTripReason, deref(atMS), st.Counters,
					s.Trips, s.LastReason, deref(s.LastTripAtMS), s.Counters)
			}
		default:
			t.Fatalf("%s: unknown op", at)
		}
	}

	checkBreakerLogs(t, buf.String(), sc.Logs)
	if sc.Events != nil {
		checkBreakerEvents(t, pub, *sc.Events)
	}
}

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func checkBreakerLogs(t *testing.T, out string, want []bkLog) {
	t.Helper()
	got := []bkLog{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		l := bkLog{Fields: map[string]any{}}
		l.Level, _ = rec["level"].(string)
		l.Msg, _ = rec["msg"].(string)
		delete(rec, "level")
		delete(rec, "msg")
		for k, v := range rec {
			l.Fields[k] = v
		}
		got = append(got, l)
	}
	if !reflect.DeepEqual(normalizeJSON(t, got), normalizeJSON(t, want)) {
		t.Fatalf("logs:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
}

func checkBreakerEvents(t *testing.T, pub *recPub, want []bkEvent) {
	t.Helper()
	if len(want) > 0 {
		waitForCount(t, pub, len(want))
	}
	// Publication is fire-and-forget: give stragglers a moment so an
	// extra event is caught rather than missed.
	time.Sleep(20 * time.Millisecond)
	var got []string
	for _, e := range pub.snapshot() {
		got = append(got, mustJSON(t, bkEvent{Topic: e.Topic, Payload: normalizeJSON(t, e.Payload)}))
	}
	var exp []string
	for _, e := range want {
		exp = append(exp, mustJSON(t, bkEvent{Topic: e.Topic, Payload: normalizeJSON(t, e.Payload)}))
	}
	sort.Strings(got)
	sort.Strings(exp)
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("events:\n got %v\nwant %v", got, exp)
	}
}

// normalizeJSON round-trips v through encoding/json so Go values and
// decoded vectors compare alike (numbers as float64, structs as maps).
func normalizeJSON(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal([]byte(mustJSON(t, v)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
