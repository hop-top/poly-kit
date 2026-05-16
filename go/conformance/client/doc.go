// Package client implements the adopter-facing seam to the
// hop.top/kit conformance grading service ("svc"). Adopters import
// this package to upload a locally-recorded cassette + manifest to a
// configured svc URL and receive a typed grading verdict back.
//
// Two surfaces ship together:
//
//   - Library: New(baseURL, opts...) returns a *Client that exposes
//     Grade(ctx, GradeRequest) and Status(ctx, gradeID). Adopters
//     embed library calls inside `go test` to fail integration tests
//     when grading verdicts are not pass.
//   - CLI: `kit conformance grade <cassette-dir>` (see
//     go/console/cli/conformance/grade/) is a thin wrapper around the
//     library that adopter CI workflows invoke directly.
//
// The Result type is a structural bridge to
// hop.top/kit/go/conformance/scenario.Result. While the scen track is
// landing in a sibling worktree, this package owns a local Result
// shape mirrors scenario.Result; the JSON wire shape is identical
// so the bridge round-trips losslessly.
package client
