package memory_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/memory"
)

var _ kv.TTLStore = (*memory.Store)(nil)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestPutGetDelete(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	if err := s.Put(ctx, "a", []byte("1")); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.Get(ctx, "a")
	if err != nil || !ok || string(v) != "1" {
		t.Fatalf("Get = %q %v %v", v, ok, err)
	}
	// The returned slice is a copy: editing it leaves the store alone.
	v[0] = 'x'
	if v2, _, _ := s.Get(ctx, "a"); string(v2) != "1" {
		t.Fatalf("store shares its bytes: %q", v2)
	}
	if err := s.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(ctx, "a"); ok {
		t.Fatal("deleted key still present")
	}
}

func TestTTLExpiry(t *testing.T) {
	ctx := context.Background()
	c := &clock{t: time.Unix(1000, 0)}
	s := memory.New()
	s.SetClock(c.now)
	if err := s.PutWithTTL(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	c.add(59 * time.Second)
	if _, ok, _ := s.Get(ctx, "k"); !ok {
		t.Fatal("entry gone before its ttl")
	}
	c.add(time.Second)
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Fatal("entry still present at its ttl")
	}
	if keys, _ := s.List(ctx, ""); len(keys) != 0 {
		t.Fatalf("List shows expired keys: %v", keys)
	}
}

func TestEvictsLeastRecentlyUsed(t *testing.T) {
	ctx := context.Background()
	// Each entry costs 1 (key) + 4 (value) = 5 bytes; three fit in 15.
	s := memory.New(memory.WithMaxBytes(15))
	for _, k := range []string{"a", "b", "c"} {
		if err := s.Put(ctx, k, []byte("vvvv")); err != nil {
			t.Fatal(err)
		}
	}
	// Touch "a", so "b" is now the least recently used.
	if _, ok, _ := s.Get(ctx, "a"); !ok {
		t.Fatal("a missing")
	}
	if err := s.Put(ctx, "d", []byte("vvvv")); err != nil {
		t.Fatal(err)
	}
	keys, _ := s.List(ctx, "")
	if want := []string{"a", "c", "d"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func TestEvictsExpiredBeforeLive(t *testing.T) {
	ctx := context.Background()
	c := &clock{t: time.Unix(1000, 0)}
	s := memory.New(memory.WithMaxBytes(10))
	s.SetClock(c.now)
	_ = s.Put(ctx, "a", []byte("vvvv"))                     // oldest, live
	_ = s.PutWithTTL(ctx, "b", []byte("vvvv"), time.Second) // newer, expires
	c.add(2 * time.Second)
	if err := s.Put(ctx, "c", []byte("vvvv")); err != nil {
		t.Fatal(err)
	}
	keys, _ := s.List(ctx, "")
	if want := []string{"a", "c"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func TestRefusesEntryLargerThanBudget(t *testing.T) {
	ctx := context.Background()
	s := memory.New(memory.WithMaxBytes(8))
	_ = s.Put(ctx, "a", []byte("1"))
	err := s.Put(ctx, "big", []byte("0123456789"))
	if !errors.Is(err, memory.ErrValueTooLarge) {
		t.Fatalf("err = %v, want ErrValueTooLarge", err)
	}
	if _, ok, _ := s.Get(ctx, "a"); !ok {
		t.Fatal("a refused write evicted a live entry")
	}
}

func TestListPrefixSorted(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	for _, k := range []string{"x/2", "y/1", "x/1"} {
		_ = s.Put(ctx, k, nil)
	}
	keys, err := s.List(ctx, "x/")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"x/1", "x/2"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func TestClosed(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	_ = s.Put(ctx, "a", []byte("1"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, "a"); !errors.Is(err, memory.ErrClosed) {
		t.Fatalf("Get after Close: %v", err)
	}
	if err := s.Put(ctx, "a", nil); !errors.Is(err, memory.ErrClosed) {
		t.Fatalf("Put after Close: %v", err)
	}
}

func TestRegisteredAsMemory(t *testing.T) {
	st, err := kv.OpenContext(context.Background(), kv.Config{Backend: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok := st.(kv.TTLStore); !ok {
		t.Fatal("memory backend is not a TTLStore")
	}
}

func TestConcurrentUse(t *testing.T) {
	ctx := context.Background()
	s := memory.New(memory.WithMaxBytes(256))
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 200 {
				k := string(rune('a' + (i+j)%16))
				if err := s.PutWithTTL(ctx, k, []byte("value"), time.Minute); err != nil {
					t.Error(err)
				}
				if _, _, err := s.Get(ctx, k); err != nil {
					t.Error(err)
				}
				if _, err := s.List(ctx, ""); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}
