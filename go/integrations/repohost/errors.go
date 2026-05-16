package repohost

import "errors"

// ErrRepoNotFound is returned when the requested repository does not
// exist or the caller's credentials cannot see it. Drivers translate
// provider-specific 404 shapes into this sentinel.
var ErrRepoNotFound = errors.New("repohost: repository not found")

// ErrCommitNotFound is returned when the requested commit SHA does
// not resolve in the target repository. Drivers translate provider-
// specific 404 shapes into this sentinel.
var ErrCommitNotFound = errors.New("repohost: commit not found")
