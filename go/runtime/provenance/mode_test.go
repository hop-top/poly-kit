package provenance_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/runtime/provenance"
)

func TestParseMode(t *testing.T) {
	cases := map[string]struct {
		want provenance.Mode
		ok   bool
	}{
		"":        {provenance.ModeOff, true},
		"off":     {provenance.ModeOff, true},
		"OFF":     {provenance.ModeOff, true},
		"warn":    {provenance.ModeWarn, true},
		"  Warn ": {provenance.ModeWarn, true},
		"strict":  {provenance.ModeStrict, true},
		"STRICT":  {provenance.ModeStrict, true},
		"bogus":   {provenance.ModeOff, false},
	}
	for in, exp := range cases {
		got, ok := provenance.ParseMode(in)
		assert.Equal(t, exp.want, got, in)
		assert.Equal(t, exp.ok, ok, in)
	}
}

func TestMode_String(t *testing.T) {
	assert.Equal(t, "off", provenance.ModeOff.String())
	assert.Equal(t, "warn", provenance.ModeWarn.String())
	assert.Equal(t, "strict", provenance.ModeStrict.String())
}

func TestSetMode_CurrentMode(t *testing.T) {
	provenance.SetMode(provenance.ModeStrict)
	defer provenance.SetMode(provenance.ModeOff)
	assert.Equal(t, provenance.ModeStrict, provenance.CurrentMode())
}

func TestWithMode_OverridesGlobal(t *testing.T) {
	provenance.SetMode(provenance.ModeOff)
	ctx := provenance.WithMode(context.Background(), provenance.ModeWarn)
	assert.Equal(t, provenance.ModeWarn, provenance.CurrentModeFromContext(ctx))
}

func TestCurrentModeFromContext_NoOverride(t *testing.T) {
	provenance.SetMode(provenance.ModeStrict)
	defer provenance.SetMode(provenance.ModeOff)
	assert.Equal(t, provenance.ModeStrict, provenance.CurrentModeFromContext(context.Background()))
}
