package cmdsurface

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/memory"
)

// quotaBridge serves newBridgeTree on REST under q, counting in ledger,
// with a runner that prints out and fails when fail is set.
func quotaBridge(q Quota, ledger *UsageLedger, runs *int, fail *bool) *Bridge {
	b := New(newBridgeTree(),
		WithRunner(&fakeRunner{run: func(context.Context, Invocation) (Result, error) {
			*runs++
			if fail != nil && *fail {
				return Result{}, errors.New("runner broke")
			}
			return Result{Stdout: "added\n"}, nil
		}}),
		WithQuota(q, ledger))
	b.Expose("*", SurfaceREST)
	return b
}

var alice = Meta{Surface: SurfaceREST, Caller: "alice", Tenant: "acme", Established: EstablishedVerified}

func add(b *Bridge, meta Meta) error {
	_, err := b.Invoke(context.Background(), Invocation{Path: []string{"widget", "add"}, Meta: meta})
	return err
}

func TestQuota_OpsPerWindowRefusesWithTheReset(t *testing.T) {
	ledger := NewUsageLedger(memory.New())
	runs := 0
	b := quotaBridge(Quota{Scope: "api", Window: time.Hour, Ops: 2}, ledger, &runs, nil)

	require.NoError(t, add(b, alice))
	require.NoError(t, add(b, alice))
	err := add(b, alice)
	require.ErrorIs(t, err, ErrQuotaExceeded)
	require.ErrorIs(t, err, ErrRateLimited, "the class degrades to a rate limit")
	assert.Equal(t, 2, runs, "a refused call runs nothing")

	var qe *QuotaExceededError
	require.ErrorAs(t, err, &qe)
	assert.Equal(t, "ops", qe.Limit)
	assert.EqualValues(t, 2, qe.Max)
	wait, ok := RetryAfter(err)
	require.True(t, ok)
	assert.InDelta(t, time.Until(time.Now().Truncate(time.Hour).Add(time.Hour)).Seconds(), wait.Seconds(), 2,
		"Retry-After is the window's reset")
	assert.Contains(t, err.Error(), "2 of 2 ops per 1h0m0s used; resets at")

	assert.NoError(t, add(b, Meta{Surface: SurfaceREST, Caller: "bob", Established: EstablishedVerified}),
		"principals count separately")
	assert.NoError(t, add(b, Meta{Surface: SurfaceLib}), "local surfaces are never counted")

	// A restart counts on from the same ledger.
	b = quotaBridge(Quota{Scope: "api", Window: time.Hour, Ops: 2}, ledger, &runs, nil)
	require.ErrorIs(t, add(b, alice), ErrQuotaExceeded)
}

func TestQuota_BytesCountTheOutput(t *testing.T) {
	ledger := NewUsageLedger(memory.New())
	runs := 0
	b := quotaBridge(Quota{Scope: "api", Window: time.Hour, Bytes: 10}, ledger, &runs, nil)
	require.NoError(t, add(b, alice)) // 6 bytes
	require.NoError(t, add(b, alice)) // 12: the call that crosses completes
	err := add(b, alice)
	var qe *QuotaExceededError
	require.ErrorAs(t, err, &qe)
	assert.Equal(t, "bytes", qe.Limit)
	assert.EqualValues(t, 12, qe.Used)
}

func TestQuota_OnlySuccessfulRunsCount(t *testing.T) {
	ledger := NewUsageLedger(memory.New())
	runs, fail := 0, true
	q := Quota{Scope: "api", Window: time.Hour, Ops: 1}
	b := quotaBridge(q, ledger, &runs, &fail)
	require.Error(t, add(b, alice))
	fail = false
	require.NoError(t, add(b, alice), "the failed run spent nothing")
	u, err := ledger.Usage(context.Background(), QuotaKey(q, alice), time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 1, u.Ops)
}

func TestQuotaKey(t *testing.T) {
	q := Quota{Scope: "api"}
	assert.Equal(t, "quota/api/principal/alice/acme", QuotaKey(q, alice))
	q.Per = QuotaPerTenant
	assert.Equal(t, "quota/api/tenant/acme", QuotaKey(q, alice))
	assert.Equal(t, "quota/api/principal/bob/",
		QuotaKey(q, Meta{Caller: "bob", Established: EstablishedVerified}), "no tenant: per principal")
	claimed := Meta{Caller: "mallory", Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.9:5555"}}
	assert.Equal(t, "quota/api/address/10.0.0.9", QuotaKey(q, claimed), "a claim selects no quota")
	assert.Equal(t, "quota/api/surface/bus", QuotaKey(q, Meta{Surface: SurfaceBus}))
}

func TestQuota_RefusalOnMCPAndOff(t *testing.T) {
	err := &QuotaExceededError{Path: "x", Surface: SurfaceMCP, Limit: "ops", Max: 1, Used: 1,
		Window: time.Hour, Reset: time.Now().Add(time.Minute), RetryAfter: time.Minute}
	text, refusal, ok := MCPRefusal(err)
	require.True(t, ok)
	assert.Equal(t, CodeQuotaExceeded, refusal["code"])
	assert.EqualValues(t, 60000, refusal["retry_after_ms"])
	assert.Contains(t, text, "quota_exceeded: ")

	// No limits, no ledger, or a sub-second window installs nothing.
	for _, q := range []Quota{{Window: time.Hour}, {Ops: 1}} {
		b := New(newBridgeTree(), WithQuota(q, NewUsageLedger(memory.New())))
		assert.Nil(t, b.cfg.quota)
	}
	assert.Nil(t, New(newBridgeTree(), WithQuota(Quota{Window: time.Hour, Ops: 1}, nil)).cfg.quota)
}

func TestQuota_ACacheHitSpendsNothingAndIsNotRefused(t *testing.T) {
	ledger := NewUsageLedger(memory.New())
	run := &tallyRunner{}
	b := New(newCacheTree(), WithRunner(run), WithResultCache(memory.New()),
		WithQuota(Quota{Scope: "api", Window: time.Hour, Ops: 1}, ledger))
	b.Expose("*", SurfaceREST)
	call := Invocation{Path: []string{"widget", "list"}, Meta: alice}

	_, err := b.Invoke(context.Background(), call)
	require.NoError(t, err)
	_, err = b.Invoke(context.Background(), call)
	require.NoError(t, err, "a replay from the cache does no work, so the spent quota does not refuse it")
	assert.EqualValues(t, 1, run.calls.Load())

	_, err = b.Invoke(context.Background(), Invocation{Path: []string{"widget", "show"}, Meta: alice})
	require.ErrorIs(t, err, ErrQuotaExceeded, "a call that would run is refused")
}
