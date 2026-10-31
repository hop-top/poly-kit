package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIContractWebSocket runs the websocket tables of
// contracts/parity/api.json: the constants and the topic matcher. The
// Rust port runs the same tables; Go is the authority.
func TestAPIContractWebSocket(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash("../../../contracts/parity/api.json"))
	require.NoError(t, err)
	var c struct {
		Constants struct {
			WebSocket struct {
				MaxMessageSize int64 `json:"default_max_message_size"`
			} `json:"websocket"`
		} `json:"constants"`
		Topics []struct {
			Pattern string `json:"pattern"`
			Topic   string `json:"topic"`
			Match   bool   `json:"match"`
		} `json:"websocket_topics"`
	}
	require.NoError(t, json.Unmarshal(raw, &c))
	require.NotEmpty(t, c.Topics)

	h := NewHub()
	assert.Equal(t, c.Constants.WebSocket.MaxMessageSize, h.maxMessageSize)
	for _, tt := range c.Topics {
		t.Run(tt.Pattern+" vs "+tt.Topic, func(t *testing.T) {
			assert.Equal(t, tt.Match, matchTopic(tt.Pattern, tt.Topic))
		})
	}
}
