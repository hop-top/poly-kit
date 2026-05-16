package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestParseModelString(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		wantName  string
		wantThres float64
		wantErr   bool
	}{
		{
			name:      "valid mf",
			model:     "router-mf-0.116",
			wantName:  "mf",
			wantThres: 0.116,
		},
		{
			name:      "valid bert",
			model:     "router-bert-0.7",
			wantName:  "bert",
			wantThres: 0.7,
		},
		{
			name:      "valid sw_ranking",
			model:     "router-sw_ranking-0.5",
			wantName:  "sw_ranking",
			wantThres: 0.5,
		},
		{
			name:      "threshold zero",
			model:     "router-random-0",
			wantName:  "random",
			wantThres: 0,
		},
		{
			name:      "threshold one",
			model:     "router-random-1",
			wantName:  "random",
			wantThres: 1,
		},
		{
			name:    "missing prefix",
			model:   "mf-0.5",
			wantErr: true,
		},
		{
			name:    "no threshold",
			model:   "router-mf",
			wantErr: true,
		},
		{
			name:    "invalid threshold",
			model:   "router-mf-abc",
			wantErr: true,
		},
		{
			name:    "threshold out of range high",
			model:   "router-mf-1.5",
			wantErr: true,
		},
		{
			name:      "hyphenated router name",
			model:     "router-sw-ranking-0.5",
			wantName:  "sw-ranking",
			wantThres: 0.5,
		},
		{
			name:    "empty string",
			model:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, threshold, err := ParseModelString(tt.model)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, name)
			assert.InDelta(t, tt.wantThres, threshold, 0.0001)
		})
	}
}

func TestController_Route(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("test", &stubRouter{score: 0.8})

	ctrl := NewController(reg, ModelPair{
		Strong: "gpt-4",
		Weak:   "gpt-3.5",
	})

	ctx := context.Background()
	sig := UserSignal{Text: "hello"}

	// Score 0.8 >= threshold 0.5 => strong
	model, err := ctrl.Route(ctx, sig, "test", 0.5)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4", model)

	// Score 0.8 < threshold 0.9 => weak
	model, err = ctrl.Route(ctx, sig, "test", 0.9)
	require.NoError(t, err)
	assert.Equal(t, "gpt-3.5", model)
}

func TestController_RouteFromModel(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("mf", &stubRouter{score: 0.3})

	ctrl := NewController(reg, ModelPair{
		Strong: "gpt-4",
		Weak:   "gpt-3.5",
	})

	ctx := context.Background()
	sig := UserSignal{Text: "hello"}

	model, err := ctrl.RouteFromModel(ctx, sig, "router-mf-0.5")
	require.NoError(t, err)
	assert.Equal(t, "gpt-3.5", model) // 0.3 < 0.5 => weak
}

func TestController_Route_InvalidRouter(t *testing.T) {
	reg := NewRegistry()
	ctrl := NewController(reg, ModelPair{Strong: "a", Weak: "b"})

	_, err := ctrl.Route(context.Background(), UserSignal{Text: "hello"}, "nonexistent", 0.5)
	assert.Error(t, err)
}

