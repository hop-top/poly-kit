package sqlite_test

// Cross-language storage-binding gate.
//
// Neither language's ordinary suite can catch a key-affinity mismatch by
// construction: the Go tests round-trip Go-to-Go and the Rust tests
// Rust-to-Rust, so a store that binds keys as BLOB on both sides of its
// own tests passes every one of them. This file crosses the boundary for
// real — Go writes a SQLite file, the Rust store reads it back, and vice
// versa — against the shared corpus in contracts/kv-v1/keys.json.
//
// The Rust half runs as a subprocess through `cargo test`. Set
// KV_CROSSLANG=1 to enable; without it the test skips, so the default
// `go test ./...` does not require a Rust toolchain.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/sqlite"
	"hop.top/kit/go/storage/sqldb"
)

type kvCase struct {
	Name     string `json:"name"`
	KeyHex   string `json:"key_hex"`
	ValueHex string `json:"value_hex"`
	Note     string `json:"note"`
}

type kvPrefixScan struct {
	Name      string   `json:"name"`
	PrefixHex string   `json:"prefix_hex"`
	ExpectHex []string `json:"expect_hex"`
	Note      string   `json:"note"`
}

type kvContract struct {
	Version     string         `json:"version"`
	Cases       []kvCase       `json:"cases"`
	ListOrder   []string       `json:"list_order"`
	PrefixScans []kvPrefixScan `json:"prefix_scans"`
}

// repoRoot walks up from the test's working directory to the tree that
// holds contracts/, so the fixture resolves regardless of where `go test`
// was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "kv-v1", "keys.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("contracts/kv-v1/keys.json: not found walking up from working directory")
	return ""
}

func loadKVContract(t *testing.T) (kvContract, string) {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "kv-v1", "keys.json"))
	require.NoError(t, err)
	var c kvContract
	require.NoError(t, json.Unmarshal(raw, &c))
	require.NotEmpty(t, c.Cases)
	return c, root
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// writeCorpus writes every fixture case into the store at path.
func writeCorpus(t *testing.T, path string, c kvContract) {
	t.Helper()
	s, err := sqlite.New(path)
	require.NoError(t, err)
	defer s.Close()

	ctx := context.Background()
	for _, tc := range c.Cases {
		key := string(mustHex(t, tc.KeyHex))
		val := mustHex(t, tc.ValueHex)
		require.NoErrorf(t, s.Put(ctx, key, val), "put %s", tc.Name)
	}
}

// readCorpus asserts the store at path holds exactly the fixture corpus.
func readCorpus(t *testing.T, path string, c kvContract) {
	t.Helper()
	s, err := sqlite.New(path)
	require.NoError(t, err)
	defer s.Close()

	ctx := context.Background()

	for _, tc := range c.Cases {
		key := string(mustHex(t, tc.KeyHex))
		want := mustHex(t, tc.ValueHex)

		got, ok, err := s.Get(ctx, key)
		require.NoErrorf(t, err, "get %s", tc.Name)
		require.Truef(t, ok, "get %s (%q): key missing — a storage-class mismatch reads as a silent miss", tc.Name, tc.KeyHex)
		// A zero-length BLOB scans back as a nil slice, so compare
		// contents rather than nil-ness: present-but-empty is the
		// contract, and `ok` above already distinguishes it from absent.
		assert.Truef(t, bytes.Equal(want, got),
			"value for %s: want %x, got %x", tc.Name, want, got)
	}

	// List issues no ORDER BY, so compare enumeration as a set. The
	// collation order itself is pinned separately, against an explicitly
	// ordered query, in TestCrossLang_BinaryCollationOrder.
	for _, ps := range c.PrefixScans {
		keys, err := s.List(ctx, string(mustHex(t, ps.PrefixHex)))
		require.NoErrorf(t, err, "list %s", ps.Name)
		gotHex := make([]string, 0, len(keys))
		for _, k := range keys {
			gotHex = append(gotHex, hex.EncodeToString([]byte(k)))
		}
		assert.ElementsMatchf(t, ps.ExpectHex, gotHex,
			"prefix scan %s: %s", ps.Name, ps.Note)
	}
}

// sortedKeyHex returns every key in the store at path under an explicit
// `ORDER BY key`, hex-encoded.
func sortedKeyHex(t *testing.T, path string) []string {
	t.Helper()
	db, err := sqldb.Open(sqldb.Options{Path: path})
	require.NoError(t, err)
	defer db.Close()

	rows, err := db.Query(`SELECT key FROM kv ORDER BY key`)
	require.NoError(t, err)
	defer rows.Close()

	var got []string
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		got = append(got, hex.EncodeToString([]byte(k)))
	}
	require.NoError(t, rows.Err())
	return got
}

