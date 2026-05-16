package qmochi

// GetVerticalPalette returns the glyphs for vertical fills (columns, sparklines).
// Index 0 = empty, index 8 = full. Each style uses distinct characters.
func GetVerticalPalette(style BlockStyle) []string {
	switch style {
	case DottedBlock:
		return []string{" ", "·", "·", "·", "·", "·", "·", "·", "·"}
	case DashedBlock:
		return []string{" ", "╶", "╶", "─", "─", "╼", "╼", "╸", "━"}
	case RoundedBlock:
		return []string{" ", "▂", "▃", "▄", "▅", "▆", "▇", "█", "█"}
	case ShadedBlock:
		return []string{" ", "░", "░", "▒", "▒", "▓", "▓", "▓", "▓"}
	case SolidBlock:
		fallthrough
	default:
		return []string{" ", "▂", "▃", "▄", "▅", "▆", "▇", "█", "█"}
	}
}

// GetHorizontalPalette returns the glyphs for horizontal fills (bars).
// Index 0 = empty, index 8 = full. Each style uses distinct characters.
func GetHorizontalPalette(style BlockStyle) []string {
	switch style {
	case DottedBlock:
		return []string{" ", "·", "·", "·", "·", "·", "·", "·", "·"}
	case DashedBlock:
		return []string{" ", "╶", "╶", "─", "─", "╼", "╼", "╸", "━"}
	case RoundedBlock:
		return []string{" ", "▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
	case ShadedBlock:
		return []string{" ", "░", "░", "▒", "▒", "▓", "▓", "▓", "▓"}
	case SolidBlock:
		fallthrough
	default:
		return []string{" ", "▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
	}
}
