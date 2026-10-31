package blob_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"

	"hop.top/kit/go/storage/blob"
)

// listOnly is a minimal Store without Stat.
type listOnly struct{ objs []blob.Object }

func (listOnly) Put(context.Context, string, io.Reader, string) error { return nil }
func (listOnly) Get(context.Context, string) (io.ReadCloser, error)   { return nil, nil }
func (listOnly) Delete(context.Context, string) error                 { return nil }
func (listOnly) Exists(context.Context, string) (bool, error)         { return false, nil }
func (l listOnly) List(context.Context, string) ([]blob.Object, error) {
	return l.objs, nil
}

// withStat adds Stater.
type withStat struct {
	listOnly
	o blob.Object
}

func (w withStat) Stat(context.Context, string) (blob.Object, error) { return w.o, nil }

func TestStatHelperPrefersStater(t *testing.T) {
	s := withStat{o: blob.Object{Key: "k", Size: 1, ContentType: "a/b"}}
	got, err := blob.Stat(context.Background(), s, "k")
	if err != nil || got != s.o {
		t.Fatalf("Stat = %+v, %v", got, err)
	}
}

func TestStatHelperFallsBackToExactListMatch(t *testing.T) {
	s := listOnly{objs: []blob.Object{{Key: "k2", Size: 9}, {Key: "k", Size: 1}}}
	got, err := blob.Stat(context.Background(), s, "k")
	if err != nil || got.Key != "k" || got.Size != 1 {
		t.Fatalf("Stat = %+v, %v; want exact key k", got, err)
	}
}

func TestStatHelperFallbackNotFound(t *testing.T) {
	s := listOnly{objs: []blob.Object{{Key: "k2"}}}
	_, err := blob.Stat(context.Background(), s, "k")
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("err = %v, want blob.ErrNotFound", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist kept for compat", err)
	}
}
