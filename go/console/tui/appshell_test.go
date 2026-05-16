package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
)

// fakeRenderer is a minimal AppRenderer used across tests. It
// optionally implements Initer / Updater / Resizer / HeaderRenderer /
// FooterRenderer via the matching boolean toggles.
type fakeRenderer struct {
	main          string
	width, height int
	resizes       int

	initCmd tea.Cmd
	hasInit bool

	hasUpdate bool
	updates   int

	header string
	footer string
}

func (f fakeRenderer) Render(w, h int) string { return f.main }

// initerRenderer implements Initer.
type initerRenderer struct {
	fakeRenderer
	called *int
}

func (i initerRenderer) Init() tea.Cmd {
	if i.called != nil {
		*i.called++
	}
	return i.initCmd
}

// updaterRenderer implements Updater.
type updaterRenderer struct {
	fakeRenderer
	lastMsg *tea.Msg
}

func (u updaterRenderer) Update(msg tea.Msg) (tui.AppRenderer, tea.Cmd) {
	if u.lastMsg != nil {
		*u.lastMsg = msg
	}
	u.updates++
	return u, nil
}

// resizerRenderer implements Resizer.
type resizerRenderer struct {
	fakeRenderer
}

func (r resizerRenderer) Resize(w, h int) tui.AppRenderer {
	r.width, r.height = w, h
	r.resizes++
	return r
}

// headerFooterRenderer implements HeaderRenderer + FooterRenderer.
type headerFooterRenderer struct {
	fakeRenderer
}

func (h headerFooterRenderer) Header(width int) string { return h.header }
func (h headerFooterRenderer) Footer(width int) string { return h.footer }

// --- tests ---

func TestAppShell_NewSetsThemeAndDefaults(t *testing.T) {
	a := tui.NewAppShell(fakeRenderer{main: "x"}, testTheme())

	assert.Equal(t, 80, a.Common().Width)
	assert.Equal(t, 24, a.Common().Height)
}

func TestAppShell_View_HasMainContent(t *testing.T) {
	a := tui.NewAppShell(fakeRenderer{main: "hello world"}, testTheme())
	v := a.View()
	require.NotEmpty(t, v.Content)
	assert.Contains(t, v.Content, "hello world")
}

func TestAppShell_WithHeaderAndFooter(t *testing.T) {
	a := tui.NewAppShell(
		fakeRenderer{main: "main content"},
		testTheme(),
		tui.WithHeader("HDR"),
		tui.WithFooter("FTR"),
	)
	v := a.View()
	assert.Contains(t, v.Content, "HDR")
	assert.Contains(t, v.Content, "FTR")
}

func TestAppShell_HeaderFooterRendererOverride(t *testing.T) {
	r := headerFooterRenderer{
		fakeRenderer: fakeRenderer{main: "M"},
		// header/footer set via fakeRenderer fields.
	}
	r.header = "RENDERER-H"
	r.footer = "RENDERER-F"

	a := tui.NewAppShell(r, testTheme(),
		tui.WithHeader("static-h"), tui.WithFooter("static-f"))
	v := a.View()
	assert.Contains(t, v.Content, "RENDERER-H")
	assert.Contains(t, v.Content, "RENDERER-F")
	assert.NotContains(t, v.Content, "static-h")
	assert.NotContains(t, v.Content, "static-f")
}

func TestAppShell_Update_WindowSize(t *testing.T) {
	a := tui.NewAppShell(fakeRenderer{}, testTheme())
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	ua := updated.(tui.AppShell)
	assert.Equal(t, 120, ua.Common().Width)
	assert.Equal(t, 40, ua.Common().Height)
}

func TestAppShell_Update_QuitKeys(t *testing.T) {
	cases := []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: tea.KeyEscape},
		{Code: 'c', Mod: tea.ModCtrl},
	}
	for _, msg := range cases {
		a := tui.NewAppShell(fakeRenderer{}, testTheme())
		_, cmd := a.Update(msg)
		require.NotNil(t, cmd, "expected tea.Quit cmd for key %v", msg)
		// Invoking the cmd produces a tea.QuitMsg.
		if cmd != nil {
			_, ok := cmd().(tea.QuitMsg)
			assert.True(t, ok, "expected QuitMsg for key %v", msg)
		}
	}
}

