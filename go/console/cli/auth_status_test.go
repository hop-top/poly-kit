package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/storage/secret"
)

// fakeMetaStore implements both secret.Store and secret.MetadataReader
// for table tests. metaByKey drives the per-key Metadata answers; an
// entry's err takes precedence over its meta.
type fakeMetaStore struct {
	metaByKey map[string]struct {
		meta secret.StoredMeta
		err  error
	}
}

func (f *fakeMetaStore) Get(_ context.Context, _ string) (*secret.Secret, error) {
	return nil, secret.ErrNotFound
}
func (f *fakeMetaStore) List(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (f *fakeMetaStore) Exists(_ context.Context, _ string) (bool, error)   { return false, nil }
func (f *fakeMetaStore) Metadata(_ context.Context, key string) (secret.StoredMeta, error) {
	row, ok := f.metaByKey[key]
	if !ok {
		return secret.StoredMeta{}, secret.ErrNotFound
	}
	return row.meta, row.err
}

// noMetaStore implements secret.Store but NOT secret.MetadataReader,
// to drive the rejection test.
type noMetaStore struct{}

func (noMetaStore) Get(_ context.Context, _ string) (*secret.Secret, error) {
	return nil, secret.ErrNotFound
}
func (noMetaStore) List(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (noMetaStore) Exists(_ context.Context, _ string) (bool, error)   { return false, nil }

func newFakeStore() *fakeMetaStore {
	upd := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return &fakeMetaStore{
		metaByKey: map[string]struct {
			meta secret.StoredMeta
			err  error
		}{
			"github_token": {
				meta: secret.StoredMeta{
					Key:       "github_token",
					Source:    "keyring/kit",
					Backend:   "keyring",
					UpdatedAt: upd,
				},
			},
			"openai_api_key": {
				meta: secret.StoredMeta{
					Key:       "openai_api_key",
					Source:    "openbao/secret/openai",
					Backend:   "openbao",
					UpdatedAt: upd,
					Scopes:    []string{"read", "write"},
				},
			},
		},
	}
}

// runCmd executes the given cobra.Command with provided args and
// returns its stdout. Side-effect annotation must already be set.
func runCmd(t *testing.T, cmd *cobra.Command, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	return buf.String()
}

func TestAuthStatusCmd_RendersTable(t *testing.T) {
	store := newFakeStore()
	cmd := cli.AuthStatusCmd(store, []string{"github_token", "openai_api_key"})

	// Wire a --format flag so resolveAuthStatusFormat picks "table".
	cmd.PersistentFlags().String("format", "table", "")

	out := runCmd(t, cmd)

	for _, want := range []string{
		"github_token",
		"openai_api_key",
		"keyring/kit",
		"openbao/secret/openai",
		"ok",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestAuthStatusCmd_RendersJSON(t *testing.T) {
	store := newFakeStore()
	cmd := cli.AuthStatusCmd(store, []string{"github_token", "openai_api_key"})

	cmd.PersistentFlags().String("format", "table", "")
	if err := cmd.PersistentFlags().Set("format", "json"); err != nil {
		t.Fatalf("set format: %v", err)
	}

	out := runCmd(t, cmd)

	var rows []cli.AuthStatusRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Status != "ok" {
			t.Errorf("row %q: Status = %q, want ok", r.Key, r.Status)
		}
		if r.Source == "" {
			t.Errorf("row %q: Source empty", r.Key)
		}
		if strings.Contains(r.Source, "value") {
			t.Errorf("row %q: source must not embed secret value: %q", r.Key, r.Source)
		}
	}
}

func TestAuthStatusCmd_RefusesIfBackendNotMetadataReader(t *testing.T) {
	cmd := cli.AuthStatusCmd(noMetaStore{}, []string{"any_key"})
	cmd.PersistentFlags().String("format", "table", "")
	cmd.SilenceErrors = true

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when backend is not MetadataReader")
	}
	if !strings.Contains(err.Error(), "backend does not expose metadata") {
		t.Errorf("error message = %q; want substring 'backend does not expose metadata'", err.Error())
	}
}

func TestAuthStatusCmd_SideEffectIsRead(t *testing.T) {
	cmd := cli.AuthStatusCmd(newFakeStore(), nil)
	got, ok := cli.GetSideEffect(cmd)
	if !ok {
		t.Fatal("expected side-effect annotation set")
	}
	if got != cli.SideEffectRead {
		t.Errorf("side-effect = %q, want %q", got, cli.SideEffectRead)
	}
}

func TestAuthStatusCmd_MissingKeyShowsAsMissing(t *testing.T) {
	store := newFakeStore()
	cmd := cli.AuthStatusCmd(store, []string{"github_token", "missing_key"})

	cmd.PersistentFlags().String("format", "table", "")
	if err := cmd.PersistentFlags().Set("format", "json"); err != nil {
		t.Fatalf("set format: %v", err)
	}

	out := runCmd(t, cmd)

	var rows []cli.AuthStatusRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out)
	}
	statusByKey := map[string]string{}
	for _, r := range rows {
		statusByKey[r.Key] = r.Status
	}
	if statusByKey["github_token"] != "ok" {
		t.Errorf("github_token status = %q, want ok", statusByKey["github_token"])
	}
	if statusByKey["missing_key"] != "missing" {
		t.Errorf("missing_key status = %q, want missing", statusByKey["missing_key"])
	}
}

