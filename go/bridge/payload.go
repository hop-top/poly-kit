package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInvalidOneof is returned when a Payload has zero or more than one
// content kind set. Mirrors proto3 oneof semantics on the JSON wire.
var ErrInvalidOneof = errors.New("bridge: payload content must set exactly one kind (text|url|file|blob)")

// Text payload — UTF-8 body + optional MIME hint.
type Text struct {
	Body string `json:"body"`
	Mime string `json:"mime,omitempty"`
}

// URL payload — href plus optional page title and user selection.
type URL struct {
	Href      string `json:"href"`
	Title     string `json:"title,omitempty"`
	Selection string `json:"selection,omitempty"`
}

// File payload — local filesystem path plus MIME hint and size in bytes.
type File struct {
	Path string `json:"path"`
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// Blob payload — inline bytes (<1 MiB), MIME hint, optional filename.
type Blob struct {
	Data     []byte `json:"data"`
	Mime     string `json:"mime,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// Payload is the wire-level inbox message. Exactly one of Text/URL/File/Blob
// must be non-nil; UnmarshalJSON enforces this invariant.
type Payload struct {
	ID            string `json:"id"`
	Source        string `json:"source,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	Timestamp     int64  `json:"timestamp,omitempty"`

	Text *Text `json:"text,omitempty"`
	URL  *URL  `json:"url,omitempty"`
	File *File `json:"file,omitempty"`
	Blob *Blob `json:"blob,omitempty"`

	Meta map[string]string `json:"meta,omitempty"`
}

// payloadShadow mirrors Payload field-for-field; lets UnmarshalJSON decode
// without recursing into Payload.UnmarshalJSON.
type payloadShadow struct {
	ID            string            `json:"id"`
	Source        string            `json:"source,omitempty"`
	SourceVersion string            `json:"source_version,omitempty"`
	Timestamp     int64             `json:"timestamp,omitempty"`
	Text          *Text             `json:"text,omitempty"`
	URL           *URL              `json:"url,omitempty"`
	File          *File             `json:"file,omitempty"`
	Blob          *Blob             `json:"blob,omitempty"`
	Meta          map[string]string `json:"meta,omitempty"`
}

// UnmarshalJSON decodes a Payload and rejects messages with zero or multiple
// content kinds set. Returns ErrInvalidOneof (wrapped with detail) on guard
// failure.
func (p *Payload) UnmarshalJSON(data []byte) error {
	var s payloadShadow
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	set := 0
	if s.Text != nil {
		set++
	}
	if s.URL != nil {
		set++
	}
	if s.File != nil {
		set++
	}
	if s.Blob != nil {
		set++
	}
	if set != 1 {
		return fmt.Errorf("%w: got %d", ErrInvalidOneof, set)
	}
	*p = Payload(s)
	return nil
}
