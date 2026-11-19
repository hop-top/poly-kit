// Package prompt holds the prompt-side building blocks of an AI tool:
// versioned templates, patterns, techniques, fragments and a schema DSL.
//
//   - [Template]: a `name@N.yaml` file (system text + a Go text/template body)
//     with a content hash; loaded through a [Source].
//   - [Pattern]: a named system prompt, loaded through a [PatternSource].
//   - [Technique]: a prefix/suffix wrapper (chain of thought, step-back, ...)
//     applied to a system prompt with [Wrap]. "Strategy" is reserved for
//     multi-call strategies elsewhere.
//   - [FragmentStore]: aliased snippets joined by [ResolveFragments].
//   - [CompileDSL]: a one-line field list compiled to a JSON Schema.
//
// Nothing here reads a default location: callers inject every directory or
// filesystem.
package prompt

import "errors"

// ErrNotFound reports a template, pattern, technique or fragment that no
// layer holds. Test with errors.Is.
var ErrNotFound = errors.New("prompt: not found")

// ErrInvalid reports input that is malformed: a bad ref or name, a template
// that does not parse, an unusable DSL.
var ErrInvalid = errors.New("prompt: invalid")
