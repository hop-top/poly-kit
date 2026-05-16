package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShortLabel(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"tlc://project/T-0078", "T-0078"},
		{"aps://workspace/profile/noor", "noor"},
		{"tlc://host", "host"},
		{"plain-text", "plain-text"},
		{"tlc://", "tlc://"},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			assert.Equal(t, tt.want, shortLabel(tt.uri))
		})
	}
}

func TestLinkifyCell_NoURI(t *testing.T) {
	got := linkifyCell("hello world")
	assert.Equal(t, "hello world", got)
}

func TestLinkifyCell_WithTlcURI(t *testing.T) {
	val := "tlc://project/T-0078"
	got := linkifyCell(val)
	// When terminal doesn't support links, returns short label inline
	// When it does, wraps in OSC 8. Either way the URI text is processed.
	assert.NotEmpty(t, got)
	// Short label must appear in output regardless of terminal support
	assert.Contains(t, got, "T-0078")
}

func TestLinkifyCell_WithApsURI(t *testing.T) {
	val := "aps://ws/profile/noor"
	got := linkifyCell(val)
	assert.Contains(t, got, "noor")
}

func TestLinkifyCell_MixedContent(t *testing.T) {
	val := "Task tlc://project/T-0078 is active"
	got := linkifyCell(val)
	assert.Contains(t, got, "T-0078")
	assert.Contains(t, got, "Task")
	assert.Contains(t, got, "is active")
}

func TestRegisterLinkScheme(t *testing.T) {
	RegisterLinkScheme("hop")
	val := "hop://registry/kit"
	got := linkifyCell(val)
	assert.Contains(t, got, "kit")
}

func TestRegisterLinkScheme_Duplicate(t *testing.T) {
	before := len(uriSchemes)
	RegisterLinkScheme("tlc")
	assert.Equal(t, before, len(uriSchemes))
}

func TestURIPattern_Matches(t *testing.T) {
	tests := []struct {
		input string
		match bool
	}{
		{"tlc://project/T-0078", true},
		{"aps://ws/noor", true},
		{"https://example.com", false},
		{"no-uri-here", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.match, uriPattern.MatchString(tt.input))
		})
	}
}
