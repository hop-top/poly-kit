package output_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

type textRow struct {
	ID    string `table:"ID"`
	Name  string `table:"Name"`
	Score int    `table:"Score"`
}

func renderText(t *testing.T, data any, optPairs []string, cols []string) (string, error) {
	t.Helper()
	f, ok := output.Default.Lookup(output.Text)
	require.True(t, ok, "text formatter must be registered on Default")
	opts, err := output.ParseOptions(optPairs, f.Options())
	require.NoError(t, err)
	var buf bytes.Buffer
	if err := f.Render(&buf, data, opts, cols); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func TestText_KVDefault(t *testing.T) {
	data := []textRow{
		{ID: "1", Name: "alpha", Score: 10},
		{ID: "2", Name: "beta", Score: 20},
	}
	out, err := renderText(t, data, nil, nil)
	require.NoError(t, err)

	expected := "ID=1\nName=alpha\nScore=10\n\nID=2\nName=beta\nScore=20\n"
	assert.Equal(t, expected, out)
}

func TestText_KVSeparatorOverride(t *testing.T) {
	data := []textRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderText(t, data, []string{"separator=: "}, nil)
	require.NoError(t, err)

	expected := "ID: 1\nName: alpha\nScore: 10\n"
	assert.Equal(t, expected, out)
}

func TestText_KVSingleRecordNoTrailingBlank(t *testing.T) {
	data := []textRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderText(t, data, nil, nil)
	require.NoError(t, err)

	// Single record: no inter-record blank line.
	assert.Equal(t, "ID=1\nName=alpha\nScore=10\n", out)
}

func TestText_LinesStyle(t *testing.T) {
	data := []textRow{
		{ID: "1", Name: "alpha", Score: 10},
		{ID: "2", Name: "beta", Score: 20},
	}
	out, err := renderText(t, data, []string{"style=lines"}, nil)
	require.NoError(t, err)

	expected := "1\talpha\t10\n2\tbeta\t20\n"
	assert.Equal(t, expected, out)
}

func TestText_LinesStyleNoHeader(t *testing.T) {
	data := []textRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderText(t, data, []string{"style=lines"}, nil)
	require.NoError(t, err)

	// First and only line is data, no header.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 1)
	assert.NotContains(t, lines[0], "ID")
	assert.NotContains(t, lines[0], "Name")
}

func TestText_ParagraphStyle(t *testing.T) {
	data := []textRow{
		{ID: "1", Name: "alpha", Score: 10},
		{ID: "2", Name: "beta", Score: 20},
	}
	out, err := renderText(t, data, []string{"style=paragraph"}, nil)
	require.NoError(t, err)

	expected := "Record 1:\n  ID: 1\n  Name: alpha\n  Score: 10\n\n" +
		"Record 2:\n  ID: 2\n  Name: beta\n  Score: 20\n"
	assert.Equal(t, expected, out)
}

func TestText_EmptySlice(t *testing.T) {
	out, err := renderText(t, []textRow{}, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, out, "empty slice produces no output")
}

func TestText_ColsFilter(t *testing.T) {
	data := []textRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderText(t, data, nil, []string{"ID", "Score"})
	require.NoError(t, err)

	assert.Equal(t, "ID=1\nScore=10\n", out)
	assert.NotContains(t, out, "Name")
	assert.NotContains(t, out, "alpha")
}

func TestText_ColsFilterLines(t *testing.T) {
	data := []textRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderText(t, data, []string{"style=lines"}, []string{"Name", "Score"})
	require.NoError(t, err)

	// Filter respects struct order, not selected order.
	assert.Equal(t, "alpha\t10\n", out)
}

func TestText_ColsFilterParagraph(t *testing.T) {
	data := []textRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderText(t, data, []string{"style=paragraph"}, []string{"Name"})
	require.NoError(t, err)

	expected := "Record 1:\n  Name: alpha\n"
	assert.Equal(t, expected, out)
}

func TestText_UnknownColumn(t *testing.T) {
	data := []textRow{{ID: "1", Name: "alpha", Score: 10}}
	_, err := renderText(t, data, nil, []string{"Bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bogus")
}

func TestText_BadStyleRejected(t *testing.T) {
	f, ok := output.Default.Lookup(output.Text)
	require.True(t, ok)
	_, err := output.ParseOptions([]string{"style=xml"}, f.Options())
	require.Error(t, err, "enum option must reject unknown values at parse time")
	assert.Contains(t, err.Error(), "xml")
}

func TestText_RegisteredOnDefault(t *testing.T) {
	f, ok := output.Default.Lookup(output.Text)
	require.True(t, ok)
	assert.Equal(t, "text", f.Key())

	exts := output.Default.ExtensionMap()
	assert.Equal(t, "text", exts[".txt"], ".txt extension must map to text formatter")
}

func TestText_SingleStruct(t *testing.T) {
	out, err := renderText(t, textRow{ID: "1", Name: "alpha", Score: 10}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "ID=1\nName=alpha\nScore=10\n", out)
}
