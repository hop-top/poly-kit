// Init command error types. Each class exposes three forms: sentinel
// var (errors.Is), typed struct with context (errors.As), and Is*
// helper. NewXxxError factories chain to the sentinel via Go 1.20+
// multi-error Unwrap so a single returned value satisfies all three
// matching styles. Error() strings are hint-rich per spec §19.
//
// Package name is kitinit (not init) to avoid confusion with Go's
// reserved init() function semantics.
package kitinit

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinels — kept stable for errors.Is checks.
var (
	ErrInvalidName      = errors.New("kit init: invalid project name")
	ErrModeBareWorktree = errors.New("kit init: cwd is a bare-worktree git directory")
	ErrAlreadyKit       = errors.New("kit init: project already kit-augmented")
	ErrMissingRequired  = errors.New("kit init: missing required input")
	ErrOrgRequired      = errors.New("kit init: --org is required when --account-type=org")
	ErrExists           = errors.New("kit init: target directory already exists")
)

// ExistsError: target directory already present in cwd. --force does not
// override (we never blow away user data); user must choose a new name or
// remove the directory first.
type ExistsError struct{ Target string }

func (e *ExistsError) Error() string {
	return fmt.Sprintf("target %q already exists; choose a different name or remove the existing directory", e.Target)
}
func (e *ExistsError) Unwrap() []error { return []error{ErrExists} }

func NewExistsError(target string) error { return &ExistsError{Target: target} }

func IsExists(err error) bool {
	var e *ExistsError
	return errors.As(err, &e) || errors.Is(err, ErrExists)
}

// InvalidNameError: project name failed regex validation.
type InvalidNameError struct{ Name string }

func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("invalid name %q: must match ^[a-z][a-z0-9-]{0,63}$", e.Name)
}
func (e *InvalidNameError) Unwrap() []error { return []error{ErrInvalidName} }

func NewInvalidNameError(name string) error { return &InvalidNameError{Name: name} }

func IsInvalidName(err error) bool {
	var e *InvalidNameError
	return errors.As(err, &e) || errors.Is(err, ErrInvalidName)
}

// ModeBareWorktreeError: cwd resolved to a bare-repo worktree (e.g.
// labspace hop layout); kit init does not yet support this.
type ModeBareWorktreeError struct{ CommonDir, GitDir string }

func (e *ModeBareWorktreeError) Error() string {
	return fmt.Sprintf(
		"kit init does not support bare worktrees yet.\n"+
			"Detected: cwd is worktree of %s (bare repo).\n"+
			"Workaround: run kit init from a regular clone.",
		e.CommonDir,
	)
}
func (e *ModeBareWorktreeError) Unwrap() []error { return []error{ErrModeBareWorktree} }

func NewModeBareWorktreeError(commonDir, gitDir string) error {
	return &ModeBareWorktreeError{CommonDir: commonDir, GitDir: gitDir}
}

func IsBareWorktree(err error) bool {
	var e *ModeBareWorktreeError
	return errors.As(err, &e) || errors.Is(err, ErrModeBareWorktree)
}

// AlreadyKitError: .kit/version present; redirect user to kit upgrade.
type AlreadyKitError struct{ Version string }

func (e *AlreadyKitError) Error() string {
	return fmt.Sprintf(
		"this project was kit-augmented at version %s.\n"+
			"Hint: run `kit upgrade` to refresh kit conventions.\n"+
			"(kit upgrade is not yet implemented; tracking issue forthcoming)",
		e.Version,
	)
}
func (e *AlreadyKitError) Unwrap() []error { return []error{ErrAlreadyKit} }

func NewAlreadyKitError(version string) error { return &AlreadyKitError{Version: version} }

func IsAlreadyKit(err error) bool {
	var e *AlreadyKitError
	return errors.As(err, &e) || errors.Is(err, ErrAlreadyKit)
}

// MissingRequiredError: a required template variable was not supplied
// via flag, env, or interactive prompt.
type MissingRequiredError struct{ VarName string }

func (e *MissingRequiredError) Error() string {
	return fmt.Sprintf(
		"missing required input %q (use --%s flag, env KIT_%s, or omit --yes for interactive prompt)",
		e.VarName, e.VarName, strings.ToUpper(e.VarName),
	)
}
func (e *MissingRequiredError) Unwrap() []error { return []error{ErrMissingRequired} }

func NewMissingRequiredError(varName string) error {
	return &MissingRequiredError{VarName: varName}
}

func IsMissingRequired(err error) bool {
	var e *MissingRequiredError
	return errors.As(err, &e) || errors.Is(err, ErrMissingRequired)
}

// NewOrgRequiredError: --account-type=org without --org. Plain
// sentinel-wrapping; no extra context fields needed.
func NewOrgRequiredError() error {
	return fmt.Errorf("--org is required when --account-type=org: %w", ErrOrgRequired)
}
