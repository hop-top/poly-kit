package markdown_test

import (
	"strings"
	"testing"

	"charm.land/glamour/v2/ansi"
	"hop.top/kit/go/console/markdown"
)

const testInput = "# Hello\n\nThis is **bold** and a [link](https://example.com).\n"

func TestRender_NonEmpty(t *testing.T) {
	out, err := markdown.Render(testInput, false)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("Render produced empty output")
	}
}

func TestRender_NoColor(t *testing.T) {
	out, err := markdown.Render(testInput, true)
	if err != nil {
		t.Fatalf("Render noColor: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("Render noColor produced empty output")
	}
	if strings.Contains(out, "\033[") {
		t.Errorf("noColor output contains ANSI escapes:\n%s", out)
	}
}

func TestRenderWith_CustomStyle(t *testing.T) {
	accent := "#FF5F87"
	bold := true
	style := ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockPrefix: "\n",
				BlockSuffix: "\n",
			},
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:       &accent,
				Bold:        &bold,
				BlockSuffix: "\n",
			},
		},
		Link: ansi.StylePrimitive{
			Color: &accent,
		},
	}

	out, err := markdown.RenderWith(testInput, style, false)
	if err != nil {
		t.Fatalf("RenderWith: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("RenderWith produced empty output")
	}
	// Custom style should produce ANSI escapes (color).
	if !strings.Contains(out, "\033[") {
		t.Error("RenderWith custom style did not produce ANSI escapes")
	}
}

func TestRenderWith_NoColor(t *testing.T) {
	out, err := markdown.RenderWith(testInput, ansi.StyleConfig{}, true)
	if err != nil {
		t.Fatalf("RenderWith noColor: %v", err)
	}
	if strings.Contains(out, "\033[") {
		t.Errorf("RenderWith noColor output contains ANSI escapes:\n%s", out)
	}
}
