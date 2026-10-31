package local_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/storage/blob/local"
)

// Runs the shared corpus in contracts/blob-v1. The vectors are the
// source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

const blobConfDir = "../../../../contracts/blob-v1"

// blobConfFiles is every vector file this runner consumes; the corpus
// directory must match it exactly.
var blobConfFiles = map[string]bool{"blob.json": true}

type blobObj struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

type blobErr struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type blobStep struct {
	Op          string     `json:"op"`
	Key         string     `json:"key"`
	Path        string     `json:"path"`
	Prefix      string     `json:"prefix"`
	Data        string     `json:"data"`
	DataHex     string     `json:"data_hex"`
	ContentType string     `json:"content_type"`
	WantData    *string    `json:"want_data"`
	WantHex     *string    `json:"want_hex"`
	WantBool    *bool      `json:"want_bool"`
	WantObjects *[]blobObj `json:"want_objects"`
	Error       *blobErr   `json:"error"`
}

type blobScenario struct {
	Name  string     `json:"name"`
	Steps []blobStep `json:"steps"`
}

type blobFile struct {
	Schema      string         `json:"$schema"`
	ID          string         `json:"$id"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Source      string         `json:"source"`
	Scenarios   []blobScenario `json:"scenarios"`
}

func loadBlobCorpus(t *testing.T) blobFile {
	t.Helper()
	entries, err := os.ReadDir(blobConfDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !blobConfFiles[e.Name()] {
			t.Fatalf("corpus file %s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(blobConfDir, "blob.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f blobFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode blob.json: %v", err)
	}
	if f.Version != "v1" {
		t.Fatalf("version %q, want v1", f.Version)
	}
	if f.Description == "" || len(f.Scenarios) == 0 {
		t.Fatal("corpus needs a description and scenarios")
	}
	return f
}

func blobPayload(t *testing.T, s blobStep) []byte {
	t.Helper()
	if s.DataHex != "" {
		b, err := hex.DecodeString(s.DataHex)
		if err != nil {
			t.Fatalf("data_hex: %v", err)
		}
		return b
	}
	return []byte(s.Data)
}

func checkBlobErr(t *testing.T, where string, want *blobErr, err error) bool {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", where, err)
		}
		return false
	}
	if err == nil {
		t.Fatalf("%s: want %s error, got none", where, want.Kind)
	}
	switch want.Kind {
	case "not_found":
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s: want not_found, got %v", where, err)
		}
	case "escapes_root":
		if err.Error() != want.Text {
			t.Fatalf("%s: error %q, want %q", where, err.Error(), want.Text)
		}
	default:
		t.Fatalf("%s: unknown error kind %q", where, want.Kind)
	}
	return true
}

func TestConformanceBlobScenarios(t *testing.T) {
	ctx := context.Background()
	for _, sc := range loadBlobCorpus(t).Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			root := t.TempDir()
			s, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			for i, st := range sc.Steps {
				where := sc.Name + "#" + st.Op
				switch st.Op {
				case "put":
					err := s.Put(ctx, st.Key, bytes.NewReader(blobPayload(t, st)), st.ContentType)
					checkBlobErr(t, where, st.Error, err)
				case "get":
					rc, err := s.Get(ctx, st.Key)
					if checkBlobErr(t, where, st.Error, err) {
						continue
					}
					got, err := io.ReadAll(rc)
					rc.Close()
					if err != nil {
						t.Fatal(err)
					}
					switch {
					case st.WantHex != nil:
						if hex.EncodeToString(got) != *st.WantHex {
							t.Fatalf("step %d get %q = %x, want %s", i, st.Key, got, *st.WantHex)
						}
					case st.WantData != nil:
						if string(got) != *st.WantData {
							t.Fatalf("step %d get %q = %q, want %q", i, st.Key, got, *st.WantData)
						}
					default:
						t.Fatalf("step %d: get needs want_data or want_hex", i)
					}
				case "delete":
					checkBlobErr(t, where, st.Error, s.Delete(ctx, st.Key))
				case "exists":
					ok, err := s.Exists(ctx, st.Key)
					if checkBlobErr(t, where, st.Error, err) {
						continue
					}
					if st.WantBool == nil || ok != *st.WantBool {
						t.Fatalf("step %d exists %q = %v, want %v", i, st.Key, ok, st.WantBool)
					}
				case "list":
					objs, err := s.List(ctx, st.Prefix)
					if checkBlobErr(t, where, st.Error, err) {
						continue
					}
					if st.WantObjects == nil {
						t.Fatalf("step %d: list needs want_objects", i)
					}
					got := make([]blobObj, 0, len(objs))
					for _, o := range objs {
						got = append(got, blobObj{Key: o.Key, Size: o.Size})
					}
					gj, _ := json.Marshal(got)
					wj, _ := json.Marshal(*st.WantObjects)
					if string(gj) != string(wj) {
						t.Fatalf("step %d list %q = %s, want %s", i, st.Prefix, gj, wj)
					}
				case "raw_write":
					p := filepath.Join(root, filepath.FromSlash(st.Path))
					if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte(st.Data), 0o640); err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatalf("step %d: unknown op %q", i, st.Op)
				}
			}
		})
	}
}