func TestController_Route_InvalidThreshold(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("test", &stubRouter{score: 0.5})
	ctrl := NewController(reg, ModelPair{Strong: "a", Weak: "b"})

	_, err := ctrl.Route(context.Background(), UserSignal{Text: "hello"}, "test", 1.5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of [0,1] range")
}

// stubMiddleware overrides model pair.
type stubMiddleware struct {
	pair *ModelPair
}

func (s *stubMiddleware) GetModelPair(
	_ context.Context, _ string,
) (*ModelPair, error) {
	return s.pair, nil
}

func TestController_Middleware(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("test", &stubRouter{score: 0.8})

	mw := &stubMiddleware{pair: &ModelPair{
		Strong: "claude-3",
		Weak:   "claude-haiku",
	}}

	ctrl := NewController(reg, ModelPair{
		Strong: "gpt-4",
		Weak:   "gpt-3.5",
	}, WithMiddleware(mw))

	ctx := context.Background()

	// Middleware overrides pair; 0.8 >= 0.5 => strong = claude-3
	model, err := ctrl.Route(ctx, UserSignal{Text: "hello"}, "test", 0.5)
	require.NoError(t, err)
	assert.Equal(t, "claude-3", model)
}

func TestController_ModelCounts(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("test", &stubRouter{score: 0.8})

	ctrl := NewController(reg, ModelPair{
		Strong: "gpt-4",
		Weak:   "gpt-3.5",
	})

	ctx := context.Background()
	_, _ = ctrl.Route(ctx, UserSignal{Text: "a"}, "test", 0.5) // strong
	_, _ = ctrl.Route(ctx, UserSignal{Text: "b"}, "test", 0.5) // strong
	_, _ = ctrl.Route(ctx, UserSignal{Text: "c"}, "test", 0.9) // weak

	counts := ctrl.ModelCounts()
	assert.Equal(t, 2, counts["test"]["gpt-4"])
	assert.Equal(t, 1, counts["test"]["gpt-3.5"])
}

func TestController_Complete_NoProvider(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("test", &stubRouter{score: 0.5})

	ctrl := NewController(reg, ModelPair{Strong: "a", Weak: "b"})

	_, err := ctrl.Complete(context.Background(), llm.Request{
		Model:    "router-test-0.5",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no provider configured")
}

func TestLastUserSignal(t *testing.T) {
	t.Run("text only", func(t *testing.T) {
		msgs := []llm.Message{
			{Role: "system", Content: "you are helpful"},
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "second question"},
		}
		sig := lastUserSignal(msgs)
		assert.Equal(t, "second question", sig.Text)
		assert.False(t, sig.HasImage)
		assert.False(t, sig.HasAudio)
		assert.False(t, sig.HasVideo)
	})

	t.Run("no user message falls back to last", func(t *testing.T) {
		msgs := []llm.Message{
			{Role: "system", Content: "system prompt"},
		}
		sig := lastUserSignal(msgs)
		assert.Equal(t, "system prompt", sig.Text)
	})

	t.Run("empty messages", func(t *testing.T) {
		sig := lastUserSignal(nil)
		assert.Equal(t, "", sig.Text)
	})

	t.Run("multimodal parts set flags", func(t *testing.T) {
		msgs := []llm.Message{
			{Role: "user", Parts: []llm.ContentPart{
				{Type: llm.PartTypeText, Text: "describe this"},
				{Type: llm.PartTypeImage},
			}},
		}
		sig := lastUserSignal(msgs)
		assert.Equal(t, "describe this", sig.Text)
		assert.True(t, sig.HasImage)
		assert.False(t, sig.HasAudio)
		assert.False(t, sig.HasVideo)
	})

	t.Run("audio and video flags", func(t *testing.T) {
		msgs := []llm.Message{
			{Role: "user", Parts: []llm.ContentPart{
				{Type: llm.PartTypeAudio},
				{Type: llm.PartTypeVideo},
			}},
		}
		sig := lastUserSignal(msgs)
		assert.True(t, sig.HasAudio)
		assert.True(t, sig.HasVideo)
		assert.False(t, sig.HasImage)
	})

	t.Run("parts text concatenated", func(t *testing.T) {
		msgs := []llm.Message{
			{Role: "user", Parts: []llm.ContentPart{
				{Type: llm.PartTypeText, Text: "hello"},
				{Type: llm.PartTypeImage},
				{Type: llm.PartTypeText, Text: " world"},
			}},
		}
		sig := lastUserSignal(msgs)
		assert.Equal(t, "hello world", sig.Text)
	})

	t.Run("Content used when Parts empty", func(t *testing.T) {
		msgs := []llm.Message{
			{Role: "user", Content: "plain text", Parts: nil},
		}
		sig := lastUserSignal(msgs)
		assert.Equal(t, "plain text", sig.Text)
		assert.False(t, sig.HasImage)
	})
}

func TestController_Route_ModalRouter(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("modal", &stubModalRouter{score: 0.9})

	ctrl := NewController(reg, ModelPair{
		Strong: "gpt-4-vision",
		Weak:   "gpt-3.5",
	})

	sig := UserSignal{Text: "describe", HasImage: true}
	model, err := ctrl.Route(context.Background(), sig, "modal", 0.5)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4-vision", model)
}

// stubModalRouter implements ModalRouter.
type stubModalRouter struct {
	score float64
}

func (s *stubModalRouter) Score(_ context.Context, _ string) (float64, error) {
	return 0.1, nil // low score via text-only path
}

func (s *stubModalRouter) ScoreSignal(_ context.Context, _ UserSignal) (float64, error) {
	return s.score, nil
}