// TestCrossLang_GoWritesGoReads is the control. It shares the corpus and
// assertions with the cross-language cases but never leaves Go, so a
// failure here means the fixture or the Go store is wrong, not that the
// two languages disagree.
func TestCrossLang_GoWritesGoReads(t *testing.T) {
	c, _ := loadKVContract(t)
	path := filepath.Join(t.TempDir(), "kv.db")
	writeCorpus(t, path, c)
	readCorpus(t, path, c)
}

// TestCrossLang_BinaryCollationOrder proves the assumption prefix scans
// rest on: under a TEXT key column, SQLite's default BINARY collation is
// still memcmp over the stored bytes, not a UTF-8-aware or locale
// comparison. If that failed, `key >= ? AND key < ?` would select the
// wrong rows for every non-ASCII key even with the affinity fixed.
func TestCrossLang_BinaryCollationOrder(t *testing.T) {
	c, _ := loadKVContract(t)
	path := filepath.Join(t.TempDir(), "kv.db")
	writeCorpus(t, path, c)

	assert.Equal(t, c.ListOrder, sortedKeyHex(t, path),
		"ORDER BY key must equal byte-wise (memcmp) order over the corpus")

	// Independently derive the same expectation, so the fixture cannot
	// silently drift into agreeing with a wrong implementation.
	want := make([]string, 0, len(c.Cases))
	for _, tc := range c.Cases {
		want = append(want, tc.KeyHex)
	}
	sort.Slice(want, func(i, j int) bool {
		return bytes.Compare(mustHex(t, want[i]), mustHex(t, want[j])) < 0
	})
	assert.Equal(t, want, c.ListOrder,
		"fixture list_order must be the corpus sorted by bytes.Compare")
}

// TestCrossLang_KeyColumnIsText pins the storage class itself. The corpus
// round-trips fine within one language whichever affinity is used; only
// the declared type keeps the other language's binding compatible.
func TestCrossLang_KeyColumnIsText(t *testing.T) {
	c, _ := loadKVContract(t)
	path := filepath.Join(t.TempDir(), "kv.db")
	writeCorpus(t, path, c)

	db, err := sqldb.Open(sqldb.Options{Path: path})
	require.NoError(t, err)
	defer db.Close()

	rows, err := db.Query(`SELECT DISTINCT typeof(key) FROM kv`)
	require.NoError(t, err)
	defer rows.Close()

	var types []string
	for rows.Next() {
		var ty string
		require.NoError(t, rows.Scan(&ty))
		types = append(types, ty)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"text"}, types,
		"every kv key must be stored with SQLite storage class TEXT")
}

// runRustKV drives the Rust cross-language harness over the same file.
func runRustKV(t *testing.T, root, mode, path string) {
	t.Helper()
	if os.Getenv("KV_CROSSLANG") == "" {
		t.Skip("set KV_CROSSLANG=1 to run the Go/Rust cross-language gate")
	}
	cmd := exec.Command("cargo", "test",
		"--features", "kv", "--test", "kv_crosslang",
		"--", "--exact", "--nocapture", "harness_"+mode)
	cmd.Dir = filepath.Join(root, "sdk", "experimental", "rs")
	cmd.Env = append(os.Environ(), "KV_CROSSLANG_DB="+path)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "rust harness_%s failed:\n%s", mode, out)
}

// TestCrossLang_GoWritesRustReads is the gate this fix exists for: Go
// writes the corpus, the Rust store opens the same file and must find
// every key with the exact value bytes and the same enumeration order.
func TestCrossLang_GoWritesRustReads(t *testing.T) {
	c, root := loadKVContract(t)
	path := filepath.Join(t.TempDir(), "kv.db")
	writeCorpus(t, path, c)
	runRustKV(t, root, "read", path)
}

// TestCrossLang_RustWritesGoReads is the same gate in the other
// direction, and the one that catches a shadow row: if Rust wrote the
// corpus under a different storage class, Go's reads would miss it.
func TestCrossLang_RustWritesGoReads(t *testing.T) {
	c, root := loadKVContract(t)
	path := filepath.Join(t.TempDir(), "kv.db")
	runRustKV(t, root, "write", path)
	readCorpus(t, path, c)
}
