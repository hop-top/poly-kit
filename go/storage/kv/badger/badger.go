package badger

import (
	"context"
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
func New(dir string) (*Store, error) {
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

func (s *Store) Close() error {
	return s.db.Close()
}
