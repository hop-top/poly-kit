package llm_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"hop.top/aim"
	"hop.top/kit/go/ai/llm"
)

// fixtureSource is an in-memory [aim.Source] driven by a flat slice of models.
// It groups by Provider into the provider map shape the registry expects and
// backfills Model.Provider so consumers see the populated field.
type fixtureSource struct {
	models []aim.Model
	err    error
}

func (f fixtureSource) Fetch(_ context.Context) (map[string]*aim.Provider, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]*aim.Provider{}
	for i := range f.models {
		m := f.models[i]
		p, ok := out[m.Provider]
		if !ok {
			p = &aim.Provider{ID: m.Provider, Name: m.Provider, Models: map[string]*aim.Model{}}
			out[m.Provider] = p
		}
		// Backfill matches what the HTTP source does — keeps Provider populated
		// after a Models() query.
		mc := m
		mc.Provider = p.ID
		p.Models[mc.ID] = &mc
	}
	return out, nil
}

func newRegistry(t *testing.T, models ...aim.Model) *aim.Registry {
	t.Helper()
	// WithCacheDir(t.TempDir()) isolates this test from any payload the user's
	// XDG aim cache may hold from real models.dev fetches.
	return aim.NewRegistry(
		aim.WithSource(fixtureSource{models: models}),
		aim.WithCacheOpts(aim.WithCacheDir(t.TempDir())),
	)
}

// boolPtr keeps the tristate-filter call sites readable.
func boolPtr(b bool) *bool { return &b }

