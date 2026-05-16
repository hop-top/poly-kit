package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
)

func TestWithStatus_MountsCmd(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true}, cli.WithStatus(cli.StatusConfig{}))
	var found bool
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "status" {
			found = true
			break
		}
	}
	assert.True(t, found, "WithStatus must mount the status subcommand")
}

func TestWithStatus_ReservedSnapshot(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true}, cli.WithStatus(cli.StatusConfig{}))
	assert.True(t, r.IsReserved("status"))
}

func TestStatusRun_RendersDefaultSections(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true}, cli.WithStatus(cli.StatusConfig{}))
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(&out)
	r.Cmd.SetArgs([]string{"status", "--format", "json"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	var payload cli.StatusOutput
	require.NoError(t, json.Unmarshal(out.Bytes(), &payload))
	titles := map[string]bool{}
	for _, s := range payload.Sections {
		titles[s.Title] = true
	}
	for _, want := range []string{"profile", "env", "workspace", "auth", "effective-config", "kit-annotations"} {
		assert.True(t, titles[want], "default section missing: %s", want)
	}
}

func TestStatus_RedactsSensitiveByDefault(t *testing.T) {
	t.Setenv("KIT_FAKE_TOKEN", "supersecret")
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true}, cli.WithStatus(cli.StatusConfig{}))
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetArgs([]string{"status", "--format", "json"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	body := out.String()
	assert.NotContains(t, body, "supersecret")
	assert.Contains(t, body, "[redacted]")
}

func TestStatus_ShowSensitiveRevealsValues(t *testing.T) {
	t.Setenv("KIT_FAKE_TOKEN", "supersecret")
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true}, cli.WithStatus(cli.StatusConfig{}))
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetArgs([]string{"status", "--show-sensitive", "--format", "json"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	body := out.String()
	assert.Contains(t, body, "supersecret")
}

func TestStatus_DisabledFlagOmitsShowSensitive(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true},
		cli.WithStatus(cli.StatusConfig{ShowSensitiveFlag: cli.StatusShowSensitiveDisabled}))
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "status" {
			assert.Nil(t, c.Flags().Lookup("show-sensitive"))
			return
		}
	}
	t.Fatal("status subcommand not mounted")
}

func TestRegisterStatusProvider_AdopterAddition(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true}, cli.WithStatus(cli.StatusConfig{}))
	r.RegisterStatusProvider("adopter", func(_ context.Context) (cli.StatusSection, error) {
		return cli.StatusSection{
			Title:    "adopter",
			Status:   cli.StatusOK,
			Priority: 1000,
			Data:     map[string]string{"hello": "world"},
		}, nil
	})

	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetArgs([]string{"status", "--format", "json"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	body := out.String()
	assert.Contains(t, body, `"hello"`)
	assert.Contains(t, body, `"world"`)
	assert.Contains(t, body, `"adopter"`)
}

func TestStatus_ProviderTimeout(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true},
		cli.WithStatus(cli.StatusConfig{DisableDefaultProviders: []string{
			"profile", "env", "workspace", "auth", "effective-config", "kit-annotations",
		}}))
	r.RegisterStatusProvider("slow", func(ctx context.Context) (cli.StatusSection, error) {
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		return cli.StatusSection{Title: "slow", Status: cli.StatusOK}, nil
	})

	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetArgs([]string{"status", "--format", "json"})
	start := time.Now()
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 4*time.Second,
		"per-provider deadline should clamp the run well below the 5s sleep")

	var payload cli.StatusOutput
	require.NoError(t, json.Unmarshal(out.Bytes(), &payload))
	require.Len(t, payload.Sections, 1)
	assert.Equal(t, cli.StatusUnavailable, payload.Sections[0].Status)
	assert.True(t, strings.Contains(payload.Sections[0].ErrorMessage, "timeout"))
}

func TestStatus_DisableDefaultProviders(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true},
		cli.WithStatus(cli.StatusConfig{DisableDefaultProviders: []string{"workspace", "env"}}))

	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetArgs([]string{"status", "--format", "json"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	var payload cli.StatusOutput
	require.NoError(t, json.Unmarshal(out.Bytes(), &payload))
	for _, sec := range payload.Sections {
		assert.NotEqual(t, "workspace", sec.Title)
		assert.NotEqual(t, "env", sec.Title)
	}
}
