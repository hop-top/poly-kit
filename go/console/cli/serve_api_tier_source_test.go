package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// TestProjectionReportsSideEffectSource drives the real api service
// and pins the reflector-to-projection mapping of every way a tier is
// arrived at. declared, silent and typo all project onto `write`; only
// the source separates the adopter's word from kit's stand-in.
func TestProjectionReportsSideEffectSource(t *testing.T) {
	r := New(Config{Name: "fix", Version: "1.0.0", DisableValidate: true},
		WithAPI(APIConfig{Addr: ":0"}))
	add := func(use, sideEffect string) {
		c := &cobra.Command{Use: use, Short: use, Run: func(*cobra.Command, []string) {}}
		if sideEffect != "" {
			c.Annotations = map[string]string{"kit/side-effect": sideEffect}
		}
		r.Cmd.AddCommand(c)
	}
	add("declared", "write-local")
	add("legacy", "write") // legacy vocabulary is still a declaration
	add("reader", "read")
	add("silent", "")
	add("delete", "") // destructive-name heuristic
	add("typo", "destrutive")

	rec := httptest.NewRecorder()
	projectionHandler(t, r).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, api.CommandProjectionPrefix, nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	got := map[string]api.DiscoveryEntry{}
	for _, e := range doc.Commands {
		got[e.Name] = e
	}

	for name, want := range map[string]struct {
		class  api.SideEffectClass
		source api.SideEffectSource
	}{
		"declared": {api.SideEffectWrite, api.SideEffectSourceDeclared},
		"legacy":   {api.SideEffectWrite, api.SideEffectSourceDeclared},
		"reader":   {api.SideEffectRead, api.SideEffectSourceDeclared},
		"silent":   {api.SideEffectWrite, api.SideEffectSourceUnannotated},
		"delete":   {api.SideEffectDestructive, api.SideEffectSourceInferred},
		"typo":     {api.SideEffectWrite, api.SideEffectSourceMalformed},
	} {
		e, ok := got[name]
		require.True(t, ok, "%s missing from discovery", name)
		assert.Equal(t, want.class, e.SideEffect, "%s class", name)
		assert.Equal(t, want.source, e.SideEffectSource, "%s source", name)
	}

	// Unannotated stays invocable, as a POST.
	assert.True(t, got["silent"].Invocable)
	assert.Equal(t, http.MethodPost, got["silent"].Method)
	assert.Equal(t, "malformed-schema", got["typo"].Reason)
}