// model is a fixture builder so tests stay compact.
func model(provider, id string, opts ...func(*aim.Model)) aim.Model {
	m := aim.Model{
		ID:       id,
		Name:     id,
		Provider: provider,
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

func withTools(b bool) func(*aim.Model)  { return func(m *aim.Model) { m.ToolCall = b } }
func withContext(n int) func(*aim.Model) { return func(m *aim.Model) { m.Limit.Context = n } }
func withOutput(n int) func(*aim.Model)  { return func(m *aim.Model) { m.Limit.Output = n } }
func withCost(in, out float64) func(*aim.Model) {
	return func(m *aim.Model) { m.Cost = &aim.Cost{Input: in, Output: out} }
}

func TestPickProvider_CapabilityFilter(t *testing.T) {
	reg := newRegistry(
		t,
		model("p1", "no-tools-a", withTools(false)),
		model("p1", "yes-tools", withTools(true)),
		model("p2", "no-tools-b", withTools(false)),
	)
	prof := llm.RequestProfile{Filter: aim.Filter{ToolCall: boolPtr(true)}}

	got, err := llm.PickProvider(context.Background(), reg, prof, llm.BudgetBalanced)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "yes-tools" {
		t.Fatalf("picked %q, want %q", got.ID, "yes-tools")
	}
}

func TestPickProvider_ContextWindowFilter(t *testing.T) {
	reg := newRegistry(
		t,
		model("p1", "small", withContext(8192)),
		model("p1", "large", withContext(200000)),
	)
	prof := llm.RequestProfile{MaxInputTokens: 100000}

	got, err := llm.PickProvider(context.Background(), reg, prof, llm.BudgetBalanced)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "large" {
		t.Fatalf("picked %q, want %q", got.ID, "large")
	}
}

func TestPickProvider_OutputTokensFilter(t *testing.T) {
	reg := newRegistry(
		t,
		model("p1", "tiny-output", withOutput(4096)),
		model("p1", "big-output", withOutput(32768)),
	)
	prof := llm.RequestProfile{MaxOutputTokens: 16384}

	got, err := llm.PickProvider(context.Background(), reg, prof, llm.BudgetBalanced)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "big-output" {
		t.Fatalf("picked %q, want %q", got.ID, "big-output")
	}
}

func TestPickProvider_BudgetCheap(t *testing.T) {
	reg := newRegistry(
		t,
		model("p1", "expensive", withCost(10, 10)),
		model("p1", "cheap", withCost(1, 1)),
		model("p1", "mid", withCost(5, 5)),
	)
	got, err := llm.PickProvider(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "cheap" {
		t.Fatalf("Cheap picked %q, want %q", got.ID, "cheap")
	}
}

func TestPickProvider_BudgetPremium(t *testing.T) {
	reg := newRegistry(
		t,
		model("p1", "small", withContext(4096), withCost(1, 1)),
		model("p1", "huge", withContext(200000), withCost(1, 1)),
		model("p1", "medium", withContext(128000), withCost(1, 1)),
	)
	got, err := llm.PickProvider(context.Background(), reg, llm.RequestProfile{}, llm.BudgetPremium)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "huge" {
		t.Fatalf("Premium picked %q, want %q (Limit.Context=%d)", got.ID, "huge", got.Limit.Context)
	}
}

func TestPickProvider_BudgetBalanced(t *testing.T) {
	// weighted = Input*0.75 + Output*0.25; with Output==Input the weighted price
	// equals Input. Pool prices [1, 5, 10] → median is 5.
	reg := newRegistry(
		t,
		model("p1", "low", withCost(1, 1)),
		model("p1", "median", withCost(5, 5)),
		model("p1", "high", withCost(10, 10)),
	)
	got, err := llm.PickProvider(context.Background(), reg, llm.RequestProfile{}, llm.BudgetBalanced)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "median" {
		t.Fatalf("Balanced picked %q, want %q", got.ID, "median")
	}
}

func TestPickProvider_DeterministicTiebreak(t *testing.T) {
	// Cheap and Premium pick index 0 after sort; with priced-identical models
	// the alphabetically smaller (Provider, ID) wins on every call.
	for _, budget := range []llm.BudgetTier{llm.BudgetCheap, llm.BudgetPremium} {
		t.Run(budget.String(), func(t *testing.T) {
			reg := newRegistry(
				t,
				model("zeta", "model-z", withCost(2, 2), withContext(100000)),
				model("alpha", "model-a", withCost(2, 2), withContext(100000)),
			)
			var seen *aim.Model
			for i := 0; i < 10; i++ {
				got, err := llm.PickProvider(context.Background(), reg, llm.RequestProfile{}, budget)
				if err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
				if seen == nil {
					seen = got
				}
				if got.Provider != seen.Provider || got.ID != seen.ID {
					t.Fatalf("call %d: got (%s,%s), want stable (%s,%s)", i, got.Provider, got.ID, seen.Provider, seen.ID)
				}
			}
			if seen.Provider != "alpha" || seen.ID != "model-a" {
				t.Fatalf("winner = (%s,%s), want (alpha, model-a)", seen.Provider, seen.ID)
			}
		})
	}

	// Balanced picks survivors[len/2]; with three priced-identical models the
	// stable tiebreak places them in (Provider, ID) order so the middle slot
	// is well-defined across runs.
	t.Run("balanced", func(t *testing.T) {
		reg := newRegistry(
			t,
			model("zeta", "model-z", withCost(2, 2)),
			model("alpha", "model-a", withCost(2, 2)),
			model("mu", "model-m", withCost(2, 2)),
		)
		var seen *aim.Model
		for i := 0; i < 10; i++ {
			got, err := llm.PickProvider(context.Background(), reg, llm.RequestProfile{}, llm.BudgetBalanced)
			if err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
			if seen == nil {
				seen = got
			}
			if got.Provider != seen.Provider || got.ID != seen.ID {
				t.Fatalf("call %d: got (%s,%s), want stable (%s,%s)", i, got.Provider, got.ID, seen.Provider, seen.ID)
			}
		}
		// Sorted by (Provider, ID): alpha/model-a, mu/model-m, zeta/model-z.
		// Median index = 1 → mu/model-m.
		if seen.Provider != "mu" || seen.ID != "model-m" {
			t.Fatalf("balanced winner = (%s,%s), want (mu, model-m)", seen.Provider, seen.ID)
		}
	})
}

func TestPickProvider_NilCost_CheapPrefers(t *testing.T) {
	reg := newRegistry(
		t,
		model("p1", "priced", withCost(5, 5)),
		model("p1", "local"), // Cost == nil
	)
	got, err := llm.PickProvider(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "local" {
		t.Fatalf("Cheap picked %q, want %q (nil-cost should win)", got.ID, "local")
	}
}

func TestPickProvider_NilCost_PremiumLosesTiebreak(t *testing.T) {
	reg := newRegistry(
		t,
		model("p1", "priced", withContext(100000), withCost(5, 5)),
		model("p1", "local", withContext(100000)), // Cost == nil → tiebreak value 0
	)
	got, err := llm.PickProvider(context.Background(), reg, llm.RequestProfile{}, llm.BudgetPremium)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "priced" {
		t.Fatalf("Premium picked %q, want %q (priced should beat nil-cost on tiebreak)", got.ID, "priced")
	}
}

func TestPickProvider_UnknownLimitsPass(t *testing.T) {
	// Limit.Context == 0 means "unknown" — picker must not eliminate it under
	// a positive MaxInputTokens cap.
	reg := newRegistry(
		t,
		model("p1", "unknown-window"),
	)
	prof := llm.RequestProfile{MaxInputTokens: 100000, MaxOutputTokens: 32000}
	got, err := llm.PickProvider(context.Background(), reg, prof, llm.BudgetBalanced)
	if err != nil {
		t.Fatalf("PickProvider: %v", err)
	}
	if got.ID != "unknown-window" {
		t.Fatalf("picked %q, want %q (unknown limits must pass)", got.ID, "unknown-window")
	}
}

func TestPickProvider_NoMatch(t *testing.T) {
	// Filter matches nothing → CandidateCount == 0, Eliminated empty.
	reg := newRegistry(
		t,
		model("p1", "no-tools", withTools(false)),
	)
	prof := llm.RequestProfile{Filter: aim.Filter{ToolCall: boolPtr(true)}}

	_, err := llm.PickProvider(context.Background(), reg, prof, llm.BudgetCheap)
	if err == nil {
		t.Fatal("PickProvider: want error, got nil")
	}
	if !errors.Is(err, llm.ErrNoProviderMatches) {
		t.Fatalf("errors.Is(err, ErrNoProviderMatches) = false; err = %v", err)
	}
	var nme *llm.NoMatchError
	if !errors.As(err, &nme) {
		t.Fatalf("errors.As(err, *NoMatchError) = false; err = %T %v", err, err)
	}
	if nme.CandidateCount != 0 {
		t.Fatalf("CandidateCount = %d, want 0", nme.CandidateCount)
	}
	if len(nme.Eliminated) != 0 {
		t.Fatalf("Eliminated = %v, want empty", nme.Eliminated)
	}
}

func TestPickProvider_NoMatch_AllEliminatedByContextWindow(t *testing.T) {
	reg := newRegistry(
		t,
		model("p1", "a", withContext(4096)),
		model("p1", "b", withContext(8192)),
		model("p1", "c", withContext(16384)),
	)
	prof := llm.RequestProfile{MaxInputTokens: 100000}

	_, err := llm.PickProvider(context.Background(), reg, prof, llm.BudgetCheap)
	if err == nil {
		t.Fatal("PickProvider: want error, got nil")
	}
	if !errors.Is(err, llm.ErrNoProviderMatches) {
		t.Fatalf("errors.Is = false; err = %v", err)
	}
	var nme *llm.NoMatchError
	if !errors.As(err, &nme) {
		t.Fatalf("errors.As failed; err = %T %v", err, err)
	}
	if nme.CandidateCount != 3 {
		t.Fatalf("CandidateCount = %d, want 3", nme.CandidateCount)
	}
	if len(nme.Eliminated) != 3 {
		t.Fatalf("len(Eliminated) = %d, want 3", len(nme.Eliminated))
	}
	for _, e := range nme.Eliminated {
		if e.Stage != "context_window" {
			t.Fatalf("Eliminated stage = %q, want %q", e.Stage, "context_window")
		}
		if e.Model == nil {
			t.Fatalf("Eliminated entry missing Model: %+v", e)
		}
	}
}

func TestPickProvider_RegistryError(t *testing.T) {
	wantErr := fmt.Errorf("upstream boom")
	reg := aim.NewRegistry(
		aim.WithSource(fixtureSource{err: wantErr}),
		aim.WithCacheOpts(aim.WithCacheDir(t.TempDir())),
	)

	_, err := llm.PickProvider(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap)
	if err == nil {
		t.Fatal("PickProvider: want error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("errors.Is(err, wantErr) = false; err = %v", err)
	}
	if msg := err.Error(); !contains(msg, "query registry") {
		t.Fatalf("error message %q does not contain %q", msg, "query registry")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
