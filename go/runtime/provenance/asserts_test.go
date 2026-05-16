package provenance_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/runtime/provenance"
)

// fakeTB records Errorf calls without aborting the surrounding test.
type fakeTB struct {
	errors []string
	fatal  []string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.fatal = append(f.fatal, fmt.Sprintf(format, args...))
}

func TestAssertProvenanceComplete_HappyPath(t *testing.T) {
	type out struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	v := out{Cohort: provenance.NewCached("beta", provenance.Provenance{
		Source: provenance.SourceCached, URL: "https://api", FetchedAt: time.Now().UTC(),
	})}
	ftb := &fakeTB{}
	provenance.AssertProvenanceComplete(ftb, context.Background(), v)
	assert.Empty(t, ftb.errors)
}

func TestAssertProvenanceComplete_MissingReports(t *testing.T) {
	type out struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	v := out{}
	ftb := &fakeTB{}
	provenance.AssertProvenanceComplete(ftb, context.Background(), v)
	assert.NotEmpty(t, ftb.errors)
	assert.True(t, strings.Contains(strings.Join(ftb.errors, "\n"), "/cohort"))
}

func TestAssertProvenanceMatchesCassette_HonestMatch(t *testing.T) {
	type out struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	v := out{Cohort: provenance.NewCached("beta", provenance.Provenance{
		Source:    provenance.SourceCached,
		URL:       "https://API.example.com:443/cohort?b=2&a=1",
		FetchedAt: time.Now().UTC(),
	})}
	entries := []provenance.CassetteEntry{
		{URL: "https://api.example.com/cohort?a=1&b=2", Method: "GET"},
	}
	ftb := &fakeTB{}
	provenance.AssertProvenanceMatchesCassette(ftb, context.Background(), v, entries)
	assert.Empty(t, ftb.errors)
}

func TestAssertProvenanceMatchesCassette_LyingFails(t *testing.T) {
	type out struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	v := out{Cohort: provenance.NewCached("beta", provenance.Provenance{
		Source:    provenance.SourceCached,
		URL:       "https://liar.example.com/cohort",
		FetchedAt: time.Now().UTC(),
	})}
	entries := []provenance.CassetteEntry{
		{URL: "https://api.example.com/cohort", Method: "GET"},
	}
	ftb := &fakeTB{}
	provenance.AssertProvenanceMatchesCassette(ftb, context.Background(), v, entries)
	assert.NotEmpty(t, ftb.errors)
	joined := strings.Join(ftb.errors, "\n")
	assert.Contains(t, joined, "/cohort")
	assert.Contains(t, joined, "liar.example.com")
}

func TestAssertProvenanceMatchesCassette_TrackerProvenance(t *testing.T) {
	// Wrapper unpopulated; tracker carries the URL. Cassette match
	// should still work.
	type out struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	v := out{} // wrapper zero
	tr := provenance.NewTracker()
	_ = tr.Cache("/cohort", provenance.Provenance{
		Source:    provenance.SourceCached,
		URL:       "https://api.example.com/cohort",
		FetchedAt: time.Now().UTC(),
	})
	ctx := provenance.WithTracker(context.Background(), tr)
	entries := []provenance.CassetteEntry{
		{URL: "https://api.example.com/cohort"},
	}
	ftb := &fakeTB{}
	provenance.AssertProvenanceMatchesCassette(ftb, ctx, v, entries)
	assert.Empty(t, ftb.errors)
}
