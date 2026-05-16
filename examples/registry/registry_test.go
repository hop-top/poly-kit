package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/peer"
)

func TestCommandsExist(t *testing.T) {
	root := cli.New(cli.Config{
		Name:            "registry",
		Version:         "0.1.0",
		Short:           "test",
		DisableValidate: true,
	},
		cli.WithIdentity(cli.IdentityConfig{}),
		cli.WithPeers(cli.PeerConfig{
			Service:   mdnsService,
			Discovery: &peer.StaticDiscoverer{},
		}),
	)
	root.Cmd.AddCommand(announceCmd(root), browseCmd(root), inspectCmd(root), watchCmd(root))

	for _, name := range []string{"announce", "browse", "inspect", "watch"} {
		found := false
		for _, c := range root.Cmd.Commands() {
			if c.Name() == name {
				found = true
				break
			}
		}
		assert.True(t, found, "command %q should exist", name)
	}
}

func TestCapabilitySetSerialization(t *testing.T) {
	cs := toolspec.NewCapabilitySet("test-svc", "2.0.0")
	cs.Add(toolspec.Capability{Name: "users", Type: "crud"})
	cs.Add(toolspec.Capability{Name: "health", Type: "get"})

	data, err := cs.JSON()
	require.NoError(t, err)

	var parsed toolspec.CapabilitySet
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "test-svc", parsed.ServiceName)
	assert.Equal(t, "2.0.0", parsed.Version)
	assert.Len(t, parsed.Capabilities, 2)
	assert.Equal(t, "users", parsed.Capabilities[0].Name)
	assert.Equal(t, "crud", parsed.Capabilities[0].Type)
}

func TestCapabilitiesHandler(t *testing.T) {
	cs := toolspec.NewCapabilitySet("handler-test", "1.0.0")
	cs.Add(toolspec.Capability{Name: "notes", Type: "endpoint"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /capabilities", func(w http.ResponseWriter, _ *http.Request) {
		data, err := cs.JSON()
		if err != nil {
			http.Error(w, "failed to marshal capabilities", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/capabilities")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result toolspec.CapabilitySet
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)
	assert.Equal(t, "handler-test", result.ServiceName)
	assert.Len(t, result.Capabilities, 1)
}
