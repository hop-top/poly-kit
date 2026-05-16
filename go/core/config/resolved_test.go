package config_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/core/config"
)

func TestOrigin_String(t *testing.T) {
	assert.Equal(t, "default", config.OriginDefault.String())
	assert.Equal(t, "global", config.OriginGlobal.String())
	assert.Equal(t, "project", config.OriginProject.String())
	assert.Equal(t, "env", config.OriginEnv.String())
	assert.Equal(t, "flag", config.OriginFlag.String())
}

func TestOrigin_Ordering(t *testing.T) {
	// Higher-priority origins must compare greater so callers can reason
	// about ladder position numerically.
	assert.True(t, config.OriginFlag > config.OriginEnv)
	assert.True(t, config.OriginEnv > config.OriginProject)
	assert.True(t, config.OriginProject > config.OriginGlobal)
	assert.True(t, config.OriginGlobal > config.OriginDefault)
}

func TestFormatResolved_String(t *testing.T) {
	r := config.Resolved[string]{Key: "out", Value: "out/", Origin: config.OriginProject}
	assert.Equal(t, "out/ (project)", config.FormatResolved(r))
}

func TestFormatResolved_Generic(t *testing.T) {
	r := config.Resolved[int]{Key: "n", Value: 42, Origin: config.OriginFlag}
	assert.Equal(t, "42 (flag)", config.FormatResolved(r))
}

func TestFormatResolvedTable(t *testing.T) {
	entries := []config.Resolved[string]{
		{Key: "a", Value: "1", Origin: config.OriginFlag, Detail: "--a"},
		{Key: "longkey", Value: "v", Origin: config.OriginEnv, Detail: "MY_LONGKEY"},
	}
	out := config.FormatResolvedTable(entries)
	assert.Contains(t, out, "KEY")
	assert.Contains(t, out, "VALUE")
	assert.Contains(t, out, "ORIGIN")
	assert.Contains(t, out, "longkey")
	// Header row shows up before first data row.
	headerIdx := strings.Index(out, "KEY")
	dataIdx := strings.Index(out, "longkey")
	assert.Less(t, headerIdx, dataIdx)
}

func TestResolvedAsJSON(t *testing.T) {
	entries := []config.Resolved[string]{
		{Key: "k1", Value: "v1", Origin: config.OriginEnv, Detail: "K1"},
	}
	got := config.ResolvedAsJSON(entries)
	assert.Equal(t, "v1", got["k1"]["value"])
	assert.Equal(t, "env", got["k1"]["origin"])
	assert.Equal(t, "K1", got["k1"]["detail"])
}

func TestSortedByKey(t *testing.T) {
	entries := []config.Resolved[string]{
		{Key: "c"}, {Key: "a"}, {Key: "b"},
	}
	got := config.SortedByKey(entries)
	assert.Equal(t, "a", got[0].Key)
	assert.Equal(t, "b", got[1].Key)
	assert.Equal(t, "c", got[2].Key)
	// Original slice unchanged.
	assert.Equal(t, "c", entries[0].Key)
}
