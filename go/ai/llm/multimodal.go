// Package llm — multimodal interfaces, request/response types,
// and Client convenience methods for image, audio, and video modalities.
package llm

import (
	"context"
	"fmt"
	"time"

	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// ---------------------------------------------------------------------------
// Multimodal provider interfaces
// ---------------------------------------------------------------------------

// ImageGenerator produces images from text prompts or an input image.
type ImageGenerator interface {
	GenerateImage(ctx context.Context, req ImageRequest) (ImageResponse, error)
}

// Transcriber converts audio to text.
type Transcriber interface {
	Transcribe(ctx context.Context, req TranscribeRequest) (TranscribeResponse, error)
}

// SpeechSynthesizer converts text to audio.
type SpeechSynthesizer interface {
	Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error)
}

// VideoAnalyzer analyzes a video and returns structured output.
type VideoAnalyzer interface {
	AnalyzeVideo(ctx context.Context, req VideoRequest) (VideoResponse, error)
}

// VideoGenerator produces a video from a text prompt.
type VideoGenerator interface {
	GenerateVideo(ctx context.Context, req VideoGenRequest) (VideoResponse, error)
}

// ---------------------------------------------------------------------------
// Request / response types
// ---------------------------------------------------------------------------

// ImageRequest parameters for image generation.
type ImageRequest struct {
	Prompt  string
	Source  MediaSource // optional: input image for img2img
	Size    string      // e.g. "1024x1024"
	N       int
	Style   string
	Quality string
	Ext     map[string]any
}

// ImageResponse is the result of image generation.
type ImageResponse struct {
	Images []ContentPart
	Usage  Usage
}

// TranscribeRequest parameters for audio transcription.
type TranscribeRequest struct {
	Source   MediaSource
	Language string
	Ext      map[string]any
}

// TranscriptSegment is a timed text chunk within a transcript.
type TranscriptSegment struct {
	Start float64
	End   float64
	Text  string
}

// TranscribeResponse is the result of transcription.
type TranscribeResponse struct {
	Text     string
	Segments []TranscriptSegment
	Usage    Usage
}

// SynthesizeRequest parameters for speech synthesis.
type SynthesizeRequest struct {
	Text   string
	Voice  string
	Format string
	Speed  float64
	Ext    map[string]any
}

// SynthesizeResponse is the result of speech synthesis.
type SynthesizeResponse struct {
	Audio ContentPart
	Usage Usage
}

// VideoRequest parameters for video analysis.
type VideoRequest struct {
	Source   MediaSource
	Messages []Message
	Ext      map[string]any
}

// VideoResponse is the result of video analysis or generation.
type VideoResponse struct {
	Video ContentPart
	Usage Usage
}

// VideoGenRequest parameters for video generation.
type VideoGenRequest struct {
	Prompt   string
	Duration float64
	Ext      map[string]any
}

// ---------------------------------------------------------------------------
// Client convenience methods
// ---------------------------------------------------------------------------

// GenerateImage delegates to the adapter's [ImageGenerator] with fallback
// and the full hook chain (OnRequest, OnResponse, OnError, OnFallback).
func (c *Client) GenerateImage(ctx context.Context, req ImageRequest) (ImageResponse, error) {
	hookReq := Request{Extensions: req.Ext}
	c.publish(ctx, TopicRequestStart, RequestStartPayload{Request: hookReq})

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error

	for i, p := range chain {
		ig, ok := p.(ImageGenerator)
		if !ok {
			err := llmerrors.NewCapabilityNotSupported(
				"image_gen", fmt.Sprintf("provider[%d]", i),
			)
			errs = append(errs, err)
			continue
		}

		start := time.Now()
		resp, err := ig.GenerateImage(ctx, req)
		dur := time.Since(start)

		if err == nil {
			c.publish(ctx, TopicRequestEnd, RequestEndPayload{
				Response: Response{OutputParts: resp.Images, Usage: resp.Usage},
				Duration: dur,
			})
			return resp, nil
		}

		errs = append(errs, err)

		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return ImageResponse{}, err
		}

		if i < len(chain)-1 {
			c.publish(ctx, TopicFallback, FallbackPayload{
				From: i, To: i + 1, Err: err, ErrMessage: err.Error(),
			})
		}
	}

	if allCapabilityErrors(errs) {
		return ImageResponse{}, errs[len(errs)-1]
	}

	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return ImageResponse{}, exhausted
}

