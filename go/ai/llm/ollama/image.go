// Package ollama — ImageGenerator adapter.
//
// Ollama's /api/generate endpoint supports multimodal models that can return
// base64-encoded images. If the response contains no images field, returns
// ErrUnsupportedModality — the model likely does not support image generation.
package ollama

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// compile-time check.
var _ llm.ImageGenerator = (*Adapter)(nil)

// generateRequest is the payload for /api/generate.
type generateRequest struct {
	Model  string   `json:"model"`
	Prompt string   `json:"prompt"`
	Stream bool     `json:"stream"`
	Images []string `json:"images,omitempty"` // optional: base64 input images
}

// generateResponse is the response from /api/generate (non-streaming).
type generateResponse struct {
	Response string   `json:"response"`
	Images   []string `json:"images,omitempty"`
	Done     bool     `json:"done"`
}

const generateEndpoint = "/api/generate"

// GenerateImage implements [llm.ImageGenerator] via Ollama's /api/generate.
//
// Supported models: multimodal image-capable models (e.g. llava, bakllava).
// Returns [llmerrors.ErrUnsupportedModality] when the response has no images.
func (a *Adapter) GenerateImage(
	ctx context.Context, req llm.ImageRequest,
) (llm.ImageResponse, error) {
	model := a.model
	if m, ok := req.Ext["model"].(string); ok && m != "" {
		model = m
	}

	payload := generateRequest{
		Model:  model,
		Prompt: req.Prompt,
		Stream: false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return llm.ImageResponse{}, fmt.Errorf("ollama: image gen: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		a.baseURL+generateEndpoint, bytes.NewReader(body),
	)
	if err != nil {
		return llm.ImageResponse{}, fmt.Errorf("ollama: image gen: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return llm.ImageResponse{}, fmt.Errorf("ollama: image gen: %w", err)
	}
	defer resp.Body.Close()

	if err := a.checkStatus(resp, model); err != nil {
		return llm.ImageResponse{}, err
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.ImageResponse{}, fmt.Errorf("ollama: image gen: read response: %w", err)
	}

	var gr generateResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return llm.ImageResponse{}, fmt.Errorf("ollama: image gen: decode: %w", err)
	}

	if len(gr.Images) == 0 {
		return llm.ImageResponse{}, llmerrors.NewUnsupportedModality(
			"image_gen", "ollama",
			fmt.Errorf("model %q returned no images; use an image-capable model", model),
		)
	}

	parts := make([]llm.ContentPart, 0, len(gr.Images))
	for _, b64 := range gr.Images {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return llm.ImageResponse{}, fmt.Errorf("ollama: image gen: decode base64: %w", err)
		}
		parts = append(parts, llm.ContentPart{
			Type:     llm.PartTypeImage,
			Source:   llm.InlineSource(data, "image/png"),
			MimeType: "image/png",
		})
	}

	return llm.ImageResponse{Images: parts}, nil
}
