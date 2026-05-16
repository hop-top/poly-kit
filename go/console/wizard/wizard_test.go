package wizard_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/wizard"
)

func TestNew_ValidSteps(t *testing.T) {
	w, err := wizard.New(
		wizard.TextInput("name", "Name"),
		wizard.Confirm("ok", "OK?"),
	)
	require.NoError(t, err)
	assert.NotNil(t, w)
}

func TestNew_DuplicateKey(t *testing.T) {
	_, err := wizard.New(
		wizard.TextInput("x", "A"),
		wizard.TextInput("x", "B"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestNew_BadDefaultType(t *testing.T) {
	_, err := wizard.New(
		wizard.Confirm("ok", "OK?").WithDefault("nope"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bool")
}

func TestNew_ActionWithoutFn(t *testing.T) {
	_, err := wizard.New(wizard.Step{
		Key: "act", Kind: wizard.KindAction, Label: "do",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ActionFn")
}

func TestNew_SelectWithoutOptions(t *testing.T) {
	_, err := wizard.New(wizard.Step{
		Key: "sel", Kind: wizard.KindSelect, Label: "pick",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "options")
}

func TestAdvance_TextInput(t *testing.T) {
	w, err := wizard.New(wizard.TextInput("name", "Name"))
	require.NoError(t, err)

	res, err := w.Advance("Alice")
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.Equal(t, "Alice", wizard.String(w.Results(), "name"))
	assert.True(t, w.Done())
}

func TestAdvance_Select(t *testing.T) {
	opts := []wizard.Option{
		{Value: "a", Label: "A"},
		{Value: "b", Label: "B"},
	}
	w, err := wizard.New(wizard.Select("color", "Color", opts))
	require.NoError(t, err)

	_, err = w.Advance("a")
	require.NoError(t, err)
	assert.Equal(t, "a", wizard.Choice(w.Results(), "color"))
}

func TestAdvance_Confirm(t *testing.T) {
	w, err := wizard.New(wizard.Confirm("ok", "Sure?"))
	require.NoError(t, err)

	_, err = w.Advance(true)
	require.NoError(t, err)
	assert.True(t, wizard.Bool(w.Results(), "ok"))
}

func TestAdvance_MultiSelect(t *testing.T) {
	opts := []wizard.Option{
		{Value: "x", Label: "X"},
		{Value: "y", Label: "Y"},
	}
	w, err := wizard.New(wizard.MultiSelect("tags", "Tags", opts))
	require.NoError(t, err)

	_, err = w.Advance([]string{"x", "y"})
	require.NoError(t, err)
	assert.Equal(t, []string{"x", "y"}, wizard.Strings(w.Results(), "tags"))
}

func TestAdvance_Action(t *testing.T) {
	called := false
	fn := func(_ context.Context, _ map[string]any) error {
		called = true
		return nil
	}
	w, err := wizard.New(wizard.Action("act", "Go", fn))
	require.NoError(t, err)

	res, err := w.Advance(nil)
	require.NoError(t, err)
	ar, ok := res.(*wizard.ActionRequest)
	require.True(t, ok)
	assert.Equal(t, "act", ar.StepKey)

	// Execute action and resolve.
	runErr := ar.Run(context.Background(), w.Results())
	require.NoError(t, w.ResolveAction(runErr))
	assert.True(t, called)
	assert.True(t, w.Done())
}

func TestAdvance_Validation(t *testing.T) {
	w, err := wizard.New(
		wizard.TextInput("email", "Email").
			WithValidateText(func(s string) error {
				if s == "bad" {
					return errors.New("invalid email")
				}
				return nil
			}),
	)
	require.NoError(t, err)

	_, err = w.Advance("bad")
	require.Error(t, err)
	var ve *wizard.ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "email", ve.StepKey)
}

func TestAdvance_Required(t *testing.T) {
	w, err := wizard.New(
		wizard.TextInput("name", "Name").WithRequired(),
	)
	require.NoError(t, err)

	_, err = w.Advance("")
	require.Error(t, err)
	var ve *wizard.ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Contains(t, ve.Error(), "required")
}

func TestResolveAction_Success(t *testing.T) {
	w, err := wizard.New(
		wizard.Action("a", "A", func(context.Context, map[string]any) error {
			return nil
		}),
		wizard.TextInput("b", "B"),
	)
	require.NoError(t, err)

	_, _ = w.Advance(nil) // get ActionRequest
	require.NoError(t, w.ResolveAction(nil))
	assert.Equal(t, "b", w.Current().Key)
}

func TestResolveAction_Abort(t *testing.T) {
	w, err := wizard.New(
		wizard.Action("a", "A", func(context.Context, map[string]any) error {
			return nil
		}).WithOnError(wizard.ActionAbort),
	)
	require.NoError(t, err)

	_, _ = w.Advance(nil)
	err = w.ResolveAction(errors.New("boom"))
	require.Error(t, err)
	var ae *wizard.ActionError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, wizard.ActionAbort, ae.Action)
}

func TestResolveAction_Retry(t *testing.T) {
	w, err := wizard.New(
		wizard.Action("a", "A", func(context.Context, map[string]any) error {
			return nil
		}).WithOnError(wizard.ActionRetry),
	)
	require.NoError(t, err)

	_, _ = w.Advance(nil)
	err = w.ResolveAction(errors.New("transient"))
	require.NoError(t, err)
	// Still on same step.
	assert.Equal(t, "a", w.Current().Key)
}

func TestResolveAction_Skip(t *testing.T) {
	w, err := wizard.New(
		wizard.Action("a", "A", func(context.Context, map[string]any) error {
			return nil
		}).WithOnError(wizard.ActionSkip),
		wizard.TextInput("b", "B"),
	)
	require.NoError(t, err)

	_, _ = w.Advance(nil)
	require.NoError(t, w.ResolveAction(errors.New("meh")))
	assert.Equal(t, "b", w.Current().Key)
}

func TestBack(t *testing.T) {
	w, err := wizard.New(
		wizard.TextInput("a", "A"),
		wizard.TextInput("b", "B"),
	)
	require.NoError(t, err)

	_, _ = w.Advance("hello")
	assert.Equal(t, "b", w.Current().Key)

	w.Back()
	assert.Equal(t, "a", w.Current().Key)
	// Result for "a" should be cleared.
	assert.Empty(t, wizard.String(w.Results(), "a"))
}

func TestBack_AtStart(t *testing.T) {
	w, err := wizard.New(wizard.TextInput("a", "A"))
	require.NoError(t, err)

	w.Back() // no-op
	assert.Equal(t, "a", w.Current().Key)
}

func TestConditional_When_True(t *testing.T) {
	w, err := wizard.New(
		wizard.Confirm("advanced", "Advanced?"),
		wizard.TextInput("extra", "Extra").
			WithWhen("advanced", func(v any) bool {
				b, _ := v.(bool)
				return b
			}),
	)
	require.NoError(t, err)

	_, _ = w.Advance(true)
	assert.Equal(t, "extra", w.Current().Key)
}

func TestConditional_When_False(t *testing.T) {
	w, err := wizard.New(
		wizard.Confirm("advanced", "Advanced?"),
		wizard.TextInput("extra", "Extra").
			WithWhen("advanced", func(v any) bool {
				b, _ := v.(bool)
				return b
			}),
	)
	require.NoError(t, err)

	_, _ = w.Advance(false)
	assert.True(t, w.Done())
}

func TestConditional_Back_OverSkipped(t *testing.T) {
	w, err := wizard.New(
		wizard.Confirm("show", "Show?"),
		wizard.TextInput("hidden", "Hidden").
			WithWhen("show", func(v any) bool {
				b, _ := v.(bool)
				return b
			}),
		wizard.TextInput("last", "Last"),
	)
	require.NoError(t, err)

	// show=false, hidden is skipped, land on "last".
	_, _ = w.Advance(false)
	assert.Equal(t, "last", w.Current().Key)

	// Back should skip "hidden" and land on "show".
	w.Back()
	assert.Equal(t, "show", w.Current().Key)
}

func TestDone(t *testing.T) {
	w, err := wizard.New(wizard.TextInput("a", "A"))
	require.NoError(t, err)

	assert.False(t, w.Done())
	_, _ = w.Advance("val")
	assert.True(t, w.Done())
}

func TestComplete_CallsOnComplete(t *testing.T) {
	w, err := wizard.New(wizard.TextInput("a", "A"))
	require.NoError(t, err)

	var got map[string]any
	w.SetOnComplete(func(r map[string]any) error {
		got = r
		return nil
	})

	_, _ = w.Advance("done")
	require.NoError(t, w.Complete())
	assert.Equal(t, "done", got["a"])
}

func TestComplete_DryRun(t *testing.T) {
	w, err := wizard.New(wizard.TextInput("a", "A"))
	require.NoError(t, err)

	called := false
	w.SetOnComplete(func(map[string]any) error {
		called = true
		return nil
	})
	w.SetDryRun(true)
	assert.True(t, w.DryRun())

	_, _ = w.Advance("val")
	require.NoError(t, w.Complete())
	assert.False(t, called)
}

func TestStepCount_ExcludesHidden(t *testing.T) {
	w, err := wizard.New(
		wizard.Confirm("show", "Show?"),
		wizard.TextInput("hidden", "Hidden").
			WithWhen("show", func(v any) bool {
				b, _ := v.(bool)
				return b
			}),
		wizard.TextInput("visible", "Visible"),
	)
	require.NoError(t, err)

	// Before answering, show defaults to nil → pred returns false.
	assert.Equal(t, 2, w.StepCount())

	_, _ = w.Advance(true)
	// Now hidden is visible.
	assert.Equal(t, 3, w.StepCount())
}

func TestNew_EmptyKey(t *testing.T) {
	_, err := wizard.New(wizard.Step{
		Key: "", Kind: wizard.KindTextInput, Label: "Name",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key must not be empty")
}

func TestAdvance_Select_InvalidOption(t *testing.T) {
	opts := []wizard.Option{
		{Value: "a", Label: "A"},
		{Value: "b", Label: "B"},
	}
	w, err := wizard.New(wizard.Select("color", "Color", opts))
	require.NoError(t, err)

	_, err = w.Advance("z")
	require.Error(t, err)
	var ve *wizard.ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Contains(t, ve.Error(), `invalid option "z"`)
}

func TestAdvance_MultiSelect_InvalidOption(t *testing.T) {
	opts := []wizard.Option{
		{Value: "x", Label: "X"},
		{Value: "y", Label: "Y"},
	}
	w, err := wizard.New(wizard.MultiSelect("tags", "Tags", opts))
	require.NoError(t, err)

	_, err = w.Advance([]string{"x", "nope"})
	require.Error(t, err)
	var ve *wizard.ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Contains(t, ve.Error(), `invalid option "nope"`)
}

func TestCurrent_ClearsStaleResults(t *testing.T) {
	w, err := wizard.New(
		wizard.Confirm("show", "Show?"),
		wizard.TextInput("extra", "Extra").
			WithWhen("show", func(v any) bool {
				b, _ := v.(bool)
				return b
			}),
		wizard.TextInput("last", "Last"),
	)
	require.NoError(t, err)

	// Answer show=true, fill extra.
	_, _ = w.Advance(true)
	assert.Equal(t, "extra", w.Current().Key)
	_, _ = w.Advance("filled")
	assert.Equal(t, "filled", w.Results()["extra"])

	// Go back to "show", answer false this time.
	w.Back() // back to extra
	w.Back() // back to show
	_, _ = w.Advance(false)

	// "extra" is now skipped; its stale result must be cleared.
	assert.Equal(t, "last", w.Current().Key)
	assert.Empty(t, w.Results()["extra"])
}

func TestResultAccessors(t *testing.T) {
	results := map[string]any{
		"name":  "Alice",
		"ok":    true,
		"tags":  []string{"a", "b"},
		"color": "red",
		"bad":   42, // wrong type for all accessors
	}

	assert.Equal(t, "Alice", wizard.String(results, "name"))
	assert.Equal(t, "", wizard.String(results, "missing"))
	assert.Equal(t, "", wizard.String(results, "bad"))

	assert.True(t, wizard.Bool(results, "ok"))
	assert.False(t, wizard.Bool(results, "missing"))
	assert.False(t, wizard.Bool(results, "bad"))

	assert.Equal(t, []string{"a", "b"}, wizard.Strings(results, "tags"))
	assert.Nil(t, wizard.Strings(results, "missing"))
	assert.Nil(t, wizard.Strings(results, "bad"))

	assert.Equal(t, "red", wizard.Choice(results, "color"))
}
