// Package triton provides a client for NVIDIA Triton Inference Server
// registered as the triton:// URI scheme.
//
// The Scorer interface is the primary abstraction: given a float32 input
// vector, it returns a scalar score. Implementations may use gRPC (KServe
// v2 protocol) or HTTP. The current implementation uses HTTP as a fallback
// until gRPC proto generation is set up.
package triton

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"hop.top/kit/go/ai/llm"
)

func init() {
	llm.Register("triton", New)
}

// Scorer scores a float32 input vector and returns a scalar score.
type Scorer interface {
	Score(ctx context.Context, input []float32) (float64, error)
}

// Client implements Scorer via Triton's HTTP inference API (KServe v2).
type Client struct {
	baseURL   string
	modelName string
	httpC     *http.Client
}

// compile-time checks.
var _ llm.Provider = (*Client)(nil)

// New creates a triton Client from the resolved config.
//
// URI format: triton://[host:port/]model_name
// Defaults to localhost:8000 if no host is specified.
func New(cfg llm.ResolvedConfig) (llm.Provider, error) {
	baseURL := cfg.Provider.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	model := cfg.Provider.Model
	if model == "" {
		model = cfg.URI.Model
	}
	if model == "" {
		return nil, fmt.Errorf("triton: model name is required")
	}

	return &Client{
		baseURL:   baseURL,
		modelName: model,
		httpC:     &http.Client{},
	}, nil
}

// Close is a no-op for the HTTP client.
func (c *Client) Close() error { return nil }

// Score sends an inference request to Triton's HTTP API and returns
// the scalar output.
func (c *Client) Score(
	ctx context.Context, input []float32,
) (float64, error) {
	// Build KServe v2 inference request.
	reqBody := inferRequest{
		Inputs: []inferInput{
			{
				Name:     "input",
				Shape:    []int{1, len(input)},
				Datatype: "FP32",
				Data:     [][]float32{input},
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("triton: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v2/models/%s/infer", c.baseURL, c.modelName)

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("triton: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpC.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("triton: inference request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("triton: HTTP %d: %s",
			resp.StatusCode, string(respBody))
	}

	var inferResp inferResponse
	if err := json.NewDecoder(resp.Body).Decode(&inferResp); err != nil {
		return 0, fmt.Errorf("triton: decode response: %w", err)
	}

	if len(inferResp.Outputs) == 0 {
		return 0, fmt.Errorf("triton: no outputs in response")
	}

	output := inferResp.Outputs[0]
	if len(output.Data) == 0 {
		return 0, fmt.Errorf("triton: empty output data")
	}

	return output.Data[0], nil
}

// ModelName returns the configured model name.
func (c *Client) ModelName() string { return c.modelName }

// BaseURL returns the configured base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// KServe v2 inference protocol types.

type inferRequest struct {
	Inputs []inferInput `json:"inputs"`
}

type inferInput struct {
	Name     string      `json:"name"`
	Shape    []int       `json:"shape"`
	Datatype string      `json:"datatype"`
	Data     [][]float32 `json:"data"`
}

type inferResponse struct {
	Outputs []inferOutput `json:"outputs"`
}

type inferOutput struct {
	Name     string    `json:"name"`
	Shape    []int     `json:"shape"`
	Datatype string    `json:"datatype"`
	Data     []float64 `json:"data"`
}
