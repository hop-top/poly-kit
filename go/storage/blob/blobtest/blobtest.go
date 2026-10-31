// Package blobtest is the shared conformance suite for blob.Store
// providers. Every provider's tests call Run with a factory for a fresh,
// empty store; the suite pins the behavior callers may rely on across
// backends.
package blobtest

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strings"
	"testing"

	"hop.top/kit/go/storage/blob"
)

// Factory returns an empty store isolated from other calls.
type Factory func(t *testing.T) blob.Store

// Run executes the conformance suite against stores built by newStore.
//
// Pinned contract:
//   - Delete of a missing key succeeds (idempotent, S3 semantics).
//   - Exists reports false, without error, for a missing key.
//   - Get of a missing key returns an error.
//   - Put overwrites; Get returns the latest bytes.
//   - List returns every key with the prefix (a string-prefix match, so
//     "logs/" excludes "logs-old/x"), with its Size, as the keys the
//     caller wrote.
//   - Object.ContentType from List is never wrong: empty (the provider
//     cannot report it, e.g. S3 ListObjectsV2) or exactly the type of the
//     latest Put. A stale type from an overwritten or deleted key never
//     leaks. Providers that can report it also run RunContentType.
func Run(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("DeleteMissingKeySucceeds", func(t *testing.T) {
		s := newStore(t)
		if err := s.Delete(ctx, "never/existed.txt"); err != nil {
			t.Fatalf("Delete of missing key: %v, want nil", err)
		}
	})

	t.Run("DeleteTwiceSucceeds", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "d.txt", "x")
		if err := s.Delete(ctx, "d.txt"); err != nil {
			t.Fatalf("first Delete: %v", err)
		}
		if err := s.Delete(ctx, "d.txt"); err != nil {
			t.Fatalf("second Delete: %v, want nil", err)
		}
		if ok, err := s.Exists(ctx, "d.txt"); err != nil || ok {
			t.Fatalf("Exists after delete = %v, %v", ok, err)
		}
	})

	t.Run("ExistsMissing", func(t *testing.T) {
		s := newStore(t)
		ok, err := s.Exists(ctx, "nope")
		if err != nil || ok {
			t.Fatalf("Exists(missing) = %v, %v; want false, nil", ok, err)
		}
	})

	t.Run("GetMissingErrors", func(t *testing.T) {
		s := newStore(t)
		rc, err := s.Get(ctx, "nope")
		if err == nil {
			rc.Close()
			t.Fatal("Get(missing): want error")
		}
	})

	t.Run("PutGetOverwrite", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "a/b.txt", "one")
		put(t, s, "a/b.txt", "two!")
		rc, err := s.Get(ctx, "a/b.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		got, _ := io.ReadAll(rc)
		if string(got) != "two!" {
			t.Fatalf("got %q, want latest", got)
		}
	})

	t.Run("ListContentTypeNeverWrong", func(t *testing.T) {
		s := newStore(t)
		putCT(t, s, "ct/a.json", "{}", "application/json")
		putCT(t, s, "ct/b.bin", "x", "")
		putCT(t, s, "ct/over", "1", "text/plain")
		putCT(t, s, "ct/over", "2", "text/csv")
		putCT(t, s, "ct/gone", "1", "image/png")
		if err := s.Delete(ctx, "ct/gone"); err != nil {
			t.Fatal(err)
		}
		putCT(t, s, "ct/gone", "1", "")
		for k, want := range wantTypes {
			got := listTypes(t, s)[k]
			if got != "" && got != want {
				t.Fatalf("List ContentType[%s] = %q, want %q or empty", k, got, want)
			}
		}
	})

	t.Run("ListPrefixKeysAndSizes", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "logs/a.log", "1")
		put(t, s, "logs/sub/b.log", "22")
		put(t, s, "data/x.bin", "333")
		put(t, s, "logs-old/c.log", "4444") // shares "logs" but not "logs/"
		objs, err := s.List(ctx, "logs/")
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key < objs[j].Key })
		var keys []string
		sizes := map[string]int64{}
		for _, o := range objs {
			keys = append(keys, o.Key)
			sizes[o.Key] = o.Size
		}
		if strings.Join(keys, ",") != "logs/a.log,logs/sub/b.log" {
			t.Fatalf("keys = %v", keys)
		}
		if sizes["logs/a.log"] != 1 || sizes["logs/sub/b.log"] != 2 {
			t.Fatalf("sizes = %v", sizes)
		}
	})
}

func put(t *testing.T, s blob.Store, key, body string) {
	t.Helper()
	if err := s.Put(context.Background(), key, bytes.NewReader([]byte(body)), "text/plain"); err != nil {
		t.Fatalf("Put %s: %v", key, err)
	}
}

var wantTypes = map[string]string{
	"ct/a.json": "application/json",
	"ct/b.bin":  "",
	"ct/over":   "text/csv",
	"ct/gone":   "",
}

// RunContentType pins the strict contract for providers that persist the
// content type: List reports exactly the type of the latest Put, empty
// when none was given, and the type does not outlive its key.
func RunContentType(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("ContentTypeRoundTrip", func(t *testing.T) {
		s := newStore(t)
		putCT(t, s, "ct/a.json", "{}", "application/json")
		putCT(t, s, "ct/b.bin", "x", "")
		putCT(t, s, "ct/over", "1", "text/plain")
		putCT(t, s, "ct/over", "2", "text/csv")
		putCT(t, s, "ct/gone", "1", "image/png")
		if err := s.Delete(ctx, "ct/gone"); err != nil {
			t.Fatal(err)
		}
		putCT(t, s, "ct/gone", "1", "")
		got := listTypes(t, s)
		for k, want := range wantTypes {
			if got[k] != want {
				t.Errorf("ContentType[%s] = %q, want %q", k, got[k], want)
			}
		}
	})

	t.Run("ContentTypeBytesUnchanged", func(t *testing.T) {
		s := newStore(t)
		putCT(t, s, "k", "payload", "text/plain")
		rc, err := s.Get(ctx, "k")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		if b, _ := io.ReadAll(rc); string(b) != "payload" {
			t.Fatalf("Get = %q", b)
		}
	})

	t.Run("ContentTypeNotListedAsKey", func(t *testing.T) {
		s := newStore(t)
		putCT(t, s, "k", "p", "text/plain")
		objs, err := s.List(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(objs) != 1 || objs[0].Key != "k" {
			t.Fatalf("List = %+v, want only k", objs)
		}
	})
}

func putCT(t *testing.T, s blob.Store, key, body, ct string) {
	t.Helper()
	if err := s.Put(context.Background(), key, bytes.NewReader([]byte(body)), ct); err != nil {
		t.Fatalf("Put %s: %v", key, err)
	}
}

func listTypes(t *testing.T, s blob.Store) map[string]string {
	t.Helper()
	objs, err := s.List(context.Background(), "ct/")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, o := range objs {
		m[o.Key] = o.ContentType
	}
	return m
}
