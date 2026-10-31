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
				MaxMessageSize   int64 `json:"default_max_message_size"`
				MaxSubscriptions int   `json:"max_subscriptions"`
				MaxPatternLen    int   `json:"max_pattern_len"`
				MaxSegments      int   `json:"max_pattern_segments"`
				MaxDoubleStars   int   `json:"max_pattern_double_stars"`
				MaxBytes         int   `json:"max_subscription_bytes"`
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
	ws := c.Constants.WebSocket
	assert.Equal(t, ws.MaxSubscriptions, maxSubscriptions)
	assert.Equal(t, ws.MaxPatternLen, maxPatternLen)
	assert.Equal(t, ws.MaxSegments, maxPatternSegments)
	assert.Equal(t, ws.MaxDoubleStars, maxPatternDoubleStars)
	assert.Equal(t, ws.MaxBytes, maxSubscriptionBytes)
	for _, tt := range c.Topics {
		t.Run(tt.Pattern+" vs "+tt.Topic, func(t *testing.T) {
			assert.Equal(t, tt.Match, matchTopic(tt.Pattern, tt.Topic))
		})
	}
}
