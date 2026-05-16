//go:build integration

package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/console/markdown"
	"hop.top/kit/go/console/output"
)

// TestIntegration_QuietSuppressesLog wires cli.Root + log.New and verifies
// that --quiet suppresses info-level messages while warn-level still appears.
func TestIntegration_QuietSuppressesLog(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "test", DisableValidate: true})
	require.NoError(t, r.Cmd.PersistentFlags().Set("quiet", "true"))

	l := kitlog.New(r.Viper)

	var buf bytes.Buffer
	l.SetOutput(&buf)
	l.SetColorProfile(colorprofile.NoTTY)

	l.Info("should be hidden")
	assert.Empty(t, buf.String(), "info message must be suppressed when --quiet is set")

	l.Warn("visible warning")
	assert.Contains(t, buf.String(), "visible warning",
		"warn message must still appear when --quiet is set")
}

// TestIntegration_NoColorDisablesANSI wires cli.Root + log + markdown and
// verifies that --no-color strips ANSI escapes from both log output and
// markdown rendering.
func TestIntegration_NoColorDisablesANSI(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "test", DisableValidate: true})
	require.NoError(t, r.Cmd.PersistentFlags().Set("no-color", "true"))

	// Log output should be free of ANSI escapes.
	l := kitlog.New(r.Viper)
	var buf bytes.Buffer
	l.SetOutput(&buf)
	l.Info("plain log line")
	assert.False(t, strings.Contains(buf.String(), "\x1b"),
		"log output must not contain ANSI escapes when --no-color is set")

	// Markdown output should also be free of ANSI escapes.
	noColor := r.Viper.GetBool("no-color")
	md, err := markdown.Render("# Title\n\n**bold** text\n", noColor)
	require.NoError(t, err)
	assert.False(t, strings.Contains(md, "\x1b"),
		"markdown output must not contain ANSI escapes when --no-color is set")
}

// TestIntegration_FormatPropagates wires cli.Root + output.Render and verifies
// that --format=json is read from viper and produces JSON output.
func TestIntegration_FormatPropagates(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "test", DisableValidate: true})
	require.NoError(t, r.Cmd.PersistentFlags().Set("format", "json"))

	format := r.Viper.GetString("format")
	assert.Equal(t, "json", format, "--format value must propagate to viper")

	type row struct {
		Name  string `json:"name"  table:"Name"`
		Count int    `json:"count" table:"Count"`
	}

	var buf bytes.Buffer
	err := output.Render(&buf, format, []row{{Name: "alpha", Count: 1}})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, `"name"`, "JSON output must contain json field keys")
	assert.Contains(t, out, `"alpha"`, "JSON output must contain value")
	assert.Contains(t, out, `"count"`, "JSON output must contain count key")
}

// TestIntegration_DifferentAccents creates two Roots with distinct Accent
// values and verifies that the resulting Themes differ.
func TestIntegration_DifferentAccents(t *testing.T) {
	a := cli.New(cli.Config{Name: "a", Version: "0.0.1", Short: "a", Accent: "#FF0000", DisableValidate: true})
	b := cli.New(cli.Config{Name: "b", Version: "0.0.1", Short: "b", Accent: "#0000FF", DisableValidate: true})

	ar, ag, ab, _ := a.Theme.Accent.RGBA()
	br, bg, bb, _ := b.Theme.Accent.RGBA()

	// At least one channel must differ.
	differ := ar != br || ag != bg || ab != bb
	assert.True(t, differ,
		"two different Accent hex values must produce different theme accent colors")

	// Also verify the Title style foreground differs (it's built from accent).
	aTitle := a.Theme.Title.GetForeground()
	bTitle := b.Theme.Title.GetForeground()
	assert.NotEqual(t, aTitle, bTitle,
		"Title style foreground must differ between different accents")
}
