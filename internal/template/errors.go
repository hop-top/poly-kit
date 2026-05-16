// Template package error types. Each class exposes three forms:
// sentinel var (errors.Is), typed struct with context (errors.As),
// and Is helper. NewXxxError factories chain both so a single
// returned value satisfies all three matching styles.
package template

import (
	"errors"
	"fmt"
)

// Sentinels — kept stable for errors.Is checks.
var (
	ErrManifestInvalid  = errors.New("template: manifest invalid")
	ErrTemplateNotFound = errors.New("template: not found")
	ErrHookFailed       = errors.New("template: hook failed")
	ErrFileConflict     = errors.New("template: file conflict")
)

// ManifestInvalidError: kit-template.yaml parse/validate failure.
type ManifestInvalidError struct {
	Path string
	Why  string
	Err  error
}

func (e *ManifestInvalidError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %s: %v", ErrManifestInvalid, e.Path, e.Why, e.Err)
	}
	return fmt.Sprintf("%s: %s: %s", ErrManifestInvalid, e.Path, e.Why)
}

func (e *ManifestInvalidError) Unwrap() []error {
	return []error{ErrManifestInvalid, e.Err}
}

func NewManifestInvalidError(path, why string, wrapped error) error {
	return &ManifestInvalidError{Path: path, Why: why, Err: wrapped}
}

func IsManifestInvalid(err error) bool {
	var e *ManifestInvalidError
	return errors.As(err, &e) || errors.Is(err, ErrManifestInvalid)
}

// TemplateNotFoundError: registry spec did not resolve.
type TemplateNotFoundError struct{ Spec string }

func (e *TemplateNotFoundError) Error() string {
	return fmt.Sprintf("%s: %s", ErrTemplateNotFound, e.Spec)
}
func (e *TemplateNotFoundError) Unwrap() error { return ErrTemplateNotFound }

func NewTemplateNotFoundError(spec string) error {
	return &TemplateNotFoundError{Spec: spec}
}

func IsTemplateNotFound(err error) bool {
	var e *TemplateNotFoundError
	return errors.As(err, &e) || errors.Is(err, ErrTemplateNotFound)
}

// HookFailedError: hook script exited non-zero.
type HookFailedError struct {
	Name     string
	ExitCode int
}

func (e *HookFailedError) Error() string {
	return fmt.Sprintf("%s: %s: exit %d", ErrHookFailed, e.Name, e.ExitCode)
}
func (e *HookFailedError) Unwrap() error { return ErrHookFailed }

func NewHookFailedError(name string, exitCode int) error {
	return &HookFailedError{Name: name, ExitCode: exitCode}
}

func IsHookFailed(err error) bool {
	var e *HookFailedError
	return errors.As(err, &e) || errors.Is(err, ErrHookFailed)
}

// FileConflictError: augment-mode conflict with --force rejected.
type FileConflictError struct{ Path string }

func (e *FileConflictError) Error() string {
	return fmt.Sprintf("%s: %s", ErrFileConflict, e.Path)
}
func (e *FileConflictError) Unwrap() error { return ErrFileConflict }

func NewFileConflictError(path string) error {
	return &FileConflictError{Path: path}
}

func IsFileConflict(err error) bool {
	var e *FileConflictError
	return errors.As(err, &e) || errors.Is(err, ErrFileConflict)
}
