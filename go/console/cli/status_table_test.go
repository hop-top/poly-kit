package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
)

// runStatusFixture executes `status` on a fixture root carrying a
// single adopter provider, in the requested format.
func runStatusFixture(t *testing.T, format string, secs ...cli.StatusSection) string {
	t.Helper()
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true},
		cli.WithStatus(cli.StatusConfig{DisableDefaultProviders: []string{
			"profile", "env", "workspace", "auth", "effective-config", "kit-annotations",
		}}))
	for _, sec := range secs {
		r.RegisterStatusProvider(sec.Title, func(_ context.Context) (cli.StatusSection, error) {
			return sec, nil
		})
	}
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(&out)
	r.Cmd.SetArgs([]string{"status", "--format", format})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))
	return out.String()
}

// TestStatus_TableFormatEmitsRows pins the defect: `<tool> status` under
// the default table format printed zero bytes at exit 0, because
// StatusOutput is a slice-wrapping struct with no `table:""` tags, so
// the column resolver found no columns and returned nil having written
// nothing.
func TestStatus_TableFormatEmitsRows(t *testing.T) {
	body := runStatusFixture(t, "table", cli.StatusSection{
		Title:  "adopter",
		Status: cli.StatusOK,
		Data:   map[string]string{"hello": "world"},
	})

	require.NotEmpty(t, strings.TrimSpace(body),
		"status must not render zero bytes under the default table format")
	assert.Contains(t, body, "SECTION")
	assert.Contains(t, body, "STATUS")
	assert.Contains(t, body, "DETAIL")
	assert.Contains(t, body, "adopter")
	assert.Contains(t, body, "ok")
	assert.Contains(t, body, "hello=world")
}

// TestStatus_DefaultFormatIsNotEmpty exercises the real default path:
// no explicit --format at all, which is how an operator invokes it.
func TestStatus_DefaultFormatIsNotEmpty(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true},
		cli.WithStatus(cli.StatusConfig{}))
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(&out)
	r.Cmd.SetArgs([]string{"status"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))
	assert.NotEmpty(t, strings.TrimSpace(out.String()),
		"bare `status` must print something")
}

// TestStatus_TableRowPerSection asserts the chosen shape: one row per
// section, not one row per field of every payload.
func TestStatus_TableRowPerSection(t *testing.T) {
	body := runStatusFixture(t, "table",
		cli.StatusSection{Title: "alpha", Status: cli.StatusOK, Priority: 1,
			Data: map[string]string{"a": "1", "b": "2", "c": "3"}},
		cli.StatusSection{Title: "beta", Status: cli.StatusOK, Priority: 2,
			Data: map[string]string{"d": "4"}},
	)
	lines := []string{}
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	// header + one row per section.
	assert.Len(t, lines, 3, "expected header + 2 section rows, got:\n%s", body)
	assert.Contains(t, lines[1], "alpha")
	assert.Contains(t, lines[2], "beta")
}

// TestStatus_TableErrorMessageFoldsIntoDetail pins that ErrorMessage
// shares the DETAIL column rather than taking a column of its own.
func TestStatus_TableErrorMessageFoldsIntoDetail(t *testing.T) {
	body := runStatusFixture(t, "table", cli.StatusSection{
		Title:        "broken",
		Status:       cli.StatusError,
		ErrorMessage: "backend refused connection",
	})
	assert.Contains(t, body, "backend refused connection")
	assert.NotContains(t, body, "ERROR_MESSAGE")
	assert.NotContains(t, body, "ERRORMESSAGE")
}

// TestStatus_TableNeverDumpsNestedStruct guards the cell budget: a
// nested payload must be summarized, never expanded into the cell.
func TestStatus_TableNeverDumpsNestedStruct(t *testing.T) {
	body := runStatusFixture(t, "table", cli.StatusSection{
		Title:  "nested",
		Status: cli.StatusOK,
		Data: map[string]any{
			"inner": map[string]any{"deep": "value", "deeper": "value2"},
			"list":  []string{"x", "y", "z"},
		},
	})
	assert.NotContains(t, body, "deep", "nested sub-tree must not be dumped into the cell")
	assert.Contains(t, body, "inner={2}")
	assert.Contains(t, body, "list=[3]")
}

// TestStatus_TableDetailIsDeterministic guards against Go's random map
// iteration order leaking into rendered output.
func TestStatus_TableDetailIsDeterministic(t *testing.T) {
	sec := cli.StatusSection{
		Title:  "many",
		Status: cli.StatusOK,
		Data:   map[string]string{"z": "1", "a": "2", "m": "3"},
	}
	first := runStatusFixture(t, "table", sec)
	for range 12 {
		assert.Equal(t, first, runStatusFixture(t, "table", sec),
			"table detail cell must be byte-stable across runs")
	}
}

// TestStatus_TableDetailTruncatesWideMaps pins the "+N more" counter so
// a wide payload cannot blow out the column widths.
func TestStatus_TableDetailTruncatesWideMaps(t *testing.T) {
	data := map[string]string{}
	for _, k := range []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7"} {
		data[k] = "v"
	}
	body := runStatusFixture(t, "table", cli.StatusSection{
		Title: "wide", Status: cli.StatusOK, Data: data,
	})
	assert.Contains(t, body, "+3 more")
}

// TestStatus_TableEmptyAndUnavailableAreLabelled asserts a section with
// nothing to report is visibly distinct from a render failure.
func TestStatus_TableEmptyAndUnavailableAreLabelled(t *testing.T) {
	body := runStatusFixture(t, "table",
		cli.StatusSection{Title: "nothing", Status: cli.StatusEmpty, Priority: 1},
		cli.StatusSection{Title: "gone", Status: cli.StatusUnavailable, Priority: 2},
	)
	assert.Contains(t, body, "(no entries)")
	assert.Contains(t, body, "(unavailable)")
}

// TestStatus_StructuredFormatsKeepNestedShape is the regression guard
// for the fix itself: the flat projection must reach ONLY the
// tag-driven formats. json/yaml feed scripts and must keep emitting the
// nested {sections:[{title,data,status}]} wire shape.
func TestStatus_StructuredFormatsKeepNestedShape(t *testing.T) {
	sec := cli.StatusSection{
		Title:  "adopter",
		Status: cli.StatusOK,
		Data:   map[string]string{"hello": "world"},
	}

	body := runStatusFixture(t, "json", sec)
	var payload cli.StatusOutput
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	require.Len(t, payload.Sections, 1)
	assert.Equal(t, "adopter", payload.Sections[0].Title)
	require.NotNil(t, payload.Sections[0].Data, "json must keep the nested Data payload")
	assert.Contains(t, body, `"sections"`)
	assert.NotContains(t, body, `"section"`, "json must not receive the flat projection")

	yamlBody := runStatusFixture(t, "yaml", sec)
	assert.Contains(t, yamlBody, "sections:")
	assert.Contains(t, yamlBody, "hello: world")
}

// TestStatus_CSVGetsFlatProjection confirms the projection reaches the
// other tag-driven formats too, not just table.
func TestStatus_CSVGetsFlatProjection(t *testing.T) {
	body := runStatusFixture(t, "csv", cli.StatusSection{
		Title: "adopter", Status: cli.StatusOK, Data: map[string]string{"hello": "world"},
	})
	assert.Contains(t, body, "SECTION,STATUS,DETAIL")
	assert.Contains(t, body, "adopter,ok,hello=world")
}
