package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/console/cli"
)

// rgba returns the 16-bit RGBA channels of a color.Color rounded down to
// 8-bit form so tests can assert "this style maps to this theme color"
// without comparing interface identity (lipgloss.Color is a string,
// charmtone constants are typed values; identity comparisons are noisy).
func rgba(c interface{ RGBA() (r, g, b, a uint32) }) (r, g, b, a uint32) {
	return c.RGBA()
}

func TestRoot_TableStyle_PullsFromTheme(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "stylenant",
		Version:         "0.1.0",
		Short:           "table-style smoke",
		Accent:          "#FF8800", // distinctive orange so we can pin Primary,
		DisableValidate: true,
	})

	ts := r.TableStyle()

	// Primary must equal the theme accent.
	wantR, wantG, wantB, _ := rgba(r.Theme.Accent)
	gotR, gotG, gotB, _ := rgba(ts.Primary)
	assert.Equal(t, wantR, gotR, "primary R")
	assert.Equal(t, wantG, gotG, "primary G")
	assert.Equal(t, wantB, gotB, "primary B")

	// Secondary must equal the theme secondary.
	wantR, wantG, wantB, _ = rgba(r.Theme.Secondary)
	gotR, gotG, gotB, _ = rgba(ts.Secondary)
	assert.Equal(t, wantR, gotR, "secondary R")
	assert.Equal(t, wantG, gotG, "secondary G")
	assert.Equal(t, wantB, gotB, "secondary B")

	// Muted, Header, and BorderForeground all derive from Theme.Muted.
	wantR, wantG, wantB, _ = rgba(r.Theme.Muted)
	for name, got := range map[string]any{
		"muted":            ts.Muted,
		"header":           ts.Header,
		"borderForeground": ts.BorderForeground,
	} {
		c, ok := got.(interface {
			RGBA() (r, g, b, a uint32)
		})
		if !ok {
			t.Fatalf("%s does not implement color.Color: %T", name, got)
		}
		gotR, gotG, gotB, _ := rgba(c)
		assert.Equal(t, wantR, gotR, name+" R")
		assert.Equal(t, wantG, gotG, name+" G")
		assert.Equal(t, wantB, gotB, name+" B")
	}
}

func TestRoot_TableStyle_BorderDefaultsToNormal(t *testing.T) {
	r := cli.New(cli.Config{Name: "borders", Version: "0.1.0", Short: "border smoke", DisableValidate: true})
	ts := r.TableStyle()

	// NormalBorder has non-empty corner runes; Hidden/empty borders do not.
	// Asserting Top != "" is enough to prove "not zero Border".
	assert.NotEmpty(t, ts.Border.Top, "Border.Top must be set")
	assert.NotEmpty(t, ts.Border.Left, "Border.Left must be set")
}
