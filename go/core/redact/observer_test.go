package redact_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

func TestOnMatch_FiresPerMatch(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)

	var got []redact.Match
	r.OnMatch(func(m redact.Match) { got = append(got, m) })

	_ = r.Apply("a 1 b 22 c 333")
	require.Len(t, got, 3)
	assert.Equal(t, "1", got[0].Original)
	assert.Equal(t, "22", got[1].Original)
	assert.Equal(t, "333", got[2].Original)
	for _, m := range got {
		assert.Equal(t, "digits", m.RuleID)
		assert.NotEmpty(t, m.Replacement)
	}
}

func TestOnMatch_MultipleObserversChain(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)

	var calls []string
	r.OnMatch(func(_ redact.Match) { calls = append(calls, "a") })
	r.OnMatch(func(_ redact.Match) { calls = append(calls, "b") })

	_ = r.Apply("1 2")
	assert.Equal(t, []string{"a", "b", "a", "b"}, calls,
		"observers should fire in registration order, per match")
}

func TestOnMatch_NilClears(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)

	var hit bool
	r.OnMatch(func(_ redact.Match) { hit = true })
	r.OnMatch(nil) // clear
	_ = r.Apply("1 2 3")
	assert.False(t, hit, "nil observer registration should clear the chain")
}

func TestStats_100RedactionsAcross5Rules(t *testing.T) {
	r := redact.New()
	// 5 distinct rules, deliberately non-overlapping so every input
	// substring is owned by exactly one rule.
	pats := map[string]string{
		"r1": `R1+`,
		"r2": `R2+`,
		"r3": `R3+`,
		"r4": `R4+`,
		"r5": `R5+`,
	}
	for id, p := range pats {
		_, err := r.AddRule(id, p, "")
		require.NoError(t, err)
	}

	// Build input with 20 hits per rule = 100 total.
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("R1 R2 R3 R4 R5 ")
	}

	var seen []redact.Match
	r.OnMatch(func(m redact.Match) { seen = append(seen, m) })
	_ = r.Apply(sb.String())

	s := r.Stats()
	assert.Equal(t, uint64(100), s.Matches, "expected 100 matches total")
	for id := range pats {
		assert.Equal(t, uint64(20), s.ByRule[id], "rule %s should fire 20×", id)
	}
	assert.Len(t, seen, 100, "observer should fire once per match")
	assert.False(t, s.LastMatchAt.IsZero(), "LastMatchAt should be set")
	assert.WithinDuration(t, time.Now(), s.LastMatchAt, 5*time.Second)
}

func TestStats_ByRuleDeepCopy(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	_ = r.Apply("1 2 3")
	s1 := r.Stats()
	s1.ByRule["digits"] = 999 // mutate the snapshot
	s2 := r.Stats()
	assert.Equal(t, uint64(3), s2.ByRule["digits"],
		"snapshot mutation must not bleed back into the redactor")
}

func TestOnMatch_ConcurrentSafe(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)

	var mu sync.Mutex
	count := 0
	r.OnMatch(func(_ redact.Match) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Apply("a 1 b 2 c 3")
		}()
	}
	wg.Wait()
	assert.Equal(t, 150, count)
	assert.Equal(t, uint64(150), r.Stats().Matches)
}
