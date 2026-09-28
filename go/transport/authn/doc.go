// Package authn verifies bearer tokens for kit's served surfaces. Its
// [Verifier] checks a JWT and returns the caller it names; its
// [Verifier.AuthFunc] is an [api.AuthFunc], so it plugs into the api
// router's Auth middleware, the rpc service's interceptor and the mcp
// service's HTTP gate unchanged.
//
// Three key sources, one verifier:
//
//   - [NewJWT]: a fixed set of public keys — the tool's own identity
//     keypair ([IdentityKey]) plus any key files ([ParsePublicKeyPEM]).
//     A token's kid selects the key; a retired key left in the set
//     keeps the tokens it signed valid while new ones carry the new
//     kid.
//   - [NewJWKS]: a JSON Web Key Set fetched from a URL, cached, and
//     refetched on an interval and when a token names a kid the set
//     does not hold (the issuer rotated).
//   - [NewOIDC]: an OpenID Connect issuer; discovery resolves its
//     jwks_uri, which is then handled as [NewJWKS] handles its URL.
//
// [APIKeys] is the opaque alternative: kit-issued keys held hashed in
// a kv store, each standing for a principal, tenant and scopes, until
// it expires or is revoked.
//
// Every JWT is checked the same way: a signature by one of the
// source's keys under an asymmetric algorithm ("none" and HMAC are
// never accepted), exp present and not past, nbf and iat not in the
// future, each with the [Options.ClockSkew] tolerance, the issuer and
// audience when [Options] names them, a subject, and the adopter's
// revocation check when [Options.Check] is set.
//
// The claims an AuthFunc returns are an [api.Claims]: the principal
// from sub, the tenant from [Options.TenantClaim] ("tenant" by
// default), and the scopes as [api.ScopesOf] reads them: "scopes" (a
// list), else "scope" (space-delimited, RFC 8693 and RFC 9068), else
// "scp".
//
// A kit tool rarely constructs these by hand: services.<svc>.auth.mode
// (jwt, jwks or oidc) selects and configures one for the api, rpc and
// mcp services. See docs/contracts/serve-lifecycle.md.
package authn
