package composite_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/composite"
	"hop.top/kit/go/storage/secret/memory"
)

// roStore wraps a MutableStore and rejects writes — used to simulate a
// genuinely read-only backend (env-like), separate from the RO routing
// flag on Member.
type roStore struct{ inner secret.MutableStore }

func (r *roStore) Get(ctx context.Context, k string) (*secret.Secret, error) {
	return r.inner.Get(ctx, k)
}
func (r *roStore) List(ctx context.Context, p string) ([]string, error) {
	return r.inner.List(ctx, p)
}
func (r *roStore) Exists(ctx context.Context, k string) (bool, error) {
	return r.inner.Exists(ctx, k)
}
func (r *roStore) Set(context.Context, string, []byte) error {
	return errors.New("roStore: read-only")
}
func (r *roStore) Delete(context.Context, string) error {
	return errors.New("roStore: read-only")
}

// metaStore wraps memory.Store and supplies StoredMeta from a side map.
type metaStore struct {
	*memory.Store
	meta map[string]secret.StoredMeta
	err  error // override: if set, Metadata returns this for every key
}

func (m *metaStore) Metadata(_ context.Context, k string) (secret.StoredMeta, error) {
	if m.err != nil {
		return secret.StoredMeta{}, m.err
	}
	v, ok := m.meta[k]
	if !ok {
		return secret.StoredMeta{}, secret.ErrNotFound
	}
	return v, nil
}

func mustSet(t *testing.T, s secret.MutableStore, k string, v string) {
	t.Helper()
	if err := s.Set(context.Background(), k, []byte(v)); err != nil {
		t.Fatalf("set %q: %v", k, err)
	}
}

func TestRoutingByOwnership(t *testing.T) {
	ctx := context.Background()
	ci := memory.New()
	dev := memory.New()

	store := composite.New(
		composite.Member{Name: "ci", Store: ci, Owns: composite.HasPrefix("ci/")},
		composite.Member{Name: "dev", Store: dev, Owns: composite.HasPrefix("dev/")},
	)

	if err := store.Set(ctx, "ci/token", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "dev/token", []byte("b")); err != nil {
		t.Fatal(err)
	}

	// Each value must live in exactly its owner — not the other one.
	if v, err := ci.Get(ctx, "ci/token"); err != nil || string(v.Value) != "a" {
		t.Fatalf("ci/token in ci: %v %v", v, err)
	}
	if _, err := dev.Get(ctx, "ci/token"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("ci/token should not be in dev: %v", err)
	}
	if v, err := dev.Get(ctx, "dev/token"); err != nil || string(v.Value) != "b" {
		t.Fatalf("dev/token in dev: %v %v", v, err)
	}
	if _, err := ci.Get(ctx, "dev/token"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("dev/token should not be in ci: %v", err)
	}
}

func TestSetNoWriter(t *testing.T) {
	ctx := context.Background()
	ci := memory.New()

	store := composite.New(
		composite.Member{Name: "ci", Store: ci, Owns: composite.HasPrefix("ci/")},
	)

	err := store.Set(ctx, "other/key", []byte("x"))
	if !errors.Is(err, composite.ErrNoWriter) {
		t.Fatalf("expected ErrNoWriter, got %v", err)
	}
}

func TestSetROMemberRejected(t *testing.T) {
	ctx := context.Background()
	ro := memory.New()
	store := composite.New(
		composite.Member{Name: "ro", Store: ro, RO: true}, // catch-all, RO
	)

	err := store.Set(ctx, "anything", []byte("x"))
	if !errors.Is(err, composite.ErrNoWriter) {
		t.Fatalf("expected ErrNoWriter on RO member, got %v", err)
	}
}

func TestSetFirstWritableOwnerWins(t *testing.T) {
	// Both members own the key; first non-RO wins.
	ctx := context.Background()
	a := memory.New()
	b := memory.New()
	store := composite.New(
		composite.Member{Name: "a", Store: a}, // catch-all
		composite.Member{Name: "b", Store: b}, // catch-all
	)

	if err := store.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Get(ctx, "k"); err != nil {
		t.Fatalf("a should have k: %v", err)
	}
	if _, err := b.Get(ctx, "k"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("b should not have k: %v", err)
	}
}

func TestSetSkipsROToReachWritable(t *testing.T) {
	ctx := context.Background()
	ro := memory.New()
	rw := memory.New()
	store := composite.New(
		composite.Member{Name: "ro", Store: ro, RO: true}, // matches first
		composite.Member{Name: "rw", Store: rw},
	)

	if err := store.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Get(ctx, "k"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("RO member must not be written: %v", err)
	}
	if _, err := rw.Get(ctx, "k"); err != nil {
		t.Fatalf("RW member should have k: %v", err)
	}
}

