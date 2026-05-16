package template_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"hop.top/kit/cmd/kit/template"
	"hop.top/kit/go/console/cli"
	tmpl "hop.top/kit/internal/template"
)

func newRoot(t *testing.T) *cli.Root {
	t.Helper()
	return cli.New(cli.Config{Name: "kit", Version: "test", DisableValidate: true})
}

func runGroup(t *testing.T, args ...string) (string, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	cmd := template.GroupCmd(newRoot(t))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return buf.String(), err
}

func TestList_HumanOutput(t *testing.T) {
	out, err := runGroup(t, "list")
	if err != nil {
		t.Fatalf("list: unexpected error: %v", err)
	}
	for _, want := range []string{"Name", "Description", "Kit_Version", "cli-go"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestList_JSON(t *testing.T) {
	out, err := runGroup(t, "list", "--json")
	if err != nil {
		t.Fatalf("list --json: unexpected error: %v", err)
	}
	var rows []map[string]string
	if derr := json.Unmarshal([]byte(out), &rows); derr != nil {
		t.Fatalf("decode JSON: %v\nraw: %s", derr, out)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one template row")
	}
	found := false
	for _, r := range rows {
		if r["name"] == "cli-go" {
			found = true
			if r["description"] == "" {
				t.Error("cli-go description is empty")
			}
			break
		}
	}
	if !found {
		t.Errorf("cli-go not present in JSON rows: %v", rows)
	}
	// Sanity: cross-check Available() agrees with the row count.
	names, aerr := tmpl.Available()
	if aerr != nil {
		t.Fatalf("Available: %v", aerr)
	}
	if len(rows) != len(names) {
		t.Errorf("row count %d != Available count %d", len(rows), len(names))
	}
}

func TestShow_BuiltIn_Human(t *testing.T) {
	out, err := runGroup(t, "show", "cli-go")
	if err != nil {
		t.Fatalf("show cli-go: unexpected error: %v", err)
	}
	for _, want := range []string{"Name:", "cli-go", "Variables:"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestShow_BuiltIn_JSON(t *testing.T) {
	out, err := runGroup(t, "show", "cli-go", "--json")
	if err != nil {
		t.Fatalf("show cli-go --json: unexpected error: %v", err)
	}
	var m tmpl.Manifest
	if derr := json.Unmarshal([]byte(out), &m); derr != nil {
		t.Fatalf("decode JSON: %v\nraw: %s", derr, out)
	}
	if m.Name != "cli-go" {
		t.Errorf("expected Name=cli-go, got %q", m.Name)
	}
	if len(m.Variables) == 0 {
		t.Error("expected at least one variable in cli-go manifest")
	}
}

func TestShow_NotFound(t *testing.T) {
	out, err := runGroup(t, "show", "nonexistent-template-xyz")
	if err == nil {
		t.Fatalf("expected error for unknown template, got output:\n%s", out)
	}
	// TemplateNotFoundError surfaces as "template not found" or similar;
	// be lenient about the exact phrasing.
	low := strings.ToLower(err.Error())
	if !strings.Contains(low, "not found") && !strings.Contains(low, "nonexistent") {
		t.Errorf("expected error to mention not-found, got: %v", err)
	}
}
