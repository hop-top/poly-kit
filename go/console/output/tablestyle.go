package output

import (
	"image/color"
	"sync"

	"charm.land/lipgloss/v2"
)

// TableStyle configures the lipgloss-backed styled table renderer enabled
// via WithTableStyle. Zero values for color fields render uncolored; a
// zero Border (all-empty Border struct) is replaced by lipgloss.NormalBorder
// at render time so callers can construct a TableStyle with only colors
// populated.
//
// Theme data is passed in as colors rather than a Theme reference so the
// output package stays a leaf — it never imports kit/console/cli. Adopters
// construct a TableStyle directly or via cli.Root.TableStyle().
type TableStyle struct {
	// Border is the box-drawing border used when rendering. When zero
	// (an empty lipgloss.Border literal), lipgloss.NormalBorder() is used.
	Border lipgloss.Border

	// BorderForeground colors the border itself.
	BorderForeground color.Color

	// Header colors the header row text.
	Header color.Color

	// Primary colors rows tagged with EmphasisPrimary.
	Primary color.Color

	// Secondary colors rows tagged with EmphasisSecondary.
	Secondary color.Color

	// Muted colors rows tagged with EmphasisMuted.
	Muted color.Color
}

var (
	defaultTableStyleMu sync.RWMutex
	defaultTableStyle   *TableStyle
)

// SetDefaultTableStyle installs the process-wide table style used by Render
// when callers render Table output without an explicit WithTableStyle option.
// It is primarily set by cli.New from the active CLI theme so kit-powered
// commands get consistent table styling without every command threading style
// options manually.
func SetDefaultTableStyle(s TableStyle) {
	defaultTableStyleMu.Lock()
	defer defaultTableStyleMu.Unlock()
	defaultTableStyle = &s
}

func getDefaultTableStyle() *TableStyle {
	defaultTableStyleMu.RLock()
	defer defaultTableStyleMu.RUnlock()
	if defaultTableStyle == nil {
		return nil
	}
	s := *defaultTableStyle
	return &s
}

// EmphasisKind identifies which themed color a row should render in when
// passed to RowEmphasis. Pattern mirrors tlc's WithPrimaryRows /
// WithSecondaryRows / WithMutedRows helpers.
type EmphasisKind int

const (
	// EmphasisNone renders the row with default (uncolored) text.
	EmphasisNone EmphasisKind = iota
	// EmphasisPrimary renders the row using TableStyle.Primary.
	EmphasisPrimary
	// EmphasisSecondary renders the row using TableStyle.Secondary.
	EmphasisSecondary
	// EmphasisMuted renders the row using TableStyle.Muted.
	EmphasisMuted
)

// RowEmphasis returns a RenderOption that marks rowIdx as having the given
// emphasis kind when rendered through the styled table path. Multiple
// RowEmphasis options compose; later calls for the same rowIdx win.
//
// Row indices are zero-based and match the order rows are passed to Render.
// EmphasisNone explicitly clears any prior emphasis on that row.
//
// When the styled path is not active (no WithTableStyle, or writer not a
// TTY), RowEmphasis is a no-op — the plain tabwriter renderer ignores it.
func RowEmphasis(rowIdx int, kind EmphasisKind) RenderOption {
	return func(c *renderConfig) {
		if c.rowEmphasis == nil {
			c.rowEmphasis = make(map[int]EmphasisKind)
		}
		c.rowEmphasis[rowIdx] = kind
	}
}

// WithTableStyle attaches a TableStyle to a Render call so the table
// formatter switches to the lipgloss-backed renderer when the writer is a
// TTY. Non-TTY writers (pipes, files, test buffers) keep using the
// existing tabwriter path so command output stays diff-friendly.
//
// Adopters construct a TableStyle from their kit/cli theme via
// cli.Root.TableStyle() — the output package never imports cli.
func WithTableStyle(s TableStyle) RenderOption {
	return func(c *renderConfig) { c.tableStyle = &s }
}

// isZeroBorder reports whether b is the zero Border literal. Used so a
// caller-provided TableStyle with no Border still gets a sensible default.
func isZeroBorder(b lipgloss.Border) bool {
	return b == lipgloss.Border{}
}
