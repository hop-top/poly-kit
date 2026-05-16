// Package wizardtui provides a bubbletea v2 frontend for the wizard engine.
// It lives in a separate subpackage to isolate bubbletea transitive deps from
// consumers who only need the line/headless frontends.
package wizardtui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/tui"
	"hop.top/kit/go/console/wizard"
)

// Frontend implements the TUIRunner interface from the wizard package.
type Frontend struct{}

// NewFrontend returns a ready Frontend.
func NewFrontend() *Frontend { return &Frontend{} }

// Run drives the wizard to completion using bubbletea.
func (f *Frontend) Run(
	ctx context.Context, w *wizard.Wizard, theme cli.Theme,
) error {
	return RunTUI(ctx, w, theme)
}

// RunTUI executes the wizard as a bubbletea program.
func RunTUI(
	ctx context.Context, w *wizard.Wizard, theme cli.Theme,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := newModel(w, theme, ctx, cancel)
	p := tea.NewProgram(m)

	final, err := p.Run()
	if err != nil {
		return err
	}
	fm := final.(model)
	if fm.aborted {
		return &wizard.AbortError{}
	}
	return fm.err
}

type actionDoneMsg struct{ err error }

type model struct {
	wizard     *wizard.Wizard
	theme      cli.Theme
	ctx        context.Context
	cancel     context.CancelFunc
	textInput  string       // current text for TextInput kind
	cursor     int          // selected row for Select/MultiSelect
	selected   map[int]bool // toggled items for MultiSelect
	confirmVal bool         // current confirm answer
	err        error        // last validation/action error
	running    bool         // action in progress
	spinner    spinner.Model
	width      int
	height     int
	aborted    bool
}

func newModel(
	w *wizard.Wizard, theme cli.Theme,
	ctx context.Context, cancel context.CancelFunc,
) model {
	m := model{
		wizard:   w,
		theme:    theme,
		ctx:      ctx,
		cancel:   cancel,
		selected: make(map[int]bool),
		spinner:  tui.NewSpinner(theme),
	}
	if s := w.Current(); s != nil {
		m.initStepState(s)
	}
	return m
}

// Init satisfies tea.Model.
func (m model) Init() tea.Cmd { return nil }

// Update satisfies tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case actionDoneMsg:
		m.running = false
		if resolveErr := m.wizard.ResolveAction(msg.err); resolveErr != nil {
			m.err = resolveErr
			return m, tea.Quit
		}
		if m.wizard.Done() {
			return m, tea.Quit
		}
		m.resetInput()
		return m, nil

	case spinner.TickMsg:
		if m.running {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.running {
		return m, nil
	}

	s := m.wizard.Current()
	if s == nil {
		return m, tea.Quit
	}

	switch key.String() {
	case "ctrl+c":
		m.aborted = true
		m.cancel()
		return m, tea.Quit

	case "enter":
		return m.submit(s)

	case "esc":
		m.wizard.Back()
		m.err = nil
		m.resetInput()
		return m, nil

	case "up", "k":
		if s.Kind == wizard.KindSelect || s.Kind == wizard.KindMultiSelect {
			if m.cursor > 0 {
				m.cursor--
			}
		}
		return m, nil

	case "down", "j":
		if s.Kind == wizard.KindSelect || s.Kind == wizard.KindMultiSelect {
			if m.cursor < len(s.Options)-1 {
				m.cursor++
			}
		}
		return m, nil

	case "space", " ":
		if s.Kind == wizard.KindMultiSelect {
			m.selected[m.cursor] = !m.selected[m.cursor]
		}
		return m, nil

	case "backspace":
		if s.Kind == wizard.KindTextInput {
			if len(m.textInput) > 0 {
				m.textInput = m.textInput[:len(m.textInput)-1]
			} else {
				m.wizard.Back()
				m.err = nil
				m.resetInput()
			}
		}
		return m, nil

	case "y", "Y":
		if s.Kind == wizard.KindConfirm {
			m.confirmVal = true
		}
		return m, nil

	case "n", "N":
		if s.Kind == wizard.KindConfirm {
			m.confirmVal = false
		}
		return m, nil

	default:
		if s.Kind == wizard.KindTextInput && len(key.Text) > 0 {
			m.textInput += key.Text
		}
		return m, nil
	}
}

