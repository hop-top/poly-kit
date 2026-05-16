// Package openai — Transcriber and SpeechSynthesizer adapters.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"

	"hop.top/kit/go/ai/llm"
)

// compile-time checks.
var (
	_ llm.Transcriber       = (*Adapter)(nil)
	_ llm.SpeechSynthesizer = (*Adapter)(nil)
)

// ---------------------------------------------------------------------------
// Transcriber (Whisper)
// ---------------------------------------------------------------------------

// verboseTranscription is the raw shape returned by verbose_json format.
// The SDK's Transcription type only exposes .Text; parse segments manually.
type verboseTranscription struct {
	Text     string           `json:"text"`
	Segments []verboseSegment `json:"segments"`
}

type verboseSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// Transcribe implements [llm.Transcriber] using Whisper.
func (a *Adapter) Transcribe(
	ctx context.Context, req llm.TranscribeRequest,
) (llm.TranscribeResponse, error) {
	if req.Source == nil {
		return llm.TranscribeResponse{}, fmt.Errorf("openai: transcribe: source is required")
	}

	reader, err := req.Source.Reader(ctx)
	if err != nil {
		return llm.TranscribeResponse{}, fmt.Errorf("openai: transcribe: open source: %w", err)
	}
	defer reader.Close()

	model := oai.AudioModelWhisper1
	if m, ok := req.Ext["model"].(string); ok && m != "" {
		model = oai.AudioModel(m)
	}

	p := oai.AudioTranscriptionNewParams{
		File:                   reader,
		Model:                  model,
		ResponseFormat:         oai.AudioResponseFormatVerboseJSON,
		TimestampGranularities: []string{"segment"},
	}
	if req.Language != "" {
		p.Language = param.NewOpt(req.Language)
	}

	resp, err := a.client.Audio.Transcriptions.New(ctx, p)
	if err != nil {
		return llm.TranscribeResponse{}, mapError(err, a.scheme, string(model))
	}

	// Parse raw JSON for segments (SDK Transcription only exposes .Text).
	var verbose verboseTranscription
	if raw := resp.RawJSON(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &verbose); err != nil {
			return llm.TranscribeResponse{}, fmt.Errorf("openai: transcribe: parse verbose transcription: %w", err)
		}
	}

	text := resp.Text
	if text == "" {
		text = verbose.Text
	}

	segs := make([]llm.TranscriptSegment, 0, len(verbose.Segments))
	for _, s := range verbose.Segments {
		segs = append(segs, llm.TranscriptSegment{
			Start: s.Start,
			End:   s.End,
			Text:  s.Text,
		})
	}

	// Map usage if available (token-based models).
	var usage llm.Usage
	if u := resp.Usage; u.TotalTokens > 0 {
		usage = llm.Usage{
			PromptTokens: int(u.InputTokens),
			TotalTokens:  int(u.TotalTokens),
		}
	}

	return llm.TranscribeResponse{
		Text:     text,
		Segments: segs,
		Usage:    usage,
	}, nil
}

// ---------------------------------------------------------------------------
// SpeechSynthesizer (TTS)
// ---------------------------------------------------------------------------

// Synthesize implements [llm.SpeechSynthesizer] using TTS.
func (a *Adapter) Synthesize(
	ctx context.Context, req llm.SynthesizeRequest,
) (llm.SynthesizeResponse, error) {
	model := oai.SpeechModelTTS1
	if m, ok := req.Ext["model"].(string); ok && m != "" {
		model = oai.SpeechModel(m)
	}

	voice := oai.AudioSpeechNewParamsVoiceAlloy
	if req.Voice != "" {
		voice = oai.AudioSpeechNewParamsVoice(req.Voice)
	}

	p := oai.AudioSpeechNewParams{
		Input: req.Text,
		Model: model,
		Voice: voice,
	}

	if req.Format != "" {
		p.ResponseFormat = oai.AudioSpeechNewParamsResponseFormat(req.Format)
	} else {
		p.ResponseFormat = oai.AudioSpeechNewParamsResponseFormatMP3
	}

	if req.Speed != 0 {
		p.Speed = param.NewOpt(req.Speed)
	}

	httpResp, err := a.client.Audio.Speech.New(ctx, p)
	if err != nil {
		return llm.SynthesizeResponse{}, mapError(err, a.scheme, string(model))
	}
	defer httpResp.Body.Close()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return llm.SynthesizeResponse{}, fmt.Errorf("openai: tts: read response: %w", err)
	}

	mimeType := formatToMIME(string(p.ResponseFormat))
	audio := llm.ContentPart{
		Type:     llm.PartTypeAudio,
		Source:   llm.InlineSource(data, mimeType),
		MimeType: mimeType,
	}

	return llm.SynthesizeResponse{Audio: audio}, nil
}

func formatToMIME(format string) string {
	switch format {
	case "mp3":
		return "audio/mpeg"
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "wav":
		return "audio/wav"
	case "pcm":
		return "audio/pcm"
	default:
		return "audio/mpeg"
	}
}