// Transcribe delegates to the adapter's [Transcriber] with fallback
// and the full hook chain.
func (c *Client) Transcribe(ctx context.Context, req TranscribeRequest) (TranscribeResponse, error) {
	hookReq := Request{Extensions: req.Ext}
	c.publish(ctx, TopicRequestStart, RequestStartPayload{Request: hookReq})

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error

	for i, p := range chain {
		tr, ok := p.(Transcriber)
		if !ok {
			err := llmerrors.NewCapabilityNotSupported(
				"transcribe", fmt.Sprintf("provider[%d]", i),
			)
			errs = append(errs, err)
			continue
		}

		start := time.Now()
		resp, err := tr.Transcribe(ctx, req)
		dur := time.Since(start)

		if err == nil {
			c.publish(ctx, TopicRequestEnd, RequestEndPayload{
				Response: Response{Content: resp.Text, Usage: resp.Usage},
				Duration: dur,
			})
			return resp, nil
		}

		errs = append(errs, err)

		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return TranscribeResponse{}, err
		}

		if i < len(chain)-1 {
			c.publish(ctx, TopicFallback, FallbackPayload{
				From: i, To: i + 1, Err: err, ErrMessage: err.Error(),
			})
		}
	}

	if allCapabilityErrors(errs) {
		return TranscribeResponse{}, errs[len(errs)-1]
	}

	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return TranscribeResponse{}, exhausted
}

// Synthesize delegates to the adapter's [SpeechSynthesizer] with fallback
// and the full hook chain.
func (c *Client) Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error) {
	hookReq := Request{Extensions: req.Ext}
	c.publish(ctx, TopicRequestStart, RequestStartPayload{Request: hookReq})

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error

	for i, p := range chain {
		ss, ok := p.(SpeechSynthesizer)
		if !ok {
			err := llmerrors.NewCapabilityNotSupported(
				"synthesize", fmt.Sprintf("provider[%d]", i),
			)
			errs = append(errs, err)
			continue
		}

		start := time.Now()
		resp, err := ss.Synthesize(ctx, req)
		dur := time.Since(start)

		if err == nil {
			c.publish(ctx, TopicRequestEnd, RequestEndPayload{
				Response: Response{
					OutputParts: []ContentPart{resp.Audio},
					Usage:       resp.Usage,
				},
				Duration: dur,
			})
			return resp, nil
		}

		errs = append(errs, err)

		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return SynthesizeResponse{}, err
		}

		if i < len(chain)-1 {
			c.publish(ctx, TopicFallback, FallbackPayload{
				From: i, To: i + 1, Err: err, ErrMessage: err.Error(),
			})
		}
	}

	if allCapabilityErrors(errs) {
		return SynthesizeResponse{}, errs[len(errs)-1]
	}

	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return SynthesizeResponse{}, exhausted
}

// AnalyzeVideo delegates to the adapter's [VideoAnalyzer] with fallback
// and the full hook chain.
func (c *Client) AnalyzeVideo(ctx context.Context, req VideoRequest) (VideoResponse, error) {
	hookReq := Request{Messages: req.Messages, Extensions: req.Ext}
	c.publish(ctx, TopicRequestStart, RequestStartPayload{Request: hookReq})

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error

	for i, p := range chain {
		va, ok := p.(VideoAnalyzer)
		if !ok {
			err := llmerrors.NewCapabilityNotSupported(
				"video_analyze", fmt.Sprintf("provider[%d]", i),
			)
			errs = append(errs, err)
			continue
		}

		start := time.Now()
		resp, err := va.AnalyzeVideo(ctx, req)
		dur := time.Since(start)

		if err == nil {
			c.publish(ctx, TopicRequestEnd, RequestEndPayload{
				Response: Response{
					OutputParts: []ContentPart{resp.Video},
					Usage:       resp.Usage,
				},
				Duration: dur,
			})
			return resp, nil
		}

		errs = append(errs, err)

		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return VideoResponse{}, err
		}

		if i < len(chain)-1 {
			c.publish(ctx, TopicFallback, FallbackPayload{
				From: i, To: i + 1, Err: err, ErrMessage: err.Error(),
			})
		}
	}

	if allCapabilityErrors(errs) {
		return VideoResponse{}, errs[len(errs)-1]
	}

	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return VideoResponse{}, exhausted
}

// GenerateVideo delegates to the adapter's [VideoGenerator] with fallback
// and the full hook chain.
func (c *Client) GenerateVideo(ctx context.Context, req VideoGenRequest) (VideoResponse, error) {
	hookReq := Request{Extensions: req.Ext}
	c.publish(ctx, TopicRequestStart, RequestStartPayload{Request: hookReq})

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error

	for i, p := range chain {
		vg, ok := p.(VideoGenerator)
		if !ok {
			err := llmerrors.NewCapabilityNotSupported(
				"video_gen", fmt.Sprintf("provider[%d]", i),
			)
			errs = append(errs, err)
			continue
		}

		start := time.Now()
		resp, err := vg.GenerateVideo(ctx, req)
		dur := time.Since(start)

		if err == nil {
			c.publish(ctx, TopicRequestEnd, RequestEndPayload{
				Response: Response{
					OutputParts: []ContentPart{resp.Video},
					Usage:       resp.Usage,
				},
				Duration: dur,
			})
			return resp, nil
		}

		errs = append(errs, err)

		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return VideoResponse{}, err
		}

		if i < len(chain)-1 {
			c.publish(ctx, TopicFallback, FallbackPayload{
				From: i, To: i + 1, Err: err, ErrMessage: err.Error(),
			})
		}
	}

	if allCapabilityErrors(errs) {
		return VideoResponse{}, errs[len(errs)-1]
	}

	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, TopicRequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return VideoResponse{}, exhausted
}
