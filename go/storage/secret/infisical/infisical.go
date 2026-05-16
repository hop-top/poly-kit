package infisical

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hop.top/kit/go/storage/secret"
)

type setRequest struct {
	WorkspaceID string `json:"workspaceId"`
	Environment string `json:"environment"`
	SecretValue string `json:"secretValue"`
}

type deleteRequest struct {
	WorkspaceID string `json:"workspaceId"`
	Environment string `json:"environment"`
}

// Store implements secret.MutableStore against the Infisical REST API.
type Store struct {
	baseURL string
	token   string
	project string
	env     string
	client  *http.Client
}

// New creates a Store for the given Infisical instance.
func New(baseURL, token, project, env string) *Store {
	return &Store{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		project: project,
		env:     env,
		client:  &http.Client{},
	}
}

// SetClient overrides the default HTTP client (useful for testing).
func (s *Store) SetClient(c *http.Client) { s.client = c }

type rawSecret struct {
	SecretKey   string    `json:"secretKey"`
	SecretValue string    `json:"secretValue"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type getResponse struct {
	Secret rawSecret `json:"secret"`
}

type listResponse struct {
	Secrets []rawSecret `json:"secrets"`
}

func (s *Store) Get(ctx context.Context, key string) (*secret.Secret, error) {
	url := fmt.Sprintf("%s/api/v3/secrets/raw/%s?environment=%s&workspaceId=%s",
		s.baseURL, url.PathEscape(key), s.env, s.project)

	resp, err := s.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, secret.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("infisical: GET %s status %d", key, resp.StatusCode)
	}

	var gr getResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, fmt.Errorf("infisical: decode: %w", err)
	}
	return &secret.Secret{Key: gr.Secret.SecretKey, Value: []byte(gr.Secret.SecretValue)}, nil
}

func (s *Store) Set(ctx context.Context, key string, value []byte) error {
	url := fmt.Sprintf("%s/api/v3/secrets/raw/%s", s.baseURL, url.PathEscape(key))
	body, err := json.Marshal(setRequest{
		WorkspaceID: s.project,
		Environment: s.env,
		SecretValue: string(value),
	})
	if err != nil {
		return fmt.Errorf("infisical: marshal set: %w", err)
	}

	resp, err := s.do(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("infisical: SET %s status %d", key, resp.StatusCode)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	url := fmt.Sprintf("%s/api/v3/secrets/raw/%s", s.baseURL, url.PathEscape(key))
	body, err := json.Marshal(deleteRequest{
		WorkspaceID: s.project,
		Environment: s.env,
	})
	if err != nil {
		return fmt.Errorf("infisical: marshal delete: %w", err)
	}

	resp, err := s.do(ctx, http.MethodDelete, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return secret.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("infisical: DELETE %s status %d", key, resp.StatusCode)
	}
	return nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	url := fmt.Sprintf("%s/api/v3/secrets/raw?environment=%s&workspaceId=%s",
		s.baseURL, s.env, s.project)

	resp, err := s.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("infisical: LIST status %d", resp.StatusCode)
	}

	var lr listResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("infisical: decode list: %w", err)
	}

	var keys []string
	for _, s := range lr.Secrets {
		if strings.HasPrefix(s.SecretKey, prefix) {
			keys = append(keys, s.SecretKey)
		}
	}
	return keys, nil
}

func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Get(ctx, key)
	if err == secret.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Metadata returns descriptive info about the Infisical secret. The
// REST endpoint returns the secret value alongside metadata in the
// same payload; we strip the value before returning. The Source
// embeds project/env so callers can distinguish staging vs prod.
func (s *Store) Metadata(ctx context.Context, key string) (secret.StoredMeta, error) {
	endpoint := fmt.Sprintf("%s/api/v3/secrets/raw/%s?environment=%s&workspaceId=%s",
		s.baseURL, url.PathEscape(key), s.env, s.project)

	resp, err := s.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return secret.StoredMeta{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return secret.StoredMeta{}, secret.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return secret.StoredMeta{}, fmt.Errorf("infisical: GET metadata %s status %d", key, resp.StatusCode)
	}

	var gr getResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return secret.StoredMeta{}, fmt.Errorf("infisical: decode metadata: %w", err)
	}
	return secret.StoredMeta{
		Key:       key,
		Source:    fmt.Sprintf("infisical/%s/%s", s.project, s.env),
		Backend:   "infisical",
		UpdatedAt: gr.Secret.UpdatedAt,
	}, nil
}

var _ secret.MetadataReader = (*Store)(nil)

func (s *Store) do(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("infisical: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	return s.client.Do(req)
}