func TestReadFallbackToNonOwner(t *testing.T) {
	// Owner doesn't have the key; reader falls through to a non-owning
	// (catch-all RO) member.
	ctx := context.Background()
	owner := memory.New() // owns "app/*" but empty
	fallback := memory.New()
	mustSet(t, fallback, "app/secret", "from-fallback")

	store := composite.New(
		composite.Member{Name: "owner", Store: owner, Owns: composite.HasPrefix("app/")},
		composite.Member{Name: "fb", Store: fallback, RO: true}, // catch-all
	)

	got, err := store.Get(ctx, "app/secret")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Value) != "from-fallback" {
		t.Fatalf("got %q", got.Value)
	}
}

func TestReadOwnerWinsOverFallback(t *testing.T) {
	ctx := context.Background()
	owner := memory.New()
	fallback := memory.New()
	mustSet(t, owner, "app/secret", "from-owner")
	mustSet(t, fallback, "app/secret", "from-fallback")

	store := composite.New(
		composite.Member{Name: "owner", Store: owner, Owns: composite.HasPrefix("app/")},
		composite.Member{Name: "fb", Store: fallback, RO: true},
	)

	got, err := store.Get(ctx, "app/secret")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "from-owner" {
		t.Fatalf("got %q, want from-owner", got.Value)
	}
}

func TestExistsReports(t *testing.T) {
	ctx := context.Background()
	a := memory.New()
	b := memory.New()
	mustSet(t, b, "k", "v") // only b has it

	store := composite.New(
		composite.Member{Name: "a", Store: a, Owns: composite.HasPrefix("a/")},
		composite.Member{Name: "b", Store: b}, // catch-all
	)

	ok, err := store.Exists(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("exists: %v %v", ok, err)
	}

	ok, err = store.Exists(ctx, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false for absent key")
	}
}

func TestListUnionDedupedSorted(t *testing.T) {
	ctx := context.Background()
	a := memory.New()
	b := memory.New()
	mustSet(t, a, "app/x", "1")
	mustSet(t, a, "app/y", "2")
	mustSet(t, b, "app/y", "dup") // overlaps
	mustSet(t, b, "app/z", "3")
	mustSet(t, b, "other/q", "4")

	store := composite.New(
		composite.Member{Name: "a", Store: a},
		composite.Member{Name: "b", Store: b},
	)

	keys, err := store.List(ctx, "app/")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app/x", "app/y", "app/z"}
	if fmt.Sprintf("%v", keys) != fmt.Sprintf("%v", want) {
		t.Fatalf("got %v, want %v", keys, want)
	}
}

