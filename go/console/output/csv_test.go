package output_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

type csvRow struct {
	ID    string `table:"ID"`
	Name  string `table:"Name"`
	Score int    `table:"Score"`
}

func renderCSV(t *testing.T, data any, optPairs []string, cols []string) (string, error) {
	t.Helper()
	f, ok := output.Default.Lookup(output.CSV)
	require.True(t, ok, "csv formatter must be registered on Default")
	opts, err := output.ParseOptions(optPairs, f.Options())
	require.NoError(t, err)
	var buf bytes.Buffer
	if err := f.Render(&buf, data, opts, cols); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func TestCSV_DefaultDelimiterWithHeader(t *testing.T) {
	data := []csvRow{
		{ID: "1", Name: "alpha", Score: 10},
		{ID: "2", Name: "beta", Score: 20},
	}
	out, err := renderCSV(t, data, nil, nil)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3, "header + 2 rows")
	assert.Equal(t, "ID,Name,Score", lines[0])
	assert.Equal(t, "1,alpha,10", lines[1])
	assert.Equal(t, "2,beta,20", lines[2])
}

func TestCSV_DelimiterOverride(t *testing.T) {
	data := []csvRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderCSV(t, data, []string{"delimiter=;"}, nil)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "ID;Name;Score", lines[0])
	assert.Equal(t, "1;alpha;10", lines[1])
}

func TestCSV_NoHeader(t *testing.T) {
	data := []csvRow{
		{ID: "1", Name: "alpha", Score: 10},
		{ID: "2", Name: "beta", Score: 20},
	}
	out, err := renderCSV(t, data, []string{"no-header=true"}, nil)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "no header, only 2 data rows")
	assert.Equal(t, "1,alpha,10", lines[0])
	assert.Equal(t, "2,beta,20", lines[1])
}

func TestCSV_QuoteAll(t *testing.T) {
	data := []csvRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderCSV(t, data, []string{"quote-all=true"}, nil)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, `"ID","Name","Score"`, lines[0])
	assert.Equal(t, `"1","alpha","10"`, lines[1])
}

func TestCSV_QuoteAll_EscapesInternalQuote(t *testing.T) {
	type quoted struct {
		Note string `table:"Note"`
	}
	data := []quoted{{Note: `say "hi"`}}
	out, err := renderCSV(t, data, []string{"quote-all=true"}, nil)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, `"Note"`, lines[0])
	assert.Equal(t, `"say ""hi"""`, lines[1])
}

func TestCSV_CRLF(t *testing.T) {
	data := []csvRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderCSV(t, data, []string{"crlf=true"}, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "\r\n")
	// Both lines (header + row) end in CRLF.
	assert.Equal(t, 2, strings.Count(out, "\r\n"))
}

func TestCSV_CRLFWithQuoteAll(t *testing.T) {
	data := []csvRow{{ID: "1", Name: "alpha", Score: 10}}
	out, err := renderCSV(t, data, []string{"crlf=true", "quote-all=true"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(out, "\r\n"))
	assert.Contains(t, out, `"ID","Name","Score"`+"\r\n")
}

func TestCSV_EmptySlice(t *testing.T) {
	out, err := renderCSV(t, []csvRow{}, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, out, "empty slice produces no output (parity with table formatter)")
}

func TestCSV_ColsSubset(t *testing.T) {
	data := []csvRow{
		{ID: "1", Name: "alpha", Score: 10},
		{ID: "2", Name: "beta", Score: 20},
	}
	out, err := renderCSV(t, data, nil, []string{"ID", "Score"})
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "ID,Score", lines[0])
	assert.Equal(t, "1,10", lines[1])
	assert.Equal(t, "2,20", lines[2])
}

func TestCSV_UnknownColumn(t *testing.T) {
	data := []csvRow{{ID: "1", Name: "alpha", Score: 10}}
	_, err := renderCSV(t, data, nil, []string{"Bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bogus")
}

func TestCSV_RegisteredOnDefault(t *testing.T) {
	f, ok := output.Default.Lookup(output.CSV)
	require.True(t, ok)
	assert.Equal(t, "csv", f.Key())

	exts := output.Default.ExtensionMap()
	assert.Equal(t, "csv", exts[".csv"], ".csv extension must map to csv formatter")
}

func TestCSV_SingleStruct(t *testing.T) {
	out, err := renderCSV(t, csvRow{ID: "1", Name: "alpha", Score: 10}, nil, nil)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "header + 1 row")
	assert.Equal(t, "ID,Name,Score", lines[0])
	assert.Equal(t, "1,alpha,10", lines[1])
}

func TestCSV_DelimiterMustBeSingleChar(t *testing.T) {
	data := []csvRow{{ID: "1", Name: "alpha", Score: 10}}
	_, err := renderCSV(t, data, []string{"delimiter=;;"}, nil)
	require.Error(t, err)
}
