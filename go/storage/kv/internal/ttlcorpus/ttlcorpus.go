// Package ttlcorpus replays contracts/kv-v1/ttl.json against any
// kv.TTLInserter, so the memory, sqlite and badger backends share one
// definition of put-if-absent-with-TTL with the Rust SqliteStore.
package ttlcorpus

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv"
)

type step struct {
	Op       string  `json:"op"`
	KeyHex   string  `json:"key_hex"`
	ValueHex *string `json:"value_hex"`
	TTLMs    int64   `json:"ttl_ms"`
	Inserted bool    `json:"inserted"`
	Ms       int64   `json:"ms"`
}

type scenario struct {
	Name  string `json:"name"`
	Steps []step `json:"steps"`
}

type corpus struct {
	Version   string     `json:"version"`
	Scenarios []scenario `json:"scenarios"`
}

func load(t *testing.T) corpus {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for range 10 {
		p := filepath.Join(dir, "contracts", "kv-v1", "ttl.json")
		if raw, err := os.ReadFile(p); err == nil {
			var c corpus
			require.NoError(t, json.Unmarshal(raw, &c))
			require.Equal(t, "v1", c.Version)
			require.NotEmpty(t, c.Scenarios)
			return c
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("contracts/kv-v1/ttl.json: not found walking up from working directory")
	return corpus{}
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// Run replays every scenario, each against a fresh store from open.
func Run(t *testing.T, open func(t *testing.T) kv.TTLInserter) {
	t.Helper()
	ctx := context.Background()
	for _, sc := range load(t).Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			s := open(t)
			for i, st := range sc.Steps {
				key := string(unhex(t, st.KeyHex))
				switch st.Op {
				case "put_if_absent":
					got, err := s.PutIfAbsentWithTTL(ctx, key, unhex(t, *st.ValueHex),
						time.Duration(st.TTLMs)*time.Millisecond)
					require.NoError(t, err, "step %d", i)
					assert.Equal(t, st.Inserted, got, "step %d inserted", i)
				case "put":
					require.NoError(t, s.Put(ctx, key, unhex(t, *st.ValueHex)), "step %d", i)
				case "sleep_ms":
					time.Sleep(time.Duration(st.Ms) * time.Millisecond)
				case "get":
					val, ok, err := s.Get(ctx, key)
					require.NoError(t, err, "step %d", i)
					if st.ValueHex == nil {
						assert.False(t, ok, "step %d: want absent", i)
						continue
					}
					require.True(t, ok, "step %d: want present", i)
					assert.True(t, bytes.Equal(unhex(t, *st.ValueHex), val), "step %d value", i)
				default:
					t.Fatalf("step %d: unknown op %q", i, st.Op)
				}
			}
		})
	}
}
