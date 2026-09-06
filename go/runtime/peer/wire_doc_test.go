package peer

import (
	"encoding/json"
	"testing"
	"time"
)

// TestTrustLevelWireValues pins the integer values documented in
// docs/adopters/reference/engine-security.md. TrustLevel has no custom
// marshaller, so its declaration order IS the wire contract and other
// language ports map by value. Reordering the constants is a silent
// breaking change for every port; this test makes it loud.
func TestTrustLevelWireValues(t *testing.T) {
	for _, tc := range []struct {
		level TrustLevel
		want  int
		name  string
	}{
		{Unknown, 0, "Unknown"},
		{Trusted, 1, "Trusted"},
		{Blocked, 2, "Blocked"},
		{PendingTOFU, 3, "PendingTOFU"},
	} {
		if int(tc.level) != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, int(tc.level), tc.want)
		}
	}
}

// TestPeerRecordJSONKeys pins the PeerRecord envelope. PeerInfo declares
// no JSON tags, so its fields serialize under Go names while PeerRecord
// contributes snake_case keys. Documented in engine-security.md; a port
// reading "id" instead of "ID" gets nothing.
func TestPeerRecordJSONKeys(t *testing.T) {
	rec := PeerRecord{
		PeerInfo: PeerInfo{
			ID:        "a1b2c3d4e5f67890",
			Name:      "engine-a",
			Addrs:     []string{"127.0.0.1:9090"},
			PublicKey: []byte("pem"),
			Metadata:  map[string]string{"k": "v"},
		},
		Trust:     Trusted,
		FirstSeen: time.Unix(0, 0).UTC(),
		LastSeen:  time.Unix(0, 0).UTC(),
	}

	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{
		"ID", "Name", "Addrs", "PublicKey", "Metadata", // untagged PeerInfo
		"trust", "first_seen", "last_seen", // tagged PeerRecord
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("PeerRecord JSON missing key %q; got keys %v", key, keysOf(got))
		}
	}

	// trust is a bare integer, not a name.
	if v, ok := got["trust"].(float64); !ok || int(v) != int(Trusted) {
		t.Errorf("trust = %v, want numeric %d", got["trust"], int(Trusted))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
