package log_test

import (
	"bytes"
	"strings"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	kitlog "hop.top/kit/go/console/log"
)

func newViper(quiet, noColor bool) *viper.Viper {
	v := viper.New()
	v.Set("quiet", quiet)
	v.Set("no-color", noColor)
	return v
}

func TestNew_DefaultLevel(t *testing.T) {
	l := kitlog.New(newViper(false, false))
	assert.Equal(t, charmlog.InfoLevel, l.GetLevel())
}

func TestNew_QuietSetsWarn(t *testing.T) {
	l := kitlog.New(newViper(true, false))
	assert.Equal(t, charmlog.WarnLevel, l.GetLevel())
}

func TestWithLevel_OverridesDefault(t *testing.T) {
	l := kitlog.WithLevel(newViper(false, false), charmlog.DebugLevel)
	assert.Equal(t, charmlog.DebugLevel, l.GetLevel())
}

func TestWithLevel_QuietOverridesLowerLevel(t *testing.T) {
	l := kitlog.WithLevel(newViper(true, false), charmlog.DebugLevel)
	assert.Equal(t, charmlog.WarnLevel, l.GetLevel(),
		"quiet should override levels lower than WarnLevel")
}

func TestWithLevel_QuietDoesNotOverrideHigherLevel(t *testing.T) {
	l := kitlog.WithLevel(newViper(true, false), charmlog.ErrorLevel)
	assert.Equal(t, charmlog.ErrorLevel, l.GetLevel(),
		"quiet should not lower a level already >= WarnLevel")
}

func TestOutput_GoesToWriter(t *testing.T) {
	v := newViper(false, true)
	l := kitlog.WithLevel(v, charmlog.InfoLevel)

	var buf bytes.Buffer
	l.SetOutput(&buf)

	l.Info("hello from test")
	assert.Contains(t, buf.String(), "hello from test")
}

func TestNoColor_DisablesANSI(t *testing.T) {
	v := newViper(false, true)
	l := kitlog.WithLevel(v, charmlog.InfoLevel)

	var buf bytes.Buffer
	l.SetOutput(&buf)

	l.Info("plain text")
	out := buf.String()
	// ANSI escape sequences start with ESC (0x1b)
	assert.False(t, strings.Contains(out, "\x1b"),
		"output should not contain ANSI escapes when no-color is set")
}

func TestColor_EnabledByDefault(t *testing.T) {
	v := newViper(false, false)
	l := kitlog.WithLevel(v, charmlog.InfoLevel)

	var buf bytes.Buffer
	l.SetOutput(&buf)
	// Force TrueColor so test env doesn't downgrade.
	l.SetColorProfile(colorprofile.TrueColor)

	l.Info("colored text")
	out := buf.String()
	assert.True(t, strings.Contains(out, "\x1b"),
		"output should contain ANSI escapes when color is enabled")
}
