package openbao

import (
	"context"
	"errors"
	"fmt"

	"github.com/openbao/openbao/api/v2"

	"hop.top/kit/go/storage/secret"
)

// Store is an OpenBao/Vault KV v2 backed MutableStore.
type Store struct {
	client *api.Client
	mount  string
	prefix string
}

// New returns a Store targeting the given OpenBao server.
// Mount defaults to "secret" when empty.
func New(addr, token, mount string) (*Store, error) {
	cfg := api.DefaultConfig()
	cfg.Address = addr
	c, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("openbao: new client: %w", err)
	}
	c.SetToken(token)
	if mount == "" {
		mount = "secret"
	}
	return &Store{client: c, mount: mount}, nil
}

// NewWithClient returns a Store using a pre-configured api.Client.
func NewWithClient(c *api.Client, mount, prefix string) *Store {
	if mount == "" {
		mount = "secret"
	}
	return &Store{client: c, mount: mount, prefix: prefix}
}

func (s *Store) path(key string) string { return s.prefix + key }

func isNotFound(err error) bool {
	var re *api.ResponseError
	if errors.As(err, &re) && re.StatusCode == 404 {
		return true
	}
	return false
}

func (s *Store) Get(ctx context.Context, key string) (*secret.Secret, error) {
	kv := s.client.KVv2(s.mount)
	sec, err := kv.Get(ctx, s.path(key))
	if err != nil {
		if isNotFound(err) {
			return nil, secret.ErrNotFound
		}
		return nil, fmt.Errorf("openbao: get %q: %w", key, err)
	}
	v, ok := sec.Data["value"]
	if !ok {
		return nil, secret.ErrNotFound
	}
	str, _ := v.(string)
	meta := make(map[string]string)
	if sec.CustomMetadata != nil {
		for k, val := range sec.CustomMetadata {
			if str, ok := val.(string); ok {
				meta[k] = str
			}
		}
	}
	return &secret.Secret{
		Key:      key,
		Value:    []byte(str),
		Metadata: meta,
	}, nil
}

func (s *Store) Set(ctx context.Context, key string, value []byte) error {
	kv := s.client.KVv2(s.mount)
	_, err := kv.Put(ctx, s.path(key), map[string]interface{}{
		"value": string(value),
	})
	if err != nil {
		return fmt.Errorf("openbao: set %q: %w", key, err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	kv := s.client.KVv2(s.mount)
	if err := kv.DeleteMetadata(ctx, s.path(key)); err != nil {
		if isNotFound(err) {
			return secret.ErrNotFound
		}
		return fmt.Errorf("openbao: delete %q: %w", key, err)
	}
	return nil
}

func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Get(ctx, key)
	if err != nil {
		if err == secret.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	path := s.mount + "/metadata/" + s.prefix + prefix
	sec, err := s.client.Logical().ListWithContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("openbao: list %q: %w", prefix, err)
	}
	if sec == nil || sec.Data == nil {
		return nil, nil
	}
	raw, ok := sec.Data["keys"]
	if !ok {
		return nil, nil
	}
	slice, ok := raw.([]interface{})
	if !ok {
		return nil, nil
	}
	keys := make([]string, 0, len(slice))
	for _, k := range slice {
		if str, ok := k.(string); ok {
			keys = append(keys, str)
		}
	}
	return keys, nil
}

// Metadata returns KV v2 metadata via the dedicated /metadata
// endpoint, which never returns the secret value. UpdatedAt is the
// most recent write time. Custom metadata is surfaced as a single
// "scope:<key>=<val>" entry per pair so adopters can filter on it.
func (s *Store) Metadata(ctx context.Context, key string) (secret.StoredMeta, error) {
	kv := s.client.KVv2(s.mount)
	md, err := kv.GetMetadata(ctx, s.path(key))
	if err != nil {
		if isNotFound(err) {
			return secret.StoredMeta{}, secret.ErrNotFound
		}
		return secret.StoredMeta{}, fmt.Errorf("openbao: metadata %q: %w", key, err)
	}
	if md == nil {
		return secret.StoredMeta{}, secret.ErrNotFound
	}
	meta := secret.StoredMeta{
		Key:       key,
		Source:    "openbao/" + s.mount + "/" + s.prefix + key,
		Backend:   "openbao",
		UpdatedAt: md.UpdatedTime,
	}
	for k, v := range md.CustomMetadata {
		if str, ok := v.(string); ok {
			meta.Scopes = append(meta.Scopes, k+"="+str)
		}
	}
	return meta, nil
}

var _ secret.MetadataReader = (*Store)(nil)
