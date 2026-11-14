package oauth

import (
	"fmt"
	"log/slog"
)

const redacted = "[redacted]"

// Grants and tokens are never printed: every formatting path (%v %+v %#v %s
// %q, slog, json.Marshal) yields a fixed string. Stored JSON comes only from
// EncodeGrant and EncodeCachedToken.

func (g Grant) String() string   { return "oauth.Grant{kind=" + string(g.Kind) + " " + redacted + "}" }
func (g Grant) GoString() string { return g.String() }
func (g Grant) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(g.String()))
}
func (g Grant) LogValue() slog.Value         { return slog.StringValue(g.String()) }
func (g Grant) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (g Grant) MarshalText() ([]byte, error) { return []byte(redacted), nil }

func (t Token) String() string   { return "oauth.Token{" + redacted + "}" }
func (t Token) GoString() string { return t.String() }
func (t Token) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(t.String()))
}
func (t Token) LogValue() slog.Value         { return slog.StringValue(t.String()) }
func (t Token) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (t Token) MarshalText() ([]byte, error) { return []byte(redacted), nil }

func (c CachedToken) String() string   { return "oauth.CachedToken{" + redacted + "}" }
func (c CachedToken) GoString() string { return c.String() }
func (c CachedToken) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(c.String()))
}
func (c CachedToken) LogValue() slog.Value         { return slog.StringValue(c.String()) }
func (c CachedToken) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (c CachedToken) MarshalText() ([]byte, error) { return []byte(redacted), nil }
