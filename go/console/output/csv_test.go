package output_test

import (
	"bytes"
	"encoding/csv"
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

// --- CR/LF preservation -------------------------------------------------

// adversarialCSVRow pins the quoting rule byte-for-byte. Every field is a
// separate hazard: the delimiter, an internal quote, an embedded LF, a
// leading space, a trailing space, empty, a tab, and a LONE CR.
type adversarialCSVRow struct {
	A string `table:"a"`
	B string `table:"b"`
	C string `table:"c"`
	D string `table:"d"`
	E string `table:"e"`
	F string `table:"f"`
	G string `table:"g"`
	H string `table:"h"`
	I string `table:"i"`
}

func adversarialCSVData() []adversarialCSVRow {
	return []adversarialCSVRow{{
		A: "plain",
		B: "with,comma",
		C: `with"quote`,
		D: "with\nnewline",
		E: " leading space",
		F: "trailing ",
		G: "",
		H: "with\ttab",
		I: "with\rcr",
	}}
}

func adversarialCSVValues() []string {
	return []string{
		"plain", "with,comma", `with"quote`, "with\nnewline",
		" leading space", "trailing ", "", "with\ttab", "with\rcr",
	}
}

// A field holding CR and/or LF is quoted and its bytes survive verbatim, in
// BOTH line-ending modes and BOTH quoting paths. RFC 4180 lists CR and LF as
// separate alternatives inside `escaped`, so a bare CR between quotes is
// legal; dropping it is silent data loss, and promoting LF to CRLF rewrites
// the caller's value. The record terminator is the only place CRLF belongs.
func TestCSV_PreservesCRAndLFVerbatim(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []string
		eol  string
	}{
		{"lf", []string{"no-header=true"}, "\n"},
		{"crlf", []string{"no-header=true", "crlf=true"}, "\r\n"},
		{"lf/quote-all", []string{"no-header=true", "quote-all=true"}, "\n"},
		{"crlf/quote-all", []string{"no-header=true", "quote-all=true", "crlf=true"}, "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := renderCSV(t, adversarialCSVData(), tc.opts, nil)
			require.NoError(t, err)

			assert.Contains(t, out, "\"with\rcr\"", "lone CR must be quoted and preserved, never dropped")
			assert.Contains(t, out, "\"with\nnewline\"", "embedded LF must stay LF inside the field")
			assert.NotContains(t, out, "withcr", "CR must not be silently discarded")
			assert.True(t, strings.HasSuffix(out, tc.eol), "record terminator")
		})
	}
}

// The real acceptance criterion: encode -> decode -> identical. Byte-equality
// alone would be satisfied by every runtime agreeing on lossy output.
func TestCSV_RoundTripsAdversarialRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []string
	}{
		{"lf", []string{"no-header=true"}},
		{"crlf", []string{"no-header=true", "crlf=true"}},
		{"lf/quote-all", []string{"no-header=true", "quote-all=true"}},
		{"crlf/quote-all", []string{"no-header=true", "quote-all=true", "crlf=true"}},
		{"semicolon", []string{"no-header=true", "delimiter=;"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := renderCSV(t, adversarialCSVData(), tc.opts, nil)
			require.NoError(t, err)

			r := csv.NewReader(strings.NewReader(out))
			r.Comma = ','
			for _, o := range tc.opts {
				if o == "delimiter=;" {
					r.Comma = ';'
				}
			}
			recs, err := r.ReadAll()
			require.NoError(t, err, "output must be parseable by encoding/csv")
			require.Len(t, recs, 1, "one input row must decode to exactly one record")
			assert.Equal(t, adversarialCSVValues(), recs[0])
		})
	}
}

// Go's own encoding/csv reader normalises a CRLF *inside* a quoted field to a
// bare LF, so a writer that promotes LF->CRLF in-field looks lossless on a
// Go-only round-trip while still having rewritten the bytes. Assert on the
// bytes directly so the promotion cannot hide.
func TestCSV_CRLFModeDoesNotPromoteInFieldLF(t *testing.T) {
	type note struct {
		Note string `table:"Note"`
	}
	out, err := renderCSV(t, []note{{Note: "a\nb"}}, []string{"no-header=true", "crlf=true"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "\"a\nb\"\r\n", out,
		"in-field LF stays LF; only the record terminator is CRLF")
}

// Go quotes on unicode.IsSpace of the first rune, not merely a literal space.
func TestCSV_QuotesAnyLeadingUnicodeSpace(t *testing.T) {
	type note struct {
		Note string `table:"Note"`
	}
	for _, tc := range []struct {
		name, in, want string
	}{
		{"tab", "\tlead", "\"\tlead\"\n"},
		{"space", " lead", "\" lead\"\n"},
		{"nbsp", "\u00a0lead", "\"\u00a0lead\"\n"},
		{"vtab", "\vlead", "\"\vlead\"\n"},
		{"trailing space stays bare", "trail ", "trail \n"},
		{"plain stays bare", "plain", "plain\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := renderCSV(t, []note{{Note: tc.in}}, []string{"no-header=true"}, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.want, out)
		})
	}
}

// `\.` alone on a line terminates a PostgreSQL COPY stream; encoding/csv
// quotes it defensively and so must we.
func TestCSV_QuotesPostgresCopySentinel(t *testing.T) {
	type note struct {
		Note string `table:"Note"`
	}
	out, err := renderCSV(t, []note{{Note: `\.`}}, []string{"no-header=true"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "\"\\.\"\n", out)
}