func TestAppShell_Update_HelpToggle(t *testing.T) {
	a := tui.NewAppShell(fakeRenderer{main: "x"}, testTheme(),
		tui.WithHelpText("HELPLINE"))
	// First press shows help.
	updated, _ := a.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	ua := updated.(tui.AppShell)
	v := ua.View()
	assert.Contains(t, v.Content, "HELPLINE")
	// Second press hides help.
	updated2, _ := ua.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	ua2 := updated2.(tui.AppShell)
	v2 := ua2.View()
	assert.NotContains(t, v2.Content, "HELPLINE")
}

func TestAppShell_Update_DelegatesToUpdater(t *testing.T) {
	var received tea.Msg
	r := updaterRenderer{
		fakeRenderer: fakeRenderer{main: "X"},
		lastMsg:      &received,
	}
	a := tui.NewAppShell(r, testTheme())
	// Send a non-canonical key — the shell should pass it on.
	a.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	assert.NotNil(t, received)
}

func TestAppShell_Init_DelegatesToIniter(t *testing.T) {
	calls := 0
	r := initerRenderer{
		fakeRenderer: fakeRenderer{main: "X"},
		called:       &calls,
	}
	a := tui.NewAppShell(r, testTheme())
	a.Init()
	assert.Equal(t, 1, calls)
}

func TestAppShell_Init_NoIniter(t *testing.T) {
	a := tui.NewAppShell(fakeRenderer{}, testTheme())
	cmd := a.Init()
	assert.Nil(t, cmd)
}

func TestAppShell_Resize_OnConstruction(t *testing.T) {
	r := resizerRenderer{fakeRenderer: fakeRenderer{main: "X"}}
	a := tui.NewAppShell(r, testTheme(), tui.WithSize(100, 30))
	got := a.Renderer().(resizerRenderer)
	assert.Equal(t, 100, got.width)
	// Content height = 30 - HeaderHeight - FooterHeight.
	assert.Equal(t, 30-2, got.height)
}

func TestAppShell_Resize_OnWindowSizeMsg(t *testing.T) {
	r := resizerRenderer{fakeRenderer: fakeRenderer{main: "X"}}
	a := tui.NewAppShell(r, testTheme())
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	ua := updated.(tui.AppShell)
	got := ua.Renderer().(resizerRenderer)
	assert.Equal(t, 60, got.width)
	assert.Equal(t, 20-2, got.height)
}

func TestAppShell_WithKeyMap_OverridesQuit(t *testing.T) {
	// Map "x" to quit, drop esc.
	km := tui.KeyMap{Quit: []string{"x"}, Help: []string{"?"}}
	a := tui.NewAppShell(fakeRenderer{}, testTheme(), tui.WithKeyMap(km))

	// "q" no longer quits — should be forwarded to renderer (no-op).
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.Nil(t, cmd)

	// "x" quits.
	_, cmd2 := a.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.NotNil(t, cmd2)
}

func TestAppShell_DefaultKeyMap_HasCanonicalKeys(t *testing.T) {
	km := tui.DefaultKeyMap()
	assert.Contains(t, km.Quit, "q")
	assert.Contains(t, km.Quit, "esc")
	assert.Contains(t, km.Quit, "ctrl+c")
	assert.Contains(t, km.Help, "?")
	assert.Contains(t, km.Help, "h")
}

func TestAppShell_AltScreenDefaultsOn(t *testing.T) {
	a := tui.NewAppShell(fakeRenderer{main: "x"}, testTheme())
	v := a.View()
	assert.True(t, v.AltScreen)
}

func TestAppShell_AltScreenOff(t *testing.T) {
	a := tui.NewAppShell(fakeRenderer{main: "x"}, testTheme(),
		tui.WithAltScreen(false))
	v := a.View()
	assert.False(t, v.AltScreen)
}

func TestAppShell_View_PadsToHeight(t *testing.T) {
	a := tui.NewAppShell(fakeRenderer{main: "tiny"}, testTheme(),
		tui.WithSize(40, 10))
	v := a.View()
	// Place pads to height — count lines.
	lines := strings.Count(v.Content, "\n")
	assert.GreaterOrEqual(t, lines, 9, "expected at least height-1 newlines")
}

// _ ensures static interface conformance.
var (
	_ tea.Model          = tui.AppShell{}
	_ tui.AppRenderer    = fakeRenderer{}
	_ tui.Initer         = initerRenderer{}
	_ tui.Updater        = updaterRenderer{}
	_ tui.Resizer        = resizerRenderer{}
	_ tui.HeaderRenderer = headerFooterRenderer{}
	_ tui.FooterRenderer = headerFooterRenderer{}
)
