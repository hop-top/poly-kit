// Package openai — ImageGenerator adapter.
package openai

import (
	"context"
	"encoding/base64"
	"fmt"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// compile-time check.
var _ llm.ImageGenerator = (*Adapter)(nil)

// GenerateImage implements [llm.ImageGenerator].
func (a *Adapter) GenerateImage(
	ctx context.Context, req llm.ImageRequest,
) (llm.ImageResponse, error) {
	model := "dall-e-3"
	if m, ok := req.Ext["model"].(string); ok && m != "" {
		model = m
	}

	n := int64(1)
	if req.N > 0 {
		n = int64(req.N)
	}

	size := oai.ImageGenerateParamsSize("1024x1024")
	if req.Size != "" {
		size = oai.ImageGenerateParamsSize(req.Size)
	}

	p := oai.ImageGenerateParams{
		Prompt: req.Prompt,
		Model:  oai.ImageModel(model),
		N:      param.NewOpt(n),
		Size:   size,
	}

	if req.Quality != "" {
		p.Quality = oai.ImageGenerateParamsQuality(req.Quality)
	}
	if req.Style != "" {
		p.Style = oai.ImageGenerateParamsStyle(req.Style)
	}

	// Use URL format for dall-e models (gpt-image-1 always returns b64_json).
	useURL := model != "gpt-image-1"
	if useURL {
		p.ResponseFormat = oai.ImageGenerateParamsResponseFormatURL
	}

	resp, err := a.client.Images.Generate(ctx, p)
	if err != nil {
		return llm.ImageResponse{}, mapImageError(err, a.scheme, model)
	}

	images, err := mapImages(ctx, resp.Data, useURL)
	if err != nil {
		return llm.ImageResponse{}, err
	}

	usage := llm.Usage{
		PromptTokens:     int(resp.Usage.InputTokens),
		CompletionTokens: int(resp.Usage.OutputTokens),
		TotalTokens:      int(resp.Usage.TotalTokens),
	}

	return llm.ImageResponse{Images: images, Usage: usage}, nil
}

// mapImages converts OpenAI Image results to ContentParts.
func mapImages(
	_ context.Context, data []oai.Image, useURL bool,
) ([]llm.ContentPart, error) {
	out := make([]llm.ContentPart, 0, len(data))
	for _, img := range data {
		var part llm.ContentPart
		part.Type = llm.PartTypeImage
		if useURL && img.URL != "" {
			part.Source = llm.URLSource(img.URL)
			part.MimeType = "image/png"
		} else if img.B64JSON != "" {
			raw, err := base64.StdEncoding.DecodeString(img.B64JSON)
			if err != nil {
				return nil, fmt.Errorf("openai: decode b64_json: %w", err)
			}
			part.Source = llm.InlineSource(raw, "image/png")
			part.MimeType = "image/png"
		} else {
			return nil, llmerrors.NewUnsupportedModality("image", "openai",
				fmt.Errorf("no image data in response"))
		}
		out = append(out, part)
	}
	return out, nil
}

func mapImageError(err error, scheme, model string) error {
	return mapError(err, scheme, model)
}
