package llmjudge_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/llmjudge"
	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/llmxrr"
	_ "hop.top/kit/go/ai/llm/ollama"
)

const (
	// groundModel is the model the grounding cassette was recorded with.
	groundModel = "qwen3-vl:8b"
	// groundW, groundH size the synthetic image; the red rectangle sits at
	// pixels [groundRect] (x1, y1, x2, y2).
	groundW, groundH = 64, 48
)

var groundRect = [4]int{40, 10, 56, 22}

// redRect is a white groundW x groundH PNG with one red rectangle. Stored
// (uncompressed) deflate keeps the bytes, hence the request, identical
// across Go versions so the cassette keeps matching.
func redRect(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, groundW, groundH))
	for y := 0; y < groundH; y++ {
		for x := 0; x < groundW; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x >= groundRect[0] && x < groundRect[2] && y >= groundRect[1] && y < groundRect[3] {
				c = color.RGBA{220, 0, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	require.NoError(t, enc.Encode(&buf, img))
	return buf.Bytes()
}

// TestGroundingOllamaCassette replays a real Ollama /api/chat exchange with
// a local vision model grounding a red rectangle on a 0-1000 grid, in free
// text. Re-record against a local daemon that has the model:
//
//	XRR_MODE=record go test -run TestGroundingOllamaCassette ./go/ai/judge/llmjudge
func TestGroundingOllamaCassette(t *testing.T) {
	tr, err := llmxrr.New(cassetteDir)
	require.NoError(t, err)
	p, err := llm.Resolve("ollama://"+groundModel+"?base_url=http://localhost:11434", llm.WithTransport(tr))
	require.NoError(t, err)
	defer p.Close()

	data := redRect(t)
	c := judge.Case{
		Questions: []judge.Question{{
			Name: "rect", Kind: judge.KindGrounding, Instructions: "the red rectangle in `image`",
		}},
		Assets: []llm.ContentPart{{
			Type: llm.PartTypeImage, MimeType: "image/png",
			Source: llm.InlineSource(data, "image/png"),
		}},
	}
	j := llmjudge.New(p.(llm.Completer),
		llmjudge.Model(groundModel), llmjudge.Temperature(0),
		llmjudge.BoxFormat(llmjudge.BoxFormatFor(groundModel)))
	v, err := j.Judge(context.Background(), c)
	require.NoError(t, err)

	a := v.Answers["rect"]
	require.NotNil(t, a.Box)
	want := judge.Box{
		Left: float64(groundRect[0]) / groundW, Top: float64(groundRect[1]) / groundH,
		Right: float64(groundRect[2]) / groundW, Bottom: float64(groundRect[3]) / groundH,
	}
	assert.GreaterOrEqual(t, a.Box.IoU(want), 0.7, "got %+v want %+v", *a.Box, want)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}