func (m model) submit(s *wizard.Step) (tea.Model, tea.Cmd) {
	var value any

	switch s.Kind {
	case wizard.KindTextInput:
		v := m.textInput
		if v == "" && s.DefaultValue != nil {
			if dv, ok := s.DefaultValue.(string); ok {
				v = dv
			}
		}
		value = v

	case wizard.KindSelect:
		if len(s.Options) > 0 {
			value = s.Options[m.cursor].Value
		}

	case wizard.KindConfirm:
		value = m.confirmVal

	case wizard.KindMultiSelect:
		var choices []string
		for i, opt := range s.Options {
			if m.selected[i] {
				choices = append(choices, opt.Value)
			}
		}
		value = choices

	case wizard.KindAction, wizard.KindSummary:
		value = nil
	}

	result, err := m.wizard.Advance(value)
	if err != nil {
		if ve, ok := err.(*wizard.ValidationError); ok {
			m.err = ve
			return m, nil
		}
		m.err = err
		return m, tea.Quit
	}

	if ar, ok := result.(*wizard.ActionRequest); ok {
		m.running = true
		m.err = nil
		return m, tea.Batch(m.spinner.Tick, m.runAction(ar))
	}

	if m.wizard.Done() {
		return m, tea.Quit
	}
	m.resetInput()
	return m, nil
}

func (m model) runAction(ar *wizard.ActionRequest) tea.Cmd {
	return func() tea.Msg {
		err := ar.Run(m.ctx, m.wizard.Results())
		return actionDoneMsg{err: err}
	}
}

func (m *model) resetInput() {
	if s := m.wizard.Current(); s != nil {
		m.initStepState(s)
	} else {
		m.textInput = ""
		m.cursor = 0
		m.selected = make(map[int]bool)
		m.confirmVal = false
		m.err = nil
	}
}

func (m *model) initStepState(s *wizard.Step) {
	m.textInput = ""
	m.cursor = 0
	m.selected = make(map[int]bool)
	m.confirmVal = false
	m.err = nil

	switch s.Kind {
	case wizard.KindConfirm:
		if dv, ok := s.DefaultValue.(bool); ok {
			m.confirmVal = dv
		}
	case wizard.KindSelect:
		if dv, ok := s.DefaultValue.(string); ok {
			for i, opt := range s.Options {
				if opt.Value == dv {
					m.cursor = i
					break
				}
			}
		}
	case wizard.KindMultiSelect:
		if dv, ok := s.DefaultValue.([]string); ok {
			vals := make(map[string]bool, len(dv))
			for _, v := range dv {
				vals[v] = true
			}
			for i, opt := range s.Options {
				if vals[opt.Value] {
					m.selected[i] = true
				}
			}
		}
	}
}

// --- view ---

// View satisfies tea.Model.
func (m model) View() tea.View {
	s := m.wizard.Current()
	if s == nil {
		return tea.NewView("")
	}

	var b strings.Builder
	// Header.
	header := fmt.Sprintf("Step %d of %d", m.wizard.StepIndex()+1, m.wizard.StepCount())
	b.WriteString(lipgloss.NewStyle().Foreground(m.theme.Accent).Render(header))
	b.WriteByte('\n')
	// Group separator.
	if s.Group != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(m.theme.Secondary).
			Render(fmt.Sprintf("── %s ──", s.Group)))
		b.WriteByte('\n')
	}
	// Label.
	b.WriteString(m.theme.Bold.Render(s.Label))
	if s.Description != "" {
		b.WriteByte('\n')
		b.WriteString(lipgloss.NewStyle().
			Foreground(m.theme.Muted).Render(s.Description))
	}
	b.WriteByte('\n')

	// Body per kind.
	switch s.Kind {
	case wizard.KindTextInput:
		m.viewTextInput(&b, s)
	case wizard.KindSelect:
		m.viewSelect(&b, s)
	case wizard.KindConfirm:
		m.viewConfirm(&b)
	case wizard.KindMultiSelect:
		m.viewMultiSelect(&b, s)
	case wizard.KindAction:
		m.viewAction(&b)
	case wizard.KindSummary:
		m.viewSummary(&b)
	}

	// Validation error.
	if m.err != nil {
		errStyle := lipgloss.NewStyle().Foreground(m.theme.Error)
		b.WriteByte('\n')
		b.WriteString(errStyle.Render(m.err.Error()))
	}

	// Footer: key hints.
	b.WriteByte('\n')
	b.WriteString(m.viewHints(s))

	return tea.NewView(b.String())
}

