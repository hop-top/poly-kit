package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseScalar(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  any
	}{
		{"float", "0.9", 0.9},
		{"negative float", "-0.5", -0.5},
		{"scientific notation", "1e3", 1000.0},
		{"int", "123", 123},
		{"negative int", "-7", -7},
		{"zero", "0", 0},
		{"bool true", "true", true},
		{"bool false", "false", false},
		{"null", "null", nil},
		{"null capitalised", "Null", nil},
		{"null upper", "NULL", nil},
		{"null tilde", "~", nil},

		// YAML 1.1 lookalikes stay strings.
		{"yes", "yes", "yes"},
		{"no", "no", "no"},
		{"on", "on", "on"},
		{"off", "off", "off"},

		// Non-decimal spellings and dates stay strings: lossy otherwise.
		{"hex", "0x1F", "0x1F"},
		{"octal", "0o17", "0o17"},
		{"binary", "0b101", "0b101"},
		{"date", "2024-01-01", "2024-01-01"},
		{"underscored digits", "1_000", "1_000"},

		// Integer-looking tokens that do not fit in an int stay strings
		// rather than rounding through float64.
		{"max int64", "9223372036854775807", 9223372036854775807},
		{"max int64 plus one", "9223372036854775808", "9223372036854775808"},
		{"min int64", "-9223372036854775808", -9223372036854775808},
		{"min int64 minus one", "-9223372036854775809", "-9223372036854775809"},
		{
			"very long digit string",
			"12345678901234567890123456789012345",
			"12345678901234567890123456789012345",
		},
		// Out-of-range float spellings likewise stay strings.
		{"float overflow", "1e400", "1e400"},

		// Case-sensitive booleans: only exact lowercase converts.
		{"True stays string", "True", "True"},
		{"TRUE stays string", "TRUE", "TRUE"},

		{"plain string", "abc", "abc"},
		{"empty string", "", ""},
		{"whitespace padded number", " 1 ", " 1 "},
		{"path", "/usr/local/bin", "/usr/local/bin"},
		{"version", "1.2.3", "1.2.3"},
		{"lone dot", ".", "."},
		{"lone dash", "-", "-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseScalar(tt.input)
			assert.Equal(t, tt.want, got)
			if tt.want != nil {
				assert.IsType(t, tt.want, got)
			}
		})
	}
}

func TestParseScalar_InfAndNaN(t *testing.T) {
	for _, in := range []string{".inf", ".Inf", ".INF", "+.inf"} {
		got, ok := ParseScalar(in).(float64)
		require.True(t, ok, "%q should parse as float64", in)
		assert.True(t, math.IsInf(got, 1))
	}
	for _, in := range []string{"-.inf", "-.Inf", "-.INF"} {
		got, ok := ParseScalar(in).(float64)
		require.True(t, ok, "%q should parse as float64", in)
		assert.True(t, math.IsInf(got, -1))
	}
	for _, in := range []string{".nan", ".NaN", ".NAN"} {
		got, ok := ParseScalar(in).(float64)
		require.True(t, ok, "%q should parse as float64", in)
		assert.True(t, math.IsNaN(got))
	}
}

// TestParseScalar_ThroughSetValue covers the intended CLI wiring end to
// end: a raw string argument becomes a correctly typed YAML scalar.
func TestParseScalar_ThroughSetValue(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{"float", "0.9", "k: 0.9\n"},
		{"int", "123", "k: 123\n"},
		{"bool", "true", "k: true\n"},
		{"null", "null", "k: null\n"},
		{"string", "abc", "k: abc\n"},
		// Quoted, and above all still the exact digits typed: a float64
		// round-trip would have written 9.223372036854776e+18.
		{
			"int overflow keeps exact digits",
			"9223372036854775808",
			"k: \"9223372036854775808\"\n",
		},
		// Quoted on the way out, so it cannot resolve back to int 31.
		{"hex stays string", "0x1F", "k: \"0x1F\"\n"},
		{"date stays string", "2024-01-01", "k: \"2024-01-01\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			opts := optsForPath(path, ScopeProject)

			require.NoError(t, SetValue("k", ParseScalar(tt.arg), ScopeProject, opts))

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(data))
		})
	}
}
