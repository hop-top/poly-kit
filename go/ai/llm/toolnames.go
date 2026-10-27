package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// ToolNamesParam is the provider param that selects the [ToolNameMode]
// of a tool-calling adapter, as in "anthropic://model?tool_names=strict".
const ToolNamesParam = "tool_names"

// ToolNameMode is how an adapter treats a [ToolDef] name its provider
// does not accept.
type ToolNameMode string

const (
	// ToolNamesSanitize, the default, sends such a name as
	// [ToolNameRule.Sanitize] rewrites it and maps it back to the
	// original in every tool call the adapter returns.
	ToolNamesSanitize ToolNameMode = "sanitize"
	// ToolNamesStrict sends names as given and fails the request with
	// an [llmerrors.ErrToolName] (reason "invalid") before any network
	// call when one is not valid for the provider.
	ToolNamesStrict ToolNameMode = "strict"
)

// ToolNameModeOf reads [ToolNamesParam] from provider params; absent
// means [ToolNamesSanitize].
func ToolNameModeOf(params map[string]string) (ToolNameMode, error) {
	v, ok := params[ToolNamesParam]
	if !ok {
		return ToolNamesSanitize, nil
	}
	switch m := ToolNameMode(strings.TrimSpace(v)); m {
	case ToolNamesSanitize, ToolNamesStrict:
		return m, nil
	}
	return "", fmt.Errorf("%s must be %q or %q, got %q", ToolNamesParam, ToolNamesSanitize, ToolNamesStrict, v)
}

// ToolNameRule is a provider's rule for tool names: 1 to MaxLen bytes
// of ASCII letters, digits, '_' and '-', plus the bytes in Extra; with
// LeadLetter, the first byte must be a letter or '_'.
type ToolNameRule struct {
	// Provider names the rule in errors.
	Provider   string
	MaxLen     int
	Extra      string
	LeadLetter bool
}

// Provider tool-name rules, from each API's reference.
var (
	// AnthropicToolNames is ^[a-zA-Z0-9_-]{1,128}$.
	AnthropicToolNames = ToolNameRule{Provider: "anthropic", MaxLen: 128}
	// OpenAIToolNames is ^[a-zA-Z0-9_-]{1,64}$, for the OpenAI API and
	// the OpenAI-compatible servers its adapter speaks to.
	OpenAIToolNames = ToolNameRule{Provider: "openai", MaxLen: 64}
	// GeminiToolNames is ^[a-zA-Z_][a-zA-Z0-9_.:-]{0,127}$.
	GeminiToolNames = ToolNameRule{Provider: "gemini", MaxLen: 128, Extra: ".:", LeadLetter: true}
)

// toolNameHashLen is how many hex digits of the name's SHA-256 a
// shortened name ends with, after a '_'.
const toolNameHashLen = 8

func (r ToolNameRule) allowed(c byte) bool {
	return isASCIIAlnum(c) || c == '_' || c == '-' || strings.IndexByte(r.Extra, c) >= 0
}

func (r ToolNameRule) leadOK(c byte) bool {
	return !r.LeadLetter || c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isASCIIAlnum(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// Valid reports whether the provider accepts name as is.
func (r ToolNameRule) Valid(name string) bool {
	if name == "" || len(name) > r.MaxLen || !r.leadOK(name[0]) {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !r.allowed(name[i]) {
			return false
		}
	}
	return true
}

// Sanitize returns name as the provider accepts it. A valid name comes
// back unchanged. Otherwise, over the UTF-8 bytes of name:
//
//  1. every maximal run of bytes the rule does not allow becomes one '_';
//  2. with LeadLetter, a first byte that is not a letter or '_' gets a
//     '_' in front;
//  3. a result longer than MaxLen, or holding no ASCII letter or digit,
//     keeps its first MaxLen-9 bytes (all of it when shorter) and gains
//     '_' and the first 8 lowercase hex digits of SHA-256(name).
//
// An empty name or one that is not valid UTF-8 is an
// [llmerrors.ErrToolName]; the result is never empty.
func (r ToolNameRule) Sanitize(name string) (string, error) {
	if name == "" {
		return "", llmerrors.NewToolName(r.Provider, llmerrors.ToolNameEmpty, name, "")
	}
	if !utf8.ValidString(name) {
		return "", llmerrors.NewToolName(r.Provider, llmerrors.ToolNameInvalidUTF8, name, "")
	}
	if r.Valid(name) {
		return name, nil
	}
	var b strings.Builder
	b.Grow(len(name) + 1)
	run := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		if r.allowed(c) {
			b.WriteByte(c)
			run = false
			continue
		}
		if !run {
			b.WriteByte('_')
		}
		run = true
	}
	out := b.String()
	if !r.leadOK(out[0]) {
		out = "_" + out
	}
	if len(out) <= r.MaxLen && hasASCIIAlnum(out) {
		return out, nil
	}
	sum := sha256.Sum256([]byte(name))
	keep := min(len(out), r.MaxLen-toolNameHashLen-1)
	return out[:keep] + "_" + hex.EncodeToString(sum[:])[:toolNameHashLen], nil
}

func hasASCIIAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		if isASCIIAlnum(s[i]) {
			return true
		}
	}
	return false
}