func (m model) viewTextInput(b *strings.Builder, s *wizard.Step) {
	prompt := "> " + m.textInput + "_"
	if m.textInput == "" && s.DefaultValue != nil {
		if dv, ok := s.DefaultValue.(string); ok {
			prompt = "> " + lipgloss.NewStyle().
				Foreground(m.theme.Muted).Render(dv) + "_"
		}
	}
	b.WriteString(prompt)
}

func (m model) viewSelect(b *strings.Builder, s *wizard.Step) {
	cur := lipgloss.NewStyle().Foreground(m.theme.Accent)
	desc := lipgloss.NewStyle().Foreground(m.theme.Muted)
	for i, opt := range s.Options {
		if i > 0 {
			b.WriteByte('\n')
		}
		prefix := "  "
		if i == m.cursor {
			prefix = cur.Render("> ")
		}
		b.WriteString(prefix + opt.Label)
		if opt.Description != "" {
			b.WriteString(" " + desc.Render(opt.Description))
		}
	}
}

func (m model) viewConfirm(b *strings.Builder) {
	muted := lipgloss.NewStyle().Foreground(m.theme.Muted)
	accent := lipgloss.NewStyle().Foreground(m.theme.Accent)
	if m.confirmVal {
		b.WriteString(accent.Render("Yes") + muted.Render(" / no"))
	} else {
		b.WriteString(muted.Render("yes / ") + accent.Render("No"))
	}
}

func (m model) viewMultiSelect(b *strings.Builder, s *wizard.Step) {
	cur := lipgloss.NewStyle().Foreground(m.theme.Accent)
	chk := lipgloss.NewStyle().Foreground(m.theme.Success)
	for i, opt := range s.Options {
		if i > 0 {
			b.WriteByte('\n')
		}
		prefix := "  "
		if i == m.cursor {
			prefix = cur.Render("> ")
		}
		box := "[ ]"
		if m.selected[i] {
			box = chk.Render("[x]")
		}
		b.WriteString(prefix + box + " " + opt.Label)
	}
}

func (m model) viewAction(b *strings.Builder) {
	if m.running {
		b.WriteString(m.spinner.View() + " Running...")
	}
}

func (m model) viewSummary(b *strings.Builder) {
	results := m.wizard.Results()
	s := m.wizard.Current()
	if s != nil && s.FormatFn != nil {
		b.WriteString(s.FormatFn(results))
		return
	}
	keys := sortedVisibleKeys(results)
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Accent)
	maxLen := 0
	for _, k := range keys {
		if len(k) > maxLen {
			maxLen = len(k)
		}
	}
	for _, k := range keys {
		fmt.Fprintf(b, "  %s  %v\n",
			keyStyle.Render(fmt.Sprintf("%-*s", maxLen+1, k+":")),
			results[k])
	}
}

func sortedVisibleKeys(results map[string]any) []string {
	keys := make([]string, 0, len(results))
	for k := range results {
		if !strings.HasPrefix(k, "__") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (m model) viewHints(s *wizard.Step) string {
	mutedStyle := lipgloss.NewStyle().Foreground(m.theme.Muted)
	hints := "enter: next  esc: back  ctrl+c: quit"
	if s.Kind == wizard.KindMultiSelect {
		hints = "enter: next  space: toggle  esc: back  ctrl+c: quit"
	}
	return mutedStyle.Render(hints)
}
