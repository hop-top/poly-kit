package log_test

import (
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	kitlog "hop.top/kit/go/console/log"
)

func TestWithVerbose_Count0_Info(t *testing.T) {
	v := viper.New()
	l := kitlog.WithVerbose(v, 0)
	assert.Equal(t, charmlog.InfoLevel, l.GetLevel())
}

func TestWithVerbose_Count1_Debug(t *testing.T) {
	v := viper.New()
	l := kitlog.WithVerbose(v, 1)
	assert.Equal(t, charmlog.DebugLevel, l.GetLevel())
}

func TestWithVerbose_Count2_Trace(t *testing.T) {
	v := viper.New()
	l := kitlog.WithVerbose(v, 2)
	assert.Equal(t, kitlog.TraceLevel, l.GetLevel())
}

func TestWithVerbose_Count3_StillTrace(t *testing.T) {
	v := viper.New()
	l := kitlog.WithVerbose(v, 3)
	assert.Equal(t, kitlog.TraceLevel, l.GetLevel(),
		"counts above 2 should still resolve to TraceLevel")
}

func TestWithVerbose_QuietOverride(t *testing.T) {
	v := viper.New()
	v.Set("quiet", true)

	tests := []struct {
		name    string
		verbose int
	}{
		{"count0", 0},
		{"count1", 1},
		{"count2", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := kitlog.WithVerbose(v, tt.verbose)
			assert.Equal(t, charmlog.WarnLevel, l.GetLevel(),
				"quiet=true must override to WarnLevel")
		})
	}
}

func TestTraceLevel_BelowDebug(t *testing.T) {
	assert.Less(t, kitlog.TraceLevel, charmlog.DebugLevel,
		"TraceLevel must be below DebugLevel")
}