func TestAuthStatusCmd_NotSupportedShowsAsUnsupported(t *testing.T) {
	store := &fakeMetaStore{
		metaByKey: map[string]struct {
			meta secret.StoredMeta
			err  error
		}{
			"k1": {err: fmt.Errorf("env backend: %w", secret.ErrNotSupported)},
		},
	}

	cmd := cli.AuthStatusCmd(store, []string{"k1"})
	cmd.PersistentFlags().String("format", "table", "")
	if err := cmd.PersistentFlags().Set("format", "json"); err != nil {
		t.Fatalf("set format: %v", err)
	}

	out := runCmd(t, cmd)
	var rows []cli.AuthStatusRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out)
	}
	if rows[0].Status != "unsupported" {
		t.Errorf("Status = %q, want unsupported", rows[0].Status)
	}
}

func TestCollectAuthStatus_StripsSecretValueFromOutput(t *testing.T) {
	// Defense-in-depth: even if someone mistakenly populates AuthStatusRow
	// with secret-looking data via Metadata, the row struct itself has no
	// Value field at all. This test asserts that contract.
	rows, err := cli.CollectAuthStatus(context.Background(), newFakeStore(), []string{"github_token"})
	if err != nil {
		t.Fatalf("CollectAuthStatus: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows len = %d", len(rows))
	}
	data, _ := json.Marshal(rows[0])
	if strings.Contains(string(data), "value") {
		t.Errorf("AuthStatusRow JSON unexpectedly contains 'value' key: %s", data)
	}
}

// Ensure CollectAuthStatus surfaces the same default format flag the
// kit Root would have wired.
func TestAuthStatusCmd_DefaultFormatIsTableWhenNoFlag(t *testing.T) {
	store := newFakeStore()
	cmd := cli.AuthStatusCmd(store, []string{"github_token"})
	// Note: NO --format flag wired anywhere — exercising the fallback.

	out := runCmd(t, cmd)
	if !strings.Contains(out, "github_token") {
		t.Errorf("expected table output to contain key; got %q", out)
	}
	// JSON would start with "[" or "{"; table starts with the header.
	if strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("expected table format, got JSON-shaped output:\n%s", out)
	}
}

// Sanity: ensure the helper works when wired under a parent "auth"
// command, mirroring real adopter usage in the spec.
func TestAuthStatusCmd_NestedUnderAuthParent(t *testing.T) {
	store := newFakeStore()
	authCmd := &cobra.Command{Use: "auth", Short: "Authentication"}
	statusCmd := cli.AuthStatusCmd(store, []string{"github_token"})
	authCmd.AddCommand(statusCmd)
	authCmd.PersistentFlags().String("format", "table", "")
	if err := authCmd.PersistentFlags().Set("format", "json"); err != nil {
		t.Fatalf("set format: %v", err)
	}

	out := runCmd(t, authCmd, "status")
	var rows []cli.AuthStatusRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].Key != "github_token" {
		t.Errorf("rows = %+v", rows)
	}
}

// Render-format conformance: ensure the default registry can format
// AuthStatusRow rows in every shipping format.
func TestAuthStatusRow_RendersInAllBuiltinFormats(t *testing.T) {
	rows := []cli.AuthStatusRow{{Key: "k", Status: "ok", Source: "memory/test"}}
	for _, f := range []string{output.JSON, output.YAML, output.Table} {
		var buf bytes.Buffer
		if err := output.Render(&buf, f, rows); err != nil {
			t.Errorf("Render(%s): %v", f, err)
		}
		if buf.Len() == 0 {
			t.Errorf("Render(%s): empty output", f)
		}
	}
}
