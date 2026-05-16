package bridge

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validBaseManifest returns a fresh, fully-populated manifest that
// passes Validate. Each test mutates a single field to assert the
// invariant under test fires in isolation.
func validBaseManifest() *Manifest {
	return &Manifest{
		Name:    "valid",
		Version: "1.0.0",
		Binary:  "valid",
		Mode:    ModeSubprocess,
		Accepts: []AcceptRule{
			{
				Kind:     KindURL,
				Priority: 5,
				Schemes:  []string{"http", "https"},
			},
			{
				Kind:     KindText,
				Priority: 5,
				MIME:     []string{"text/*"},
			},
		},
		Invoke: InvokeSpec{
			Args:    []string{"--from-bridge"},
			Timeout: 10 * time.Second,
		},
	}
}

func TestLoad_TwoValidOneInvalid(t *testing.T) {
	ms, err := Load("testdata/manifests")
	require.NoError(t, err)
	require.Len(t, ms, 2, "invalid.yaml must be skipped, valid pair kept")

	// Sorted alphabetically by Name: ctxt before tlc.
	assert.Equal(t, "ctxt", ms[0].Name)
	assert.Equal(t, "tlc", ms[1].Name)

	// Deep field check confirms full YAML parse path:
	assert.Equal(t, "0.42.0", ms[0].Version)
	assert.Equal(t, ModeSubprocess, ms[0].Mode)
	assert.Equal(t, 30*time.Second, ms[0].Invoke.Timeout)
	assert.Equal(t, 15*time.Second, ms[1].Invoke.Timeout)

	// First accept rule on ctxt is the URL rule with priority 10.
	require.NotEmpty(t, ms[0].Accepts)
	assert.Equal(t, KindURL, ms[0].Accepts[0].Kind)
	assert.Equal(t, 10, ms[0].Accepts[0].Priority)
	assert.Equal(t, []string{"http", "https"}, ms[0].Accepts[0].Schemes)

	// FallbackInproc populated on ctxt; nil on tlc.
	require.NotNil(t, ms[0].FallbackInproc)
	assert.True(t, ms[0].FallbackInproc.Enabled)
	// $HOME stored verbatim — expansion happens at dispatch, not load.
	assert.Equal(t, "$HOME/.run/ctxt/bridge.sock", ms[0].FallbackInproc.Socket)
	assert.Nil(t, ms[1].FallbackInproc)

	// Env values with ${payload.*} placeholders stored verbatim.
	assert.Equal(t, "${payload.source}", ms[0].Invoke.Env["CTXT_BRIDGE_SOURCE"])
}

func TestLoad_MissingDirReturnsEmpty(t *testing.T) {
	ms, err := Load(filepath.Join(t.TempDir(), "no-such-dir"))
	require.NoError(t, err, "missing dir is not an error — fresh installs hit this")
	assert.Empty(t, ms)
}

func TestLoad_EmptyDirReturnsEmpty(t *testing.T) {
	ms, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, ms)
}

func TestManifest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string
	}{
		{
			name:    "missing name",
			mutate:  func(m *Manifest) { m.Name = "" },
			wantErr: "name",
		},
		{
			name:    "missing version",
			mutate:  func(m *Manifest) { m.Version = "" },
			wantErr: "version",
		},
		{
			name:    "missing binary",
			mutate:  func(m *Manifest) { m.Binary = "" },
			wantErr: "binary",
		},
		{
			name:    "unknown mode",
			mutate:  func(m *Manifest) { m.Mode = "weird" },
			wantErr: "mode",
		},
		{
			name:    "empty accepts",
			mutate:  func(m *Manifest) { m.Accepts = nil },
			wantErr: "accepts",
		},
		{
			name: "unknown accept kind",
			mutate: func(m *Manifest) {
				m.Accepts[0].Kind = "video"
			},
			wantErr: "kind",
		},
		{
			name: "negative priority",
			mutate: func(m *Manifest) {
				m.Accepts[0].Priority = -1
			},
			wantErr: "priority",
		},
		{
			name: "url with no schemes",
			mutate: func(m *Manifest) {
				m.Accepts = []AcceptRule{
					{Kind: KindURL, Priority: 5},
				}
			},
			wantErr: "schemes",
		},
		{
			name: "text with empty mime",
			mutate: func(m *Manifest) {
				m.Accepts = []AcceptRule{
					{Kind: KindText, Priority: 5},
				}
			},
			wantErr: "mime",
		},
		{
			name: "file with empty mime",
			mutate: func(m *Manifest) {
				m.Accepts = []AcceptRule{
					{Kind: KindFile, Priority: 5},
				}
			},
			wantErr: "mime",
		},
		{
			name: "blob with empty mime",
			mutate: func(m *Manifest) {
				m.Accepts = []AcceptRule{
					{Kind: KindBlob, Priority: 5},
				}
			},
			wantErr: "mime",
		},
		{
			name: "negative max_size",
			mutate: func(m *Manifest) {
				m.Accepts[1].MaxSize = -1
			},
			wantErr: "max_size",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validBaseManifest()
			tc.mutate(m)
			err := m.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestManifest_Validate_AcceptsAllModes(t *testing.T) {
	for _, mode := range []Mode{ModeSubprocess, ModeHTTP, ModeSocket, ModeInproc} {
		t.Run(string(mode), func(t *testing.T) {
			m := validBaseManifest()
			m.Mode = mode
			require.NoError(t, m.Validate())
		})
	}
}

func TestManifest_Validate_BaseIsValid(t *testing.T) {
	require.NoError(t, validBaseManifest().Validate())
}
