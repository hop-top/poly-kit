// Package avatar provides a provider-agnostic facade for generating
// avatar URLs (or data URIs) from a seed value.
//
// The package ships with a built-in dicebear provider; additional
// providers (gravatar, boring, libravatar, …) can register themselves
// via RegisterProvider. Callers select a provider by name, or rely on
// the default ("dicebear").
//
// Typical usage:
//
//	url, _ := avatar.Generate(ctx, avatar.Options{
//	    Seed:  "noor",
//	    Style: "shapes",
//	    Size:  256,
//	})
//
// Selecting a non-default provider:
//
//	url, _ := avatar.Generate(ctx, avatar.Options{
//	    Provider: "gravatar",
//	    Seed:     "user@example.com",
//	    Size:     128,
//	})
//
// Registering a custom provider:
//
//	avatar.RegisterProvider(myProvider{})
//
// All built-in providers produce URLs (no I/O at generation time);
// the ctx is reserved for future async providers.
package avatar
