package markdown_test

import (
	"strings"
	"testing"

	"charm.land/glamour/v2/ansi"
	"github.com/charmbracelet/colorprofile"
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
	t.Cleanup(markdown.SetProfileForTest(colorprofile.TrueColor))
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

func TestRender_AutoPlainWhenNotTerminal(t *testing.T) {
	for _, p := range []colorprofile.Profile{colorprofile.NoTTY, colorprofile.ASCII} {
		t.Run(p.String(), func(t *testing.T) {
			t.Cleanup(markdown.SetProfileForTest(p))
			out, err := markdown.Render(testInput, false)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.Contains(out, "\033[") {
				t.Errorf("Render emitted ANSI on non-color stdout:\n%q", out)
			}
			out, err = markdown.RenderWith(testInput, ansi.StyleConfig{}, false)
			if err != nil {
				t.Fatalf("RenderWith: %v", err)
			}
			if strings.Contains(out, "\033[") {
				t.Errorf("RenderWith emitted ANSI on non-color stdout:\n%q", out)
			}
		})
	}
}

func TestRender_AutoStyledOnColorTerminal(t *testing.T) {
	t.Cleanup(markdown.SetProfileForTest(colorprofile.TrueColor))
	out, err := markdown.Render(testInput, false)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "\033[") {
		t.Error("Render did not style on a color terminal")
	}
}

func TestRender_ExplicitNoColorForcesPlain(t *testing.T) {
	t.Cleanup(markdown.SetProfileForTest(colorprofile.TrueColor))
	out, err := markdown.Render(testInput, true)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "\033[") {
		t.Errorf("noColor=true emitted ANSI:\n%q", out)
	}
}
