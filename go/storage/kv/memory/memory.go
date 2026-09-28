// Package memory is the in-process kv driver: a bounded map that lives
// as long as the process, with expiry and least-recently-used eviction.
//
// It is the store for data that is cheap to lose — a result cache, a
// short-lived session table — in a process that has no file to keep it
// in. A restart empties it; nothing is shared with another process.
package memory

import (
	"container/list"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultMaxBytes is the budget a Store holds when [WithMaxBytes] does
// not set one: 64 MiB of keys and values together.
const DefaultMaxBytes int64 = 64 << 20

// ErrValueTooLarge is returned by Put and PutWithTTL for an entry whose
// key and value together exceed the store's whole budget. Nothing is
// evicted for it.
var ErrValueTooLarge = errors.New("kv memory: entry larger than the store's budget")

// ErrClosed is returned by every call on a Store after Close.
var ErrClosed = errors.New("kv memory: store closed")

// Option configures [New].
type Option func(*Store)

// WithMaxBytes bounds the bytes of keys and values the store holds.
// When a write would pass the bound, expired entries go first, then the
// least recently used. Zero or less keeps [DefaultMaxBytes].
func WithMaxBytes(n int64) Option {
	return func(s *Store) {
		if n > 0 {
			s.maxBytes = n
		}
	}
}

// Store implements kv.Store and kv.TTLStore in memory. Safe for
// concurrent use.
type Store struct {
	mu       sync.Mutex
	maxBytes int64
	used     int64
	items    map[string]*list.Element
	lru      *list.List // front = most recently used
	now      func() time.Time
	closed   bool
}

type entry struct {
	key     string
	value   []byte
	expires time.Time // zero = never
}

func (e *entry) cost() int64 { return int64(len(e.key) + len(e.value)) }

// New returns an empty Store.
func New(opts ...Option) *Store {
	s := &Store{
		maxBytes: DefaultMaxBytes,
		items:    map[string]*list.Element{},
		lru:      list.New(),
		now:      time.Now,
	}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return s
}

// Put stores value under key with no expiry.
func (s *Store) Put(ctx context.Context, key string, value []byte) error {
	return s.put(ctx, key, value, 0)
}

// PutWithTTL stores value under key until ttl has passed. A ttl of
// zero or less stores it with no expiry, as Put does.
func (s *Store) PutWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.put(ctx, key, value, ttl)
}

func (s *Store) put(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e := &entry{key: key, value: append([]byte(nil), value...)}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if e.cost() > s.maxBytes {
		return ErrValueTooLarge
	}
	if ttl > 0 {
		e.expires = s.now().Add(ttl)
	}
	if el, ok := s.items[key]; ok {
		s.remove(el)
	}
	s.makeRoom(e.cost())
	s.items[key] = s.lru.PushFront(e)
	s.used += e.cost()
	return nil
}

// Get returns the value stored under key. An expired entry reads as
// absent and is dropped.
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false, ErrClosed
	}
	el, ok := s.items[key]
	if !ok {
		return nil, false, nil
	}
	e := el.Value.(*entry)
	if s.expired(e) {
		s.remove(el)
		return nil, false, nil
	}
	s.lru.MoveToFront(el)
	return append([]byte(nil), e.value...), true, nil
}

// Delete removes key. Deleting an absent key is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if el, ok := s.items[key]; ok {
		s.remove(el)
	}
	return nil
}

// List returns the unexpired keys starting with prefix, sorted.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	var out []string
	for k, el := range s.items {
		if strings.HasPrefix(k, prefix) && !s.expired(el.Value.(*entry)) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Close drops every entry. Later calls return [ErrClosed].
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.items = map[string]*list.Element{}
	s.lru.Init()
	s.used = 0
	return nil
}

func (s *Store) expired(e *entry) bool {
	return !e.expires.IsZero() && !s.now().Before(e.expires)
}

func (s *Store) remove(el *list.Element) {
	e := s.lru.Remove(el).(*entry)
	delete(s.items, e.key)
	s.used -= e.cost()
}

// makeRoom frees space for an entry costing need: expired entries
// first, then the least recently used.
func (s *Store) makeRoom(need int64) {
	if s.used+need <= s.maxBytes {
		return
	}
	for el := s.lru.Back(); el != nil; {
		prev := el.Prev()
		if s.expired(el.Value.(*entry)) {
			s.remove(el)
		}
		el = prev
	}
	for s.used+need > s.maxBytes {
		back := s.lru.Back()
		if back == nil {
			return
		}
		s.remove(back)
	}
}