func TestDeleteFromOwner(t *testing.T) {
	ctx := context.Background()
	owner := memory.New()
	other := memory.New()
	mustSet(t, owner, "app/k", "v")
	mustSet(t, other, "app/k", "shadow") // not the owner, must remain

	store := composite.New(
		composite.Member{Name: "owner", Store: owner, Owns: composite.HasPrefix("app/")},
		composite.Member{Name: "other", Store: other, RO: true},
	)

	if err := store.Delete(ctx, "app/k"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Get(ctx, "app/k"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("owner should no longer have k: %v", err)
	}
	if _, err := other.Get(ctx, "app/k"); err != nil {
		t.Fatalf("non-owner must be untouched: %v", err)
	}
}

func TestDeleteNoWriter(t *testing.T) {
	ctx := context.Background()
	ro := memory.New()
	mustSet(t, ro, "k", "v")
	store := composite.New(
		composite.Member{Name: "ro", Store: ro, RO: true},
	)
	err := store.Delete(ctx, "k")
	if !errors.Is(err, composite.ErrNoWriter) {
		t.Fatalf("expected ErrNoWriter, got %v", err)
	}
}

func TestDeleteWritableOwnerMissingKey(t *testing.T) {
	ctx := context.Background()
	owner := memory.New() // empty
	store := composite.New(
		composite.Member{Name: "owner", Store: owner, Owns: composite.HasPrefix("app/")},
	)
	err := store.Delete(ctx, "app/missing")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMintRoutesToOwner(t *testing.T) {
	ctx := context.Background()
	ci := memory.New()
	dev := memory.New()
	store := composite.New(
		composite.Member{Name: "ci", Store: ci, Owns: composite.HasPrefix("ci/")},
		composite.Member{Name: "dev", Store: dev, Owns: composite.HasPrefix("dev/")},
	)

	tok, err := secret.Mint(ctx, store, "ci/session", 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 32 {
		t.Fatalf("expected 32 hex chars, got %d", len(tok))
	}
	if _, err := ci.Get(ctx, "ci/session"); err != nil {
		t.Fatalf("minted secret should be in ci: %v", err)
	}
	if _, err := dev.Get(ctx, "ci/session"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("minted secret must not leak to dev: %v", err)
	}
}

func TestMetadataSourceRewritten(t *testing.T) {
	ctx := context.Background()
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	owner := &metaStore{
		Store: memory.New(),
		meta: map[string]secret.StoredMeta{
			"app/k": {Key: "app/k", Source: "agefile//tmp/x", Backend: "agefile", UpdatedAt: when},
		},
	}
	mustSet(t, owner, "app/k", "v")

	store := composite.New(
		composite.Member{Name: "primary", Store: owner, Owns: composite.HasPrefix("app/")},
	)

	meta, err := store.Metadata(ctx, "app/k")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Source != "composite/primary" {
		t.Fatalf("Source = %q, want composite/primary", meta.Source)
	}
	if meta.Backend != "agefile" {
		t.Fatalf("Backend lost: %q", meta.Backend)
	}
}

func TestMetadataSkipsUnsupported(t *testing.T) {
	ctx := context.Background()
	// First member has no MetadataReader (plain memory.New returns
	// ErrNotSupported via its Metadata method); second member supplies
	// the answer.
	first := memory.New()
	mustSet(t, first, "k", "v")
	second := &metaStore{
		Store: memory.New(),
		meta:  map[string]secret.StoredMeta{"k": {Key: "k", Source: "x"}},
	}
	mustSet(t, second, "k", "v")

	store := composite.New(
		composite.Member{Name: "first", Store: first},
		composite.Member{Name: "second", Store: second},
	)

	meta, err := store.Metadata(ctx, "k")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Source != "composite/second" {
		t.Fatalf("Source = %q, want composite/second", meta.Source)
	}
}

func TestMetadataAllNotSupported(t *testing.T) {
	ctx := context.Background()
	store := composite.New(
		composite.Member{Name: "a", Store: memory.New()},
		composite.Member{Name: "b", Store: memory.New()},
	)
	_, err := store.Metadata(ctx, "k")
	if !errors.Is(err, secret.ErrNotSupported) {
		t.Fatalf("expected ErrNotSupported, got %v", err)
	}
}

func TestPanicOnEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty Name")
		}
	}()
	composite.New(composite.Member{Store: memory.New()})
}

func TestPanicOnNilStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil Store")
		}
	}()
	composite.New(composite.Member{Name: "x"})
}

// Verify roStore is wired correctly into composite — writes attempted
// against a writable Member whose underlying Store rejects them must
// surface that error rather than silently failing.
func TestUnderlyingWriteErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	store := composite.New(
		composite.Member{Name: "ro", Store: &roStore{inner: memory.New()}},
	)
	err := store.Set(ctx, "k", []byte("v"))
	if err == nil {
		t.Fatal("expected error from underlying store")
	}
	if errors.Is(err, composite.ErrNoWriter) {
		t.Fatalf("should not be ErrNoWriter: %v", err)
	}
}

// --- predicate helpers ---

func TestHasPrefix(t *testing.T) {
	p := composite.HasPrefix("ci/")
	if !p("ci/x") || p("dev/x") {
		t.Fatal()
	}
}

func TestHasSuffix(t *testing.T) {
	p := composite.HasSuffix(".pem")
	if !p("key.pem") || p("key.txt") {
		t.Fatal()
	}
}

func TestAnyOf(t *testing.T) {
	p := composite.AnyOf("a", "b")
	if !p("a") || !p("b") || p("c") {
		t.Fatal()
	}
}

func TestMatchRegexp(t *testing.T) {
	p := composite.MatchRegexp(regexp.MustCompile(`^v\d+/`))
	if !p("v1/x") || p("x") {
		t.Fatal()
	}
}

func TestOrAndNot(t *testing.T) {
	p := composite.Or(composite.HasPrefix("ci/"), composite.HasPrefix("dev/"))
	if !p("ci/x") || !p("dev/x") || p("prod/x") {
		t.Fatal()
	}

	q := composite.And(composite.HasPrefix("app/"), composite.HasSuffix(".pem"))
	if !q("app/x.pem") || q("app/x.txt") || q("other/x.pem") {
		t.Fatal()
	}

	r := composite.Not(composite.HasPrefix("ci/"))
	if r("ci/x") || !r("dev/x") {
		t.Fatal()
	}
}

func TestOrWithNilIsCatchAll(t *testing.T) {
	p := composite.Or(composite.HasPrefix("ci/"), nil)
	if !p("anything") {
		t.Fatal()
	}
}

func TestNotNilMatchesNothing(t *testing.T) {
	p := composite.Not(nil)
	if p("anything") {
		t.Fatal()
	}
}
