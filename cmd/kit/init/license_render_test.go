package kitinit_test

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
)

// render parses the LICENSE-* template at path against the given
// Copyrights slice and returns the rendered output.
func renderLicense(t *testing.T, path string, cps []kitinit.Copyright) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	tpl, err := template.New("license").
		Option("missingkey=error").
		Parse(string(body))
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tpl.Execute(&buf, map[string]any{
		"Copyrights": cps,
		"Year":       2026,
		"Author":     "ignored",
	}))
	return buf.String()
}

func TestLicenseMIT_DefaultFourHolders(t *testing.T) {
	out := renderLicense(t,
		"../../../templates/shared/LICENSE-MIT.tmpl",
		kitinit.DefaultCopyrights(2026),
	)
	assert.Contains(t, out, "MIT License")
	assert.Contains(t, out, "Permission is hereby granted, free of charge")
	assert.Contains(t, out, "# >>> kit-managed: copyright >>>")
	assert.Contains(t, out, "# <<< kit-managed: copyright <<<")
	for _, want := range []string{
		"Copyright (c) 2026 Idea Crafters LLC <https://ideacrafters.com>",
		"Copyright (c) 2026 AI Experts <https://lesexperts.ai>",
		"Copyright (c) 2026 @jadb <https://github.com/jadb>",
		"Copyright (c) 2026 @monaam <https://github.com/monaam>",
	} {
		assert.Contains(t, out, want)
	}
}

func TestLicenseMIT_SingleHolderNoURL(t *testing.T) {
	out := renderLicense(t,
		"../../../templates/shared/LICENSE-MIT.tmpl",
		[]kitinit.Copyright{{Years: "2026", Holder: "Jane Doe"}},
	)
	assert.Contains(t, out, "Copyright (c) 2026 Jane Doe\n")
	// No trailing space, no empty angle brackets.
	assert.NotContains(t, out, "Jane Doe ")
	assert.NotContains(t, out, "<>")
}

func TestLicenseApache_MultiHolder(t *testing.T) {
	out := renderLicense(t,
		"../../../templates/shared/LICENSE-Apache-2.0.tmpl",
		[]kitinit.Copyright{
			{Years: "2020-2024", Holder: "Acme Inc", URL: "https://acme.example"},
			{Years: "2026", Holder: "Jane Doe"},
		},
	)
	assert.Contains(t, out, "Apache License")
	assert.Contains(t, out, "Version 2.0, January 2004")
	assert.Contains(t, out, "   Copyright 2020-2024 Acme Inc <https://acme.example>")
	assert.Contains(t, out, "   Copyright 2026 Jane Doe\n")
	// Apache template must NOT prefix copyright lines with "(c)".
	assert.NotContains(t, out, "Copyright (c)")
}

func TestLicenseBuiltinMirrorsMatchSource(t *testing.T) {
	for _, name := range []string{
		"LICENSE-MIT.tmpl",
		"LICENSE-Apache-2.0.tmpl",
	} {
		src, err := os.ReadFile("../../../templates/shared/" + name)
		require.NoError(t, err, name)
		mirror, err := os.ReadFile(
			"../../../internal/template/builtins/shared/" + name)
		require.NoError(t, err, name)
		assert.Equal(t, string(src), string(mirror),
			"embedded mirror %s drifted from templates/shared/", name)
	}
}

func TestLicenseRender_NoUnclosedTemplateTokens(t *testing.T) {
	out := renderLicense(t,
		"../../../templates/shared/LICENSE-MIT.tmpl",
		kitinit.DefaultCopyrights(2026),
	)
	// Both opening and closing template delimiters must be gone after
	// render. A bare "{{" or "}}" would indicate an unbalanced trim
	// directive or syntax mistake.
	assert.False(t, strings.Contains(out, "{{"),
		"rendered LICENSE still contains '{{':\n%s", out)
	assert.False(t, strings.Contains(out, "}}"),
		"rendered LICENSE still contains '}}':\n%s", out)
}
