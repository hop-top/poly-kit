package cli_test

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

func testCmd() *cobra.Command {
	return &cobra.Command{
		Use: "testcmd", Short: "test",
		Run: func(_ *cobra.Command, _ []string) {},
	}
}

func TestRegisterStream_AddsMetadata(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training event log")

	raw, ok := cmd.Annotations["streams"]
	require.True(t, ok, "streams annotation must exist")

	var meta []map[string]string
	require.NoError(t, json.Unmarshal([]byte(raw), &meta))
	require.Len(t, meta, 1)
	assert.Equal(t, "training", meta[0]["name"])
	assert.Equal(t, "Training event log", meta[0]["description"])
}

func TestRegisterStream_AddsStreamFlag(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training log")

	f := cmd.Flags().Lookup("stream")
	require.NotNil(t, f, "--stream flag must be registered")
}

func TestRegisterStream_MultipleStreams(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training log")
	cli.RegisterStream(cmd, "metrics", "Metric events")

	raw := cmd.Annotations["streams"]
	var meta []map[string]string
	require.NoError(t, json.Unmarshal([]byte(raw), &meta))
	assert.Len(t, meta, 2)
	assert.Equal(t, "training", meta[0]["name"])
	assert.Equal(t, "metrics", meta[1]["name"])
}

func TestChannel_Enabled_ReturnsWriter(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training log")

	// Simulate --stream training being set.
	require.NoError(t, cmd.Flags().Set("stream", "training"))

	w := cli.Channel(cmd, "training")
	assert.NotEqual(t, io.Discard, w,
		"enabled channel must not be discard")

	// Verify the writer is functional (writes without error).
	n, err := w.Write([]byte("hello\n"))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
}

func TestChannel_Disabled_ReturnsDiscard(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training log")
	// Don't set --stream.

	w := cli.Channel(cmd, "training")
	assert.Equal(t, io.Discard, w,
		"disabled channel must return io.Discard")
}

func TestChannel_MultipleStreams_OnlyEnabledWrites(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training log")
	cli.RegisterStream(cmd, "metrics", "Metric events")
	require.NoError(t, cmd.Flags().Set("stream", "training"))

	tw := cli.Channel(cmd, "training")
	mw := cli.Channel(cmd, "metrics")

	assert.NotEqual(t, io.Discard, tw)
	assert.Equal(t, io.Discard, mw,
		"non-enabled stream must return discard")
}

func TestChannel_CommaSeparated(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training log")
	cli.RegisterStream(cmd, "metrics", "Metric events")
	require.NoError(t, cmd.Flags().Set("stream", "training,metrics"))

	tw := cli.Channel(cmd, "training")
	mw := cli.Channel(cmd, "metrics")

	assert.NotEqual(t, io.Discard, tw)
	assert.NotEqual(t, io.Discard, mw,
		"comma-separated streams must both be active")
}

func TestStreams_HelpSection(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training event log")
	cli.RegisterStream(cmd, "metrics", "Metric events")

	usage := cmd.UsageString()
	assert.Contains(t, strings.ToUpper(usage), "STREAMS",
		"help must contain STREAMS section")
	assert.Contains(t, usage, "training")
	assert.Contains(t, usage, "metrics")
}

func TestStreams_NoSectionWithoutRegistration(t *testing.T) {
	cmd := testCmd()
	usage := cmd.UsageString()
	assert.NotContains(t, strings.ToUpper(usage), "STREAMS",
		"help must not contain STREAMS when no streams registered")
}

func TestChannel_ConcurrentWrites(t *testing.T) {
	cmd := testCmd()
	cli.RegisterStream(cmd, "training", "Training log")
	require.NoError(t, cmd.Flags().Set("stream", "training"))

	w := cli.Channel(cmd, "training")
	require.NotEqual(t, io.Discard, w)

	// Concurrent writes must not panic or race.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = w.Write([]byte("line\n"))
		}()
	}
	wg.Wait()
}
