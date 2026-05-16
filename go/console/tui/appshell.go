package tui

// AppShell — a top-level bubbletea program shell for kit-based CLIs.
//
// # Design
//
// kit/console/tui ships components (anim, badge, confirm, list, pills,
// progress, status) but every adopter (aps, tlc, dpkms, …) hand-rolls
// the same boilerplate: a `tea.Model`, a `tea.NewProgram(...)` call,
// the WindowSize handling, the q/esc-to-quit keymap, a header/main/
// footer composition.
//
// AppShell hoists that into kit:
//
//   - frames the screen as header / main / footer (styles.Common)
//   - owns `tea.NewProgram` and the Init/Update/View loop
//   - delegates content rendering to a caller-supplied AppRenderer
//   - ships canonical keybinds (q/esc/ctrl+c quit; ?/h help) that are
//     overridable per app via WithKeyMap
//   - integrates with kit/cli Theme (NewAppShell takes cli.Theme; the
//     WithRoot helper plucks Theme from a configured *cli.Root)
//   - handles WindowSize/Suspend/Resume without per-app boilerplate
//
// # Migration overhead for adopters
//
// Apps stop owning a `tea.Model` and instead implement `AppRenderer`.
// Rough delta:
//
//   - Init/Update/View on Model → Init/Update/Render on AppRenderer
//   - tea.WindowSizeMsg branch → Resize(w, h) on AppRenderer
//   - q/esc/ctrl+c branch → drop; AppShell handles it
//   - tea.NewProgram(model).Run() → tui.NewAppShell(r, opts...).Run(ctx)
//
// AppRenderer.Render returns a plain string for the main region. The
// shell wraps it with a themed header and footer; both are caller-
// configurable via SetHeader / SetFooter on AppShell or by implementing
// the optional HeaderRenderer / FooterRenderer interfaces on the
// AppRenderer itself.
//
// # Bubbletea patterns the AppShell encapsulates
//
//   - tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
//   - tea.WindowSizeMsg → mutate `styles.Common` and forward to the
//     renderer via Resize(w, h)
//   - canonical key handling with per-app override (KeyMap)
//   - help-toggle state machine wired to a status-bar-style hint line
//   - Init() runs renderer.Init() if implemented; otherwise no-op
//   - Update() routes the message to the renderer (if Updater) after
//     handling its own concerns
//   - View() composes header / renderer.Render(width, height) / footer
//     in the alt-screen and pads to terminal size with lipgloss.Place

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/tui/styles"
)

// AppRenderer is implemented by an app that wants AppShell to own the
// program loop and frame rendering. It must produce the main region
// content; header and footer are supplied separately (and may be
// extended via the optional HeaderRenderer / FooterRenderer interfaces).
//
// Render receives the available width and height for the main region
// (already discounted for header/footer height by the shell).
type AppRenderer interface {
	Render(width, height int) string
}

// Initer is an optional capability for AppRenderer implementations that
// need to schedule initial commands (e.g. fetch, animation start).
// AppShell.Init() returns the result of this method when implemented,
// or nil otherwise.
type Initer interface {
	Init() tea.Cmd
}

// Updater is an optional capability for AppRenderer implementations
// that handle messages. AppShell.Update calls Updater.Update after its
// own handling (window-size + canonical keys), letting the renderer
// observe the post-shell state.
//
// Returning the same AppRenderer (no copy) is fine — the shell stores
// the returned value back as its renderer.
type Updater interface {
	Update(msg tea.Msg) (AppRenderer, tea.Cmd)
}

// Resizer is an optional capability for AppRenderer implementations
// that need to learn about the available main-region size eagerly
// (e.g. to rebuild a viewport). AppShell calls Resize on every
// tea.WindowSizeMsg before delegating to Updater.
type Resizer interface {
	Resize(width, height int) AppRenderer
}

// HeaderRenderer is an optional capability allowing the renderer to
// supply its own header text. Implementing this overrides any header
// text set via SetHeader.
type HeaderRenderer interface {
	Header(width int) string
}

