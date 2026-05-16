package etcd

import (
	"context"
	"fmt"

	clientv3 "go.etcd.io/etcd/client/v3"

	"hop.top/kit/go/storage/kv"
)

// Store implements kv.Store backed by etcd.
type Store struct {
	client *clientv3.Client
	prefix string
}

var _ kv.Store = (*Store)(nil)

// New connects to an etcd cluster and returns a prefixed Store.
func New(endpoints []string, prefix string) (*Store, error) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd kv: connect: %w", err)
	}
	return &Store{client: client, prefix: prefix}, nil
}

func (s *Store) Put(ctx context.Context, key string, value []byte) error {
	_, err := s.client.Put(ctx, s.prefix+key, string(value))
	return err
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	resp, err := s.client.Get(ctx, s.prefix+key)
	if err != nil {
		return nil, false, err
	}
	if len(resp.Kvs) == 0 {
		return nil, false, nil
	}
	return resp.Kvs[0].Value, true, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.Delete(ctx, s.prefix+key)
	return err
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	full := s.prefix + prefix
	resp, err := s.client.Get(ctx, full, clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		// Strip the store prefix, return relative key.
		keys = append(keys, string(kv.Key)[len(s.prefix):])
	}
	return keys, nil
}

func (s *Store) Close() error {
	return s.client.Close()
}
