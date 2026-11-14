// Package oauth defines the stored formats and shared errors of the OAuth 2
// token source: the grant envelope held at a reference, the cached access
// token held beside it, and the key layout tying them together.
//
// Stored values are language-neutral JSON with stable snake_case names. The
// cached token is exactly a uma "oauth2" credential envelope, so a host that
// reads the token key through uma needs no conversion.
//
// Nothing here talks to a network. Grants, the refresh loop and locking live
// in later layers that build on these types.
package oauth
