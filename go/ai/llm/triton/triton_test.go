package triton

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestClient_ImplementsProvider(t *testing.T) {
	var _ llm.Provider = (*Client)(nil)
}

func TestClient_ImplementsScorer(t *testing.T) {
	var _ Scorer = (*Client)(nil)
}

func TestNew_Validation(t *testing.T) {
	// Missing model.
	_, err := New(llm.ResolvedConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model name is required")

	// Valid config.
	p, err := New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{Model: "mf"},
	})
	require.NoError(t, err)
	c := p.(*Client)
	assert.Equal(t, "mf", c.ModelName())
	assert.Equal(t, "http://localhost:8000", c.BaseURL())
}

func TestNew_CustomBaseURL(t *testing.T) {
	p, err := New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{
			Model:   "bert",
			BaseURL: "http://triton:9000",
		},
	})
	require.NoError(t, err)
	c := p.(*Client)
	assert.Equal(t, "http://triton:9000", c.BaseURL())
}

func TestClient_Score_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v2/models/mf/infer", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var req inferRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			assert.Len(t, req.Inputs, 1)
			assert.Equal(t, "FP32", req.Inputs[0].Datatype)

			resp := inferResponse{
				Outputs: []inferOutput{
					{
						Name:     "output",
						Shape:    []int{1, 1},
						Datatype: "FP64",
						Data:     []float64{0.75},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
	defer srv.Close()

	c := &Client{
		baseURL:   srv.URL,
		modelName: "mf",
		httpC:     srv.Client(),
	}

	score, err := c.Score(context.Background(), []float32{1.0, 2.0, 3.0})
	require.NoError(t, err)
	assert.InDelta(t, 0.75, score, 0.001)
}

func TestClient_Score_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}))
	defer srv.Close()

	c := &Client{
		baseURL:   srv.URL,
		modelName: "mf",
		httpC:     srv.Client(),
	}

	_, err := c.Score(context.Background(), []float32{1.0})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestClient_Score_EmptyOutputs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			resp := inferResponse{Outputs: []inferOutput{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
	defer srv.Close()

	c := &Client{
		baseURL:   srv.URL,
		modelName: "mf",
		httpC:     srv.Client(),
	}

	_, err := c.Score(context.Background(), []float32{1.0})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no outputs")
}

func TestClient_Score_EmptyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			resp := inferResponse{
				Outputs: []inferOutput{{Data: []float64{}}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
	defer srv.Close()

	c := &Client{
		baseURL:   srv.URL,
		modelName: "mf",
		httpC:     srv.Client(),
	}

	_, err := c.Score(context.Background(), []float32{1.0})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty output")
}

func TestClient_Close(t *testing.T) {
	c := &Client{}
	assert.NoError(t, c.Close())
}

// authServer answers every inference request with a score and records
// its Authorization header values: nil when none was sent.
func authServer(t *testing.T) (*httptest.Server, *[][]string) {
	t.Helper()
	var got [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Values("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inferResponse{Outputs: []inferOutput{{Data: []float64{0.5}}}})
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// The key the URI carries goes out as a bearer token; a blank or
// missing key sends no Authorization header at all.
func TestScore_SendsKeyWhenSet(t *testing.T) {
	cases := map[string][]string{
		"":         nil,
		"  ":       nil,
		"\t":       nil,
		"fake-key": {"Bearer fake-key"},
	}
	for key, want := range cases {
		t.Run(key, func(t *testing.T) {
			srv, got := authServer(t)
			p, err := New(llm.ResolvedConfig{Provider: llm.ProviderConfig{
				BaseURL: srv.URL, Model: "mf", APIKey: key,
			}})
			require.NoError(t, err)
			_, err = p.(*Client).Score(context.Background(), []float32{1})
			require.NoError(t, err)
			require.Len(t, *got, 1)
			assert.Equal(t, want, (*got)[0])
		})
	}
}

// End to end: TRITON_API_KEY reaches the server through ApplyAPIKey
// and Resolve when set, and nothing is sent when it is unset or blank.
func TestScore_TritonAPIKeyEndToEnd(t *testing.T) {
	cases := []struct {
		name  string
		unset bool
		value string
		want  []string
	}{
		{name: "unset", unset: true},
		{name: "empty", value: ""},
		{name: "whitespace", value: "  "},
		{name: "set", value: "fake-triton", want: []string{"Bearer fake-triton"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("TRITON_API_KEY", tc.value)
			if tc.unset {
				require.NoError(t, os.Unsetenv("TRITON_API_KEY"))
			}
			srv, got := authServer(t)
			uri, err := llm.ApplyAPIKey(context.Background(), nil, "triton://mf?base_url="+srv.URL)
			require.NoError(t, err)
			p, err := llm.Resolve(uri)
			require.NoError(t, err)
			_, err = p.(*Client).Score(context.Background(), []float32{1})
			require.NoError(t, err)
			require.Len(t, *got, 1)
			assert.Equal(t, tc.want, (*got)[0])
		})
	}
}
