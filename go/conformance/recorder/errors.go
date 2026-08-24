package recorder

import (
	"errors"
	"fmt"
)

// StoryHashMismatchError reports that the supplied story bytes do not
// hash to the scenario's declared story_ref.content_hash. Recording
// with drifted story bytes would produce a cassette the grading
// service rejects, so the recorder refuses up front.
type StoryHashMismatchError struct {
	Declared string
	Actual   string
}

func (e *StoryHashMismatchError) Error() string {
	return fmt.Sprintf("recorder: story hash mismatch: scenario declares %s, story bytes hash to %s (update the scenario's story_ref.content_hash or record against the right story)",
		e.Declared, e.Actual)
}

// IsStoryHashMismatch reports whether err is or wraps a
// *StoryHashMismatchError.
func IsStoryHashMismatch(err error) bool {
	var e *StoryHashMismatchError
	return errors.As(err, &e)
}

// StoryNotFoundError reports that the story source could not be
// resolved from the scenario's story_ref.story_path or an explicit
// override.
type StoryNotFoundError struct {
	StoryPath string
	Err       error
}

func (e *StoryNotFoundError) Error() string {
	return fmt.Sprintf("recorder: story %q: %v", e.StoryPath, e.Err)
}

func (e *StoryNotFoundError) Unwrap() error { return e.Err }

// IsStoryNotFound reports whether err is or wraps a
// *StoryNotFoundError.
func IsStoryNotFound(err error) bool {
	var e *StoryNotFoundError
	return errors.As(err, &e)
}

// OutDirNotEmptyError reports that the requested output directory
// already holds files. The recorder never overwrites prior captures.
type OutDirNotEmptyError struct {
	Dir string
}

func (e *OutDirNotEmptyError) Error() string {
	return fmt.Sprintf("recorder: out dir %q is not empty; refusing to overwrite an existing recording", e.Dir)
}

// IsOutDirNotEmpty reports whether err is or wraps an
// *OutDirNotEmptyError.
func IsOutDirNotEmpty(err error) bool {
	var e *OutDirNotEmptyError
	return errors.As(err, &e)
}

// StepExecError reports that a step's subprocess could not run to a
// captured exit (missing binary, timeout, signal). This is a
// recording failure, never a capture.
type StepExecError struct {
	StepID string
	Err    error
}

func (e *StepExecError) Error() string {
	return fmt.Sprintf("recorder: step %q failed to execute: %v", e.StepID, e.Err)
}

func (e *StepExecError) Unwrap() error { return e.Err }

// IsStepExec reports whether err is or wraps a *StepExecError.
func IsStepExec(err error) bool {
	var e *StepExecError
	return errors.As(err, &e)
}
