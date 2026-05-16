package kitinit_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
)

func TestAsk_TextWithDefault_EmptyInput(t *testing.T) {
	in := bytes.NewBufferString("\n")
	out := &bytes.Buffer{}
	w := kitinit.NewTTYWizard(in, out)
	got, err := w.Ask("name", "Project name", "myapp", nil)
	require.NoError(t, err)
	assert.Equal(t, "myapp", got)
	assert.Contains(t, out.String(), "Project name [myapp]:")
}

func TestAsk_TextWithDefault_OverrideInput(t *testing.T) {
	in := bytes.NewBufferString("other\n")
	out := &bytes.Buffer{}
	w := kitinit.NewTTYWizard(in, out)
	got, err := w.Ask("name", "Project name", "myapp", nil)
	require.NoError(t, err)
	assert.Equal(t, "other", got)
}

func TestAsk_Choice_NumericSelection(t *testing.T) {
	in := bytes.NewBufferString("2\n")
	out := &bytes.Buffer{}
	w := kitinit.NewTTYWizard(in, out)
	got, err := w.Ask("kind", "Pick one", "", []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "b", got)
	assert.Contains(t, out.String(), "1) a")
	assert.Contains(t, out.String(), "2) b")
	assert.Contains(t, out.String(), "3) c")
}

func TestAsk_Choice_TextSelection(t *testing.T) {
	in := bytes.NewBufferString("b\n")
	out := &bytes.Buffer{}
	w := kitinit.NewTTYWizard(in, out)
	got, err := w.Ask("kind", "Pick one", "", []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "b", got)
}

func TestAsk_Choice_DefaultOnEmpty(t *testing.T) {
	in := bytes.NewBufferString("\n")
	out := &bytes.Buffer{}
	w := kitinit.NewTTYWizard(in, out)
	got, err := w.Ask("kind", "Pick one", "a", []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "a", got)
	// default marker rendered
	assert.Contains(t, out.String(), "* 1) a")
}

func TestAsk_Choice_InvalidThenValid(t *testing.T) {
	in := bytes.NewBufferString("garbage\n2\n")
	out := &bytes.Buffer{}
	w := kitinit.NewTTYWizard(in, out)
	got, err := w.Ask("kind", "Pick one", "", []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "b", got)
	assert.Contains(t, out.String(), `Invalid selection "garbage"`)
}
