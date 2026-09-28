package api

import "errors"

// ErrOverloaded reports that the invocation plane's capacity gate
// refused the call: every in-flight slot of the service is taken and
// its queue is full. The projection answers it 503 overloaded, with a
// Retry-After header when the error carries a retry hint (see
// [ErrRateLimited]).
var ErrOverloaded = errors.New("api: overloaded")

// CodeOverloaded is the refusal code of the capacity gate.
const CodeOverloaded = "overloaded"