// FooterRenderer is an optional capability allowing the renderer to
// supply its own footer text (e.g. a help/status line). Implementing
// this overrides any footer text set via SetFooter.
type FooterRenderer interface {
	Footer(width int) string
}

// KeyMap defines the canonical AppShell keybinds. Apps can override
// any subset via WithKeyMap; the zero value yields the defaults
// (q/esc/ctrl+c quit, ?/h help).
type KeyMap struct {
	Quit []string
	Help []string
}

// DefaultKeyMap returns the canonical keybinds. Adopters that want a
// strict subset (e.g. drop esc to free it for "back") can copy and
// edit before passing to WithKeyMap.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit: []string{"q", "esc", "ctrl+c"},
		Help: []string{"?", "h"},
	}
}

func (KeyMap) matches(key string, set []string) bool {
	for _, k := range set {
		if key == k {
			return true
		}
	}
	return false
}

// AppShell is a top-level tea.Model that frames an AppRenderer.
// Construct with NewAppShell; run with Run(ctx) or pass to
// tea.NewProgram directly for tests.
//
// AppShell follows kit's value-receiver / copy-on-write pattern: every
// mutator returns a copy.
type AppShell struct {
	renderer AppRenderer
	common   styles.Common
	keymap   KeyMap

	header   string
	footer   string
	showHelp bool
	helpText string

	altScreen bool
}

// AppShellOption configures an AppShell at construction time.
type AppShellOption func(*AppShell)

// WithKeyMap overrides the canonical keybinds. Pass DefaultKeyMap() as
// a starting point if you only want to extend.
func WithKeyMap(km KeyMap) AppShellOption {
	return func(a *AppShell) { a.keymap = km }
}

// WithHeader sets the initial header text. The renderer can override
// per-frame by implementing HeaderRenderer.
func WithHeader(text string) AppShellOption {
	return func(a *AppShell) { a.header = text }
}

// WithFooter sets the initial footer text. The renderer can override
// per-frame by implementing FooterRenderer.
func WithFooter(text string) AppShellOption {
	return func(a *AppShell) { a.footer = text }
}

// WithHelpText sets the text displayed when the help-toggle key is
// pressed. The default is a brief reminder of the canonical keys.
func WithHelpText(text string) AppShellOption {
	return func(a *AppShell) { a.helpText = text }
}

// WithSize sets the initial terminal size used until the first
// tea.WindowSizeMsg arrives. Most adopters never need this; useful
// for tests and headless rendering.
func WithSize(width, height int) AppShellOption {
	return func(a *AppShell) {
		a.common.Width = width
		a.common.Height = height
	}
}

// WithAltScreen toggles bubbletea's alt-screen behavior. Default is
// true. Pass WithAltScreen(false) for inline TUIs that should not
// take over the screen.
func WithAltScreen(on bool) AppShellOption {
	return func(a *AppShell) { a.altScreen = on }
}

// NewAppShell returns an AppShell wired to the renderer and theme.
// Default options: alt-screen on, canonical keymap, empty header/
// footer (renderer overrides win).
func NewAppShell(renderer AppRenderer, theme cli.Theme, opts ...AppShellOption) AppShell {
	a := AppShell{
		renderer:  renderer,
		common:    styles.NewCommon(theme, 80, 24),
		keymap:    DefaultKeyMap(),
		altScreen: true,
		helpText:  "  q/esc quit  ?/h help",
	}
	for _, opt := range opts {
		opt(&a)
	}
	// Eagerly hand the initial size to a Resizer so it can build any
	// internal viewports/lists before the first WindowSizeMsg.
	if r, ok := a.renderer.(Resizer); ok {
		a.renderer = r.Resize(a.common.Width, a.common.ContentHeight())
	}
	return a
}

// NewAppShellFromRoot is a convenience constructor that pulls the
// theme from a configured *cli.Root. Apps that already build a root
// for their CLI surface should prefer this so theme stays in sync
// with --accent / palette flags.
func NewAppShellFromRoot(renderer AppRenderer, root *cli.Root, opts ...AppShellOption) AppShell {
	return NewAppShell(renderer, root.Theme, opts...)
}

