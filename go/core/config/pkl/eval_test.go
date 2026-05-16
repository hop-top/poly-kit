package pkl

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToPklLiteral_String(t *testing.T) {
	assert.Equal(t, `"hello"`, toPklLiteral("hello"))
}

func TestToPklLiteral_Bool(t *testing.T) {
	assert.Equal(t, "true", toPklLiteral(true))
	assert.Equal(t, "false", toPklLiteral(false))
}

func TestToPklLiteral_Int(t *testing.T) {
	assert.Equal(t, "42", toPklLiteral(42))
}

func TestToPklLiteral_Int64(t *testing.T) {
	assert.Equal(t, "99", toPklLiteral(int64(99)))
}

func TestToPklLiteral_Float(t *testing.T) {
	assert.Equal(t, "3.14", toPklLiteral(3.14))
}

func TestToPklLiteral_StringSlice(t *testing.T) {
	got := toPklLiteral([]string{"a", "b"})
	assert.Contains(t, got, "new Listing {")
	assert.Contains(t, got, `"a"`)
	assert.Contains(t, got, `"b"`)
}

func TestToPklLiteral_EmptySlice(t *testing.T) {
	assert.Equal(t, "new Listing {}", toPklLiteral([]string{}))
}

func TestToPklLiteral_Unsupported(t *testing.T) {
	assert.Equal(t, "", toPklLiteral(struct{}{}))
}

func TestGenerateAmendment(t *testing.T) {
	answers := map[string]any{
		"name": "app",
		"port": 8080,
	}
	got, err := generateAmendment("/tmp/config.pkl", answers)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(got, `amends "/tmp/config.pkl"`))
	assert.Contains(t, got, `name = "app"`)
	assert.Contains(t, got, "port = 8080")
}

func TestGenerateAmendment_UnsupportedType(t *testing.T) {
	answers := map[string]any{"bad": struct{}{}}
	_, err := generateAmendment("/tmp/x.pkl", answers)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported value type")
}

func TestResolve_Basic(t *testing.T) {
	if _, err := exec.LookPath("pkl"); err != nil {
		t.Skip("pkl binary not found")
	}

	result, err := Resolve(
		context.Background(),
		"testdata/basic.pkl",
		map[string]any{"name": "myapp", "port": 9090},
	)
	require.NoError(t, err)

	assert.Equal(t, "myapp", result["name"])
	// pkl-go may return int or float64 depending on version
	switch v := result["port"].(type) {
	case int:
		assert.Equal(t, 9090, v)
	case int64:
		assert.Equal(t, int64(9090), v)
	case float64:
		assert.Equal(t, float64(9090), v)
	default:
		t.Fatalf("unexpected port type: %T", result["port"])
	}
	assert.Equal(t, false, result["debug"])
}

func TestResolve_Defaults(t *testing.T) {
	if _, err := exec.LookPath("pkl"); err != nil {
		t.Skip("pkl binary not found")
	}

	result, err := Resolve(
		context.Background(),
		"testdata/basic.pkl",
		map[string]any{},
	)
	require.NoError(t, err)

	assert.Equal(t, "default-app", result["name"])
}
