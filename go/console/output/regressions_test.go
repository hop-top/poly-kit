package output_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

type regRow struct {
	ID    string `table:"ID"`
	Name  string `table:"Name"`
	Score int    `table:"Score"`
}

func TestRegression_LargeSlice(t *testing.T) {
	data := make([]regRow, 1000)
	for i := range data {
		data[i] = regRow{ID: fmt.Sprintf("id-%d", i), Name: "item", Score: i}
	}
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, data))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	assert.Equal(t, 1001, len(lines), "expect 1 header + 1000 rows")
}

func TestRegression_SingleElement(t *testing.T) {
	data := []regRow{{ID: "1", Name: "solo", Score: 42}}
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, data))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	assert.Equal(t, 2, len(lines), "expect 1 header + 1 row")
	assert.Contains(t, lines[1], "solo")
}

func TestRegression_EmptySlice(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, []regRow{}))
	assert.Empty(t, buf.String(), "empty slice must produce no output")
}

func TestRegression_PointerSlice(t *testing.T) {
	data := []*regRow{
		{ID: "p1", Name: "alpha", Score: 10},
		{ID: "p2", Name: "beta", Score: 20},
	}
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, data))

	out := buf.String()
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "beta")

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	assert.Equal(t, 3, len(lines), "expect 1 header + 2 rows")
}

func TestRegression_FieldOrdering(t *testing.T) {
	type ordered struct {
		Z string `table:"Zulu"`
		A string `table:"Alpha"`
		M string `table:"Mike"`
	}
	data := []ordered{{Z: "z", A: "a", M: "m"}}
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, data))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 1)

	header := lines[0]
	zIdx := strings.Index(header, "Zulu")
	aIdx := strings.Index(header, "Alpha")
	mIdx := strings.Index(header, "Mike")

	assert.Less(t, zIdx, aIdx, "Zulu before Alpha (struct field order)")
	assert.Less(t, aIdx, mIdx, "Alpha before Mike (struct field order)")
}

func TestRegression_NonSliceStruct(t *testing.T) {
	item := regRow{ID: "single", Name: "direct", Score: 99}
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, item))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	assert.Equal(t, 2, len(lines), "expect 1 header + 1 row")
	assert.Contains(t, lines[1], "direct")
}
