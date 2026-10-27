package llm_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/storage/secret"
)

type ctxKey struct{}

// ctxStore records the context each lookup runs under.
type ctxStore struct{ seen *[]any }

func (s ctxStore) Get(ctx context.Context, k string) (*secret.Secret, error) {
	*s.seen = append(*s.seen, ctx.Value(ctxKey{}))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &secret.Secret{Key: k, Value: []byte("store-key")}, nil
}
func (ctxStore) List(context.Context, string) ([]string, error) { return nil, nil }
func (ctxStore) Exists(context.Context, string) (bool, error)   { return true, nil }

// The store is called synchronously, with the caller's context, so a
// slow backend honors its deadline and cancellation.
func TestSecretStoreRunsUnderCallerContext(t *testing.T) {
	isolateKeys(t)
	var seen []any
	ctx := context.WithValue(context.Background(), ctxKey{}, "caller")
	uri, err := llm.ApplyAPIKey(ctx, ctxStore{&seen}, "openrouter://m")
	require.NoError(t, err)
	assert.Contains(t, uri, "api_key=store-key")
	require.NotEmpty(t, seen)
	for _, v := range seen {
		assert.Equal(t, "caller", v)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	seen = nil
	_, err = llm.ApplyAPIKey(cancelled, ctxStore{&seen}, "openrouter://m")
	require.NotEmpty(t, seen, "the store saw the cancelled context")
	var missing *llm.MissingKeyError
	require.ErrorAs(t, err, &missing, "a cancelled lookup finds no key")
	assert.ErrorIs(t, err, context.Canceled)
}
