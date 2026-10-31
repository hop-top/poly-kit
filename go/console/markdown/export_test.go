package markdown

import "github.com/charmbracelet/colorprofile"

// SetProfileForTest pins the detected stdout color profile and returns a
// restore func.
func SetProfileForTest(p colorprofile.Profile) (restore func()) {
	old := detectProfile
	detectProfile = func() colorprofile.Profile { return p }
	return func() { detectProfile = old }
}