// Common returns the shared styles+dimensions context. Sub-renderers
// that need access to themed lipgloss styles can pull it from here.
func (a AppShell) Common() styles.Common { return a.common }

// Renderer returns the current AppRenderer. Useful in tests to
// retrieve the post-Update state.
func (a AppShell) Renderer() AppRenderer { return a.renderer }

// SetHeader returns a copy with the given header text. Overridden per
// frame by a HeaderRenderer if the renderer implements one.
func (a AppShell) SetHeader(text string) AppShell { a.header = text; return a }

// SetFooter returns a copy with the given footer text. Overridden per
// frame by a FooterRenderer if the renderer implements one.
func (a AppShell) SetFooter(text string) AppShell { a.footer = text; return a }

// Init satisfies tea.Model. Calls renderer.Init() if the renderer
// implements Initer, otherwise returns nil.
func (a AppShell) Init() tea.Cmd {
	if r, ok := a.renderer.(Initer); ok {
		return r.Init()
	}
	return nil
}

// Update satisfies tea.Model. It handles window-size, the canonical
// quit/help keys, then forwards the message to an Updater renderer.
func (a AppShell) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.common.Width = msg.Width
		a.common.Height = msg.Height
		if r, ok := a.renderer.(Resizer); ok {
			a.renderer = r.Resize(a.common.Width, a.common.ContentHeight())
		}
		// Fall through to renderer in case it also wants the raw
		// message via Updater.

	case tea.KeyPressMsg:
		key := msg.String()
		if a.keymap.matches(key, a.keymap.Quit) {
			return a, tea.Quit
		}
		if a.keymap.matches(key, a.keymap.Help) {
			a.showHelp = !a.showHelp
			return a, nil
		}
	}

	if r, ok := a.renderer.(Updater); ok {
		next, cmd := r.Update(msg)
		a.renderer = next
		return a, cmd
	}
	return a, nil
}

// View satisfies tea.Model. It composes the header, the renderer's
// main content, and the footer, padded to the terminal size.
func (a AppShell) View() tea.View {
	s := a.common.Styles
	w := a.common.Width
	contentH := a.common.ContentHeight()

	header := a.headerText(w)
	footer := a.footerText(w)

	main := a.renderer.Render(w, contentH)
	mainBox := s.Main.
		Width(w).
		Height(contentH).
		Render(main)

	out := strings.Join([]string{
		s.Header.Width(w).Render(header),
		mainBox,
		s.Footer.Width(w).Render(footer),
	}, "\n")

	v := tea.NewView(lipgloss.Place(w, a.common.Height, lipgloss.Left, lipgloss.Top, out))
	v.AltScreen = a.altScreen
	return v
}

// headerText resolves the header for one frame: HeaderRenderer wins
// over the static header set via WithHeader/SetHeader.
func (a AppShell) headerText(width int) string {
	if r, ok := a.renderer.(HeaderRenderer); ok {
		return r.Header(width)
	}
	return a.header
}

// footerText resolves the footer for one frame. Help mode preempts
// both renderer and static footer so adopters always have a way out.
func (a AppShell) footerText(width int) string {
	if a.showHelp && a.helpText != "" {
		return a.helpText
	}
	if r, ok := a.renderer.(FooterRenderer); ok {
		return r.Footer(width)
	}
	return a.footer
}

// Run wires the AppShell into a tea.NewProgram bound to ctx and runs
// it. Returns the final tea.Model and any error from the program.
//
// Run is the canonical entry point — adopters should not need to
// touch tea.NewProgram directly. For tests, the AppShell value also
// satisfies tea.Model and can be passed to tea.NewProgram or used
// with table-driven Update calls.
func (a AppShell) Run(ctx context.Context) (tea.Model, error) {
	progOpts := []tea.ProgramOption{tea.WithContext(ctx)}
	p := tea.NewProgram(a, progOpts...)
	return p.Run()
}
