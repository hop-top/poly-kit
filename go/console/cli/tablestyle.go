package cli

import (
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/output"
)

// TableStyle returns an output.TableStyle populated from the Root's Theme.
//
// Use it with output.WithTableStyle so styled tables follow the active
// CLI theme without callers re-declaring color choices at every call site:
//
//	output.Render(w, output.Table, rows, output.WithTableStyle(root.TableStyle()))
//
// The dependency direction is one-way: cli depends on output, never the
// reverse. output stays a leaf so it can be embedded in any tool without
// pulling in fang/cobra/identity transitively.
func (r *Root) TableStyle() output.TableStyle {
	return output.TableStyle{
		Border:           lipgloss.NormalBorder(),
		BorderForeground: r.Theme.Muted,
		Header:           r.Theme.Muted,
		Primary:          r.Theme.Accent,
		Secondary:        r.Theme.Secondary,
		Muted:            r.Theme.Muted,
	}
}
