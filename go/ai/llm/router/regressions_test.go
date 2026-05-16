package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// capturingRouter records the text passed to Score.
type capturingRouter struct {
	score    float64
	lastText string
}

func (r *capturingRouter) Score(_ context.Context, text string) (float64, error) {
	r.lastText = text
	return r.score, nil
}

// capturingModalRouter records the full UserSignal passed to ScoreSignal.
type capturingModalRouter struct {
	score   float64
	lastSig UserSignal
	// scoreCalled tracks whether Score (text-only) was called
	scoreCalled bool
}

func (r *capturingModalRouter) Score(_ context.Context, _ string) (float64, error) {
	r.scoreCalled = true
	return 0, nil
}

func (r *capturingModalRouter) ScoreSignal(_ context.Context, sig UserSignal) (float64, error) {
	r.lastSig = sig
	return r.score, nil
}

// TestRegression_TextOnlyBackwardCompat verifies plain Router receives text
// via Score when routing with UserSignal (T-0733 backward compat).
func TestRegression_TextOnlyBackwardCompat(t *testing.T) {
	cr := &capturingRouter{score: 0.6}
	reg := NewRegistry()
	require.NoError(t, reg.Register("plain", cr))

	ctrl := NewController(reg, ModelPair{Strong: "strong", Weak: "weak"})
	sig := UserSignal{Text: "explain quantum physics"}

	model, err := ctrl.Route(context.Background(), sig, "plain", 0.5)
	require.NoError(t, err)
	assert.Equal(t, "strong", model)
	assert.Equal(t, "explain quantum physics", cr.lastText)
}

// TestRegression_ModalRouterDispatch verifies ModalRouter receives full
// UserSignal via ScoreSignal, not the text-only Score path (T-0733).
func TestRegression_ModalRouterDispatch(t *testing.T) {
	mr := &capturingModalRouter{score: 0.9}
	reg := NewRegistry()
	require.NoError(t, reg.Register("modal", mr))

	ctrl := NewController(reg, ModelPair{Strong: "vision", Weak: "basic"})
	sig := UserSignal{Text: "describe this", HasImage: true}

	model, err := ctrl.Route(context.Background(), sig, "modal", 0.5)
	require.NoError(t, err)
	assert.Equal(t, "vision", model)
	assert.Equal(t, sig, mr.lastSig)
	assert.False(t, mr.scoreCalled, "Score() must not be called on ModalRouter")
}

// TestRegression_ImageFlagDetection verifies lastUserSignal sets HasImage
// when message contains an image ContentPart (T-0733).
func TestRegression_ImageFlagDetection(t *testing.T) {
	msgs := []llm.Message{{
		Role: "user",
		Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "what is this?"},
			{Type: llm.PartTypeImage},
		},
	}}

	sig := lastUserSignal(msgs)
	assert.Equal(t, "what is this?", sig.Text)
	assert.True(t, sig.HasImage)
	assert.False(t, sig.HasAudio)
	assert.False(t, sig.HasVideo)
}

// TestRegression_MixedContent verifies lastUserSignal sets all appropriate
// flags for a message with text + image + audio parts (T-0733).
func TestRegression_MixedContent(t *testing.T) {
	msgs := []llm.Message{{
		Role: "user",
		Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "transcribe and describe"},
			{Type: llm.PartTypeImage},
			{Type: llm.PartTypeAudio},
		},
	}}

	sig := lastUserSignal(msgs)
	assert.Equal(t, "transcribe and describe", sig.Text)
	assert.True(t, sig.HasImage)
	assert.True(t, sig.HasAudio)
	assert.False(t, sig.HasVideo)
}

// TestRegression_EmptyPartsFallback verifies lastUserSignal uses Content
// field when Parts is empty (T-0733 backward compat).
func TestRegression_EmptyPartsFallback(t *testing.T) {
	msgs := []llm.Message{{
		Role:    "user",
		Content: "plain text message",
		Parts:   nil,
	}}

	sig := lastUserSignal(msgs)
	assert.Equal(t, "plain text message", sig.Text)
	assert.False(t, sig.HasImage)
	assert.False(t, sig.HasAudio)
	assert.False(t, sig.HasVideo)
}

// TestRegression_SingleRegistryLookup verifies Route succeeds for a valid
// router, proving the refactored validateRouterThreshold works without a
// redundant registry lookup (T-0733).
func TestRegression_SingleRegistryLookup(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register("test", &stubRouter{score: 0.5}))

	ctrl := NewController(reg, ModelPair{Strong: "s", Weak: "w"})

	model, err := ctrl.Route(context.Background(), UserSignal{Text: "hi"}, "test", 0.5)
	require.NoError(t, err)
	assert.Equal(t, "s", model)

	// nonexistent router still errors
	_, err = ctrl.Route(context.Background(), UserSignal{Text: "hi"}, "missing", 0.5)
	assert.Error(t, err)
}
