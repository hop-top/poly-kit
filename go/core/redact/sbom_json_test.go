package redact_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

// A GitHub SBOM line: "Organization: Go Authors" followed by the next
// JSON key must not be read as a generic key assignment.
const sbomLine = `{"sbom":{"SPDXID":"SPDXRef-DOCUMENT","packages":[{"name":"go:golang.org/x/sys","supplier":"Organization: Go Authors","externalRefs":[{"referenceCategory":"PACKAGE-MANAGER","referenceType":"purl","referenceLocator":"pkg:golang/golang.org/x/sys@v0.20.0"}]}]}}`

func TestDefault_SBOMJSONUnchanged(t *testing.T) {
	r, err := redact.NewEmbedded()
	require.NoError(t, err)
	out := r.Apply(sbomLine)
	assert.Equal(t, sbomLine, out)
	assert.True(t, json.Valid([]byte(out)), out)
	d := redact.Default()
	assert.Equal(t, sbomLine, d.Apply(sbomLine))
}

func TestDefault_GenericKeyStillRedacted(t *testing.T) {
	d := redact.Default()
	secret := "Zx9Qm2Lp7Wv4Rt8Kd3Nb6"
	for _, in := range []string{
		"api_key=" + secret,
		`{"token": "` + secret + `", "name": "x"}`,
		`{"supplier":"Organization: Go Authors","api_key":"` + secret + `"}`,
	} {
		out := d.Apply(in)
		assert.NotContains(t, out, secret, in)
	}
	out := d.Apply(`{"token": "` + secret + `", "name": "x"}`)
	assert.True(t, json.Valid([]byte(out)), out)
}
