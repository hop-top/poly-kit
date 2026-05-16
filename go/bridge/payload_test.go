package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPayload_JSON_RoundTrip(t *testing.T) {
	for _, tc := range positivePayloads() {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			require.NoError(t, err)

			var out Payload
			require.NoError(t, json.Unmarshal(data, &out))
			assert.Equal(t, tc.in, out)
		})
	}
}

func TestPayload_RejectsZeroOrMultipleKinds(t *testing.T) {
	for _, tc := range negativeKindRawJSON() {
		t.Run(tc.name, func(t *testing.T) {
			var p Payload
			err := json.Unmarshal([]byte(tc.raw), &p)
			assert.Error(t, err, "expected oneof guard to reject")
		})
	}
}
