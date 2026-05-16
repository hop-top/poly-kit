package triton

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
