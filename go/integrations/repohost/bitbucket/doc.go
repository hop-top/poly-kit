// Package bitbucket implements the [repohost.MutableHost] interface
// for Bitbucket Cloud using the github.com/ktrysmt/go-bitbucket SDK.
//
// Auth: Config.Token takes priority. When empty, the driver reads
// BITBUCKET_TOKEN. When no token is found the driver proceeds
// unauthenticated and is rate-limited by Bitbucket.
//
// Bitbucket Cloud accepts API tokens via HTTP Basic auth with an
// empty username and the token as the password — the driver wires
// this up via bitbucket.NewBasicAuth("", token).
//
// Self-hosted Bitbucket Server is OUT OF SCOPE — this driver targets
// Bitbucket Cloud only. Bitbucket Server has a different API surface
// (v1.0 not v2.0) and would need a separate driver.
//
// Default BaseURL is https://api.bitbucket.org/2.0; override via
// Config.BaseURL only for Atlassian Isolated Cloud Instances.
//
// Notable provider quirks:
//   - Bitbucket Cloud has no labels — Labels is always returned as an
//     empty (non-nil) slice.
//   - Bitbucket issues are an opt-in feature per repository; many
//     repos have them disabled. ListIssues returns an empty slice +
//     nil error in that case rather than surfacing a failure.
//   - Bitbucket has limited author/label filter support on the
//     list endpoints; the driver applies those filters client-side.
//
// API reference:
// https://developer.atlassian.com/cloud/bitbucket/rest/intro/
package bitbucket
