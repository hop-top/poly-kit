package wizardtui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/wizard"
)

func testTheme() cli.Theme {
	accent := lipgloss.Color("#7ED957")
	muted := lipgloss.Color("#6B7280")
	return cli.Theme{
		Palette:   cli.Neon,
		Accent:    accent,
		Secondary: lipgloss.Color("#FF00FF"),
		Muted:     muted,
		Error:     lipgloss.Color("#EF4444"),
		Success:   lipgloss.Color("#10B981"),
		Title:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")),
		Subtle:    lipgloss.NewStyle().Foreground(muted),
		Bold:      lipgloss.NewStyle().Bold(true),
	}
}

func testModel(t *testing.T, steps ...wizard.Step) model {
	t.Helper()
	w, err := wizard.New(steps...)
	require.NoError(t, err)
	return newModel(w, testTheme(), nil, func() {})
}

func TestTUIModel_Init(t *testing.T) {
	m := testModel(t,
		wizard.TextInput("name", "Your name"),
		wizard.Confirm("ok", "Continue?"),
	)

	cmd := m.Init()
	assert.Nil(t, cmd, "Init should return nil cmd")

	s := m.wizard.Current()
	require.NotNil(t, s)
	assert.Equal(t, "name", s.Key)
	assert.Equal(t, wizard.KindTextInput, s.Kind)
}

func TestTUIModel_TextInput_Enter(t *testing.T) {
	m := testModel(t,
		wizard.TextInput("name", "Your name"),
		wizard.Confirm("ok", "Continue?"),
	)

	// Type "alice".
	for _, ch := range "alice" {
		r, _ := m.Update(tea.KeyPressMsg{Code: rune(ch), Text: string(ch)})
		m = r.(model)
	}
	assert.Equal(t, "alice", m.textInput)

	// Press enter to advance.
	r, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = r.(model)

	s := m.wizard.Current()
	require.NotNil(t, s)
	assert.Equal(t, "ok", s.Key, "should advance to next step")
	assert.Equal(t, "", m.textInput, "input should reset")
}

func TestTUIModel_Select_ArrowEnter(t *testing.T) {
	opts := []wizard.Option{
		{Value: "go", Label: "Go"},
		{Value: "rs", Label: "Rust"},
		{Value: "py", Label: "Python"},
	}
	m := testModel(t,
		wizard.Select("lang", "Language", opts),
		wizard.Confirm("ok", "Continue?"),
	)
	assert.Equal(t, 0, m.cursor)

	// Arrow down twice.
	r, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = r.(model)
	r, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = r.(model)
	assert.Equal(t, 2, m.cursor)

	// Enter selects "py".
	r, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = r.(model)

	results := m.wizard.Results()
	assert.Equal(t, "py", results["lang"])
}

func TestTUIModel_Back_Esc(t *testing.T) {
	m := testModel(t,
		wizard.TextInput("name", "Name"),
		wizard.TextInput("email", "Email"),
	)

	// Type + advance past first step.
	for _, ch := range "bob" {
		r, _ := m.Update(tea.KeyPressMsg{Code: rune(ch), Text: string(ch)})
		m = r.(model)
	}
	r, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = r.(model)
	require.Equal(t, "email", m.wizard.Current().Key)

	// Esc goes back.
	r, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = r.(model)
	assert.Equal(t, "name", m.wizard.Current().Key)
}

func TestTUIModel_Abort_CtrlC(t *testing.T) {
	m := testModel(t,
		wizard.TextInput("name", "Name"),
	)

	r, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = r.(model)

	assert.True(t, m.aborted)
	assert.NotNil(t, cmd, "should return quit cmd")
}

func TestTUIModel_View_Header(t *testing.T) {
	m := testModel(t,
		wizard.TextInput("name", "Your name"),
	)

	v := m.View()
	out := v.Content
	assert.Contains(t, out, "Step 1 of 1")
	assert.Contains(t, out, "Your name")
	assert.Contains(t, out, "enter: next")
}

func TestTUIModel_MultiSelect_Toggle(t *testing.T) {
	opts := []wizard.Option{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Beta"},
	}
	m := testModel(t,
		wizard.MultiSelect("items", "Pick", opts),
		wizard.Confirm("ok", "Done?"),
	)

	// Toggle first item.
	r, _ := m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = r.(model)
	assert.True(t, m.selected[0])

	// Move down and toggle second.
	r, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = r.(model)
	r, _ = m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = r.(model)
	assert.True(t, m.selected[1])

	// Submit.
	r, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = r.(model)

	results := m.wizard.Results()
	got, ok := results["items"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"a", "b"}, got)
}

func TestTUIModel_Confirm_No(t *testing.T) {
	m := testModel(t,
		wizard.Confirm("ok", "Continue?"),
		wizard.TextInput("name", "Name"),
	)

	// Press "n" then enter → value is false.
	r, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = r.(model)
	assert.False(t, m.confirmVal)

	r, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = r.(model)

	results := m.wizard.Results()
	assert.Equal(t, false, results["ok"])
}

func TestTUIModel_Confirm_Default_False(t *testing.T) {
	step := wizard.Confirm("ok", "Continue?")
	step.DefaultValue = false

	m := testModel(t, step, wizard.TextInput("name", "Name"))
	assert.False(t, m.confirmVal)
}

func TestTUIModel_Select_Default(t *testing.T) {
	opts := []wizard.Option{
		{Value: "go", Label: "Go"},
		{Value: "rs", Label: "Rust"},
		{Value: "py", Label: "Python"},
	}
	step := wizard.Select("lang", "Language", opts)
	step.DefaultValue = "rs"

	m := testModel(t, step, wizard.Confirm("ok", "Done?"))
	// cursor should be at index 1 (Rust).
	assert.Equal(t, 1, m.cursor)
}

func TestTUIModel_View_MultiSelect_Hints(t *testing.T) {
	opts := []wizard.Option{{Value: "x", Label: "X"}}
	m := testModel(t,
		wizard.MultiSelect("items", "Pick", opts),
	)
	out := m.View().Content
	assert.Contains(t, out, "space: toggle")
}
