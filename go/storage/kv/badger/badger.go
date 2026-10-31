package badger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	badgerdb "github.com/dgraph-io/badger/v4"
)

// Store implements kv.Store backed by BadgerDB.
type Store struct {
	db *badgerdb.DB
}

// New opens a BadgerDB at the given directory path.
//
// It is NewContext with a background context, kept for callers that have
// none to offer.
func New(dir string) (*Store, error) {
	return NewContext(context.Background(), dir)
}

// NewContext opens a BadgerDB at the given directory path, honoring
// cancellation on ctx.
//
// There is no network policy to enforce here: BadgerDB is a directory on
// the local filesystem, so this driver has no dial to refuse and an offline
// context does not restrict it. ctx is checked before the open so a
// canceled caller does not create files, and the context-aware shape is
// what lets kv.Open route every shipped driver through one registration
// path.
func NewContext(ctx context.Context, dir string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts := badgerdb.DefaultOptions(dir).WithLogger(nil)
	db, err := badgerdb.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("badger kv: open: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Put(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.Update(func(txn *badgerdb.Txn) error {
		return txn.Set([]byte(key), value)
	})
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	var val []byte
	err := s.db.View(func(txn *badgerdb.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}
		val, err = item.ValueCopy(nil)
		return err
	})
	if err == badgerdb.ErrKeyNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.Update(func(txn *badgerdb.Txn) error {
		return txn.Delete([]byte(key))
	})
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var keys []string
	err := s.db.View(func(txn *badgerdb.Txn) error {
		opts := badgerdb.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()
		pfx := []byte(prefix)
		for it.Seek(pfx); it.Valid(); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			k := string(it.Item().Key())
			if !strings.HasPrefix(k, prefix) {
				break
			}
			keys = append(keys, k)
		}
		return nil
	})
	return keys, err
}

func (s *Store) PutWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.Update(func(txn *badgerdb.Txn) error {
		e := badgerdb.NewEntry([]byte(key), value).WithTTL(ttl)
		return txn.SetEntry(e)
	})
}

// PutIfAbsentWithTTL stores value under key until ttl has passed, unless
// a live entry already holds the key, and reports whether it stored. It
// runs as one transaction; badger's conflict detection aborts the losers
// of a race, which retry and then see the winner's entry.
func (s *Store) PutIfAbsentWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		inserted := false
		err := s.db.Update(func(txn *badgerdb.Txn) error {
			_, err := txn.Get([]byte(key))
			if err == nil {
				inserted = false
				return nil
			}
			if !errors.Is(err, badgerdb.ErrKeyNotFound) {
				return err
			}
			inserted = true
			return txn.SetEntry(badgerdb.NewEntry([]byte(key), value).WithTTL(ttl))
		})
		if errors.Is(err, badgerdb.ErrConflict) {
			continue
		}
		return inserted && err == nil, err
	}
}

func (s *Store) Close() error {
	return s.db.Close()
}