// ToolNames maps one request's tool names to the names sent to the
// provider (wire names) and back. Adapters build one per request with
// [NewToolNames]; a nil *ToolNames maps every name to itself.
type ToolNames struct {
	rule   ToolNameRule
	mode   ToolNameMode
	toWire map[string]string
	toOrig map[string]string
}

// NewToolNames checks defs against rule and builds their mapping. The
// first failing def, in order, fails the request with an
// [llmerrors.ErrToolName]:
//
//   - "empty": an empty name;
//   - "invalid_utf8": a name that is not valid UTF-8;
//   - "invalid": in [ToolNamesStrict], a name rule does not accept;
//   - "duplicate": a name an earlier def already has;
//   - "collision": a name whose wire name an earlier def already has
//     (Other names that def).
func NewToolNames(rule ToolNameRule, mode ToolNameMode, defs []ToolDef) (*ToolNames, error) {
	t := &ToolNames{
		rule:   rule,
		mode:   mode,
		toWire: make(map[string]string, len(defs)),
		toOrig: make(map[string]string, len(defs)),
	}
	for _, d := range defs {
		name := d.Name
		var wire string
		switch {
		case name == "":
			return nil, llmerrors.NewToolName(rule.Provider, llmerrors.ToolNameEmpty, name, "")
		case !utf8.ValidString(name):
			return nil, llmerrors.NewToolName(rule.Provider, llmerrors.ToolNameInvalidUTF8, name, "")
		case mode == ToolNamesStrict:
			if !rule.Valid(name) {
				return nil, llmerrors.NewToolName(rule.Provider, llmerrors.ToolNameInvalid, name, "")
			}
			wire = name
		default:
			var err error
			if wire, err = rule.Sanitize(name); err != nil {
				return nil, err
			}
		}
		if _, dup := t.toWire[name]; dup {
			return nil, llmerrors.NewToolName(rule.Provider, llmerrors.ToolNameDuplicate, name, "")
		}
		if other, taken := t.toOrig[wire]; taken {
			return nil, llmerrors.NewToolName(rule.Provider, llmerrors.ToolNameCollision, name, other)
		}
		t.toWire[name] = wire
		t.toOrig[wire] = name
	}
	return t, nil
}

// Wire returns the name to send for name: a def's wire name, else, as
// for a call replayed from history, name sanitized ([ToolNamesSanitize])
// or unchanged ([ToolNamesStrict], or a name Sanitize refuses).
func (t *ToolNames) Wire(name string) string {
	if t == nil {
		return name
	}
	if w, ok := t.toWire[name]; ok {
		return w
	}
	if t.mode == ToolNamesStrict {
		return name
	}
	if w, err := t.rule.Sanitize(name); err == nil {
		return w
	}
	return name
}

// Original returns the def name a wire name the provider returned
// stands for; a name no def maps to comes back unchanged.
func (t *ToolNames) Original(wire string) string {
	if t == nil {
		return wire
	}
	if o, ok := t.toOrig[wire]; ok {
		return o
	}
	return wire
}

// Defs returns defs with each name replaced by its wire name.
func (t *ToolNames) Defs(defs []ToolDef) []ToolDef {
	out := make([]ToolDef, len(defs))
	for i, d := range defs {
		d.Name = t.Wire(d.Name)
		out[i] = d
	}
	return out
}

// Calls returns calls with each name replaced by the original it
// stands for; calls is not modified.
func (t *ToolNames) Calls(calls []ToolCall) []ToolCall {
	if t == nil || len(calls) == 0 {
		return calls
	}
	out := make([]ToolCall, len(calls))
	for i, c := range calls {
		c.Name = t.Original(c.Name)
		out[i] = c
	}
	return out
}
