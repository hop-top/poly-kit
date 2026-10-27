package consent

import (
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// The grants file is <XDG_STATE_HOME>/<tool>/consent/grants.yaml:
//
//	version: 1
//	grants:
//	  - capability: io.screen.capture
//	    subject: app:dek
//	    decision: allow
//	    duration: always
//	    decided_at: "2026-03-01T00:00:00Z"
//	    decision_source: prompt
//	    prompt_version: 1
//
// The file belongs to consent alone; it is rewritten whole. Decoding
// is strict: one bad record makes the whole file malformed, so every
// check fails closed instead of skipping it.

const (
	grantsVersion = 1
	keyVersion    = "version"
	keyGrants     = "grants"
	keyCapability = "capability"
	keySubject    = "subject"
	keyDecision   = "decision"
	keyDuration   = "duration"
	keyExpiresAt  = "expires_at"
)

var grantFields = map[string]bool{
	keyCapability: true, keySubject: true, keyDecision: true, keyDuration: true,
	keyDecidedAt: true, keyExpiresAt: true, keyDecisionSource: true, keyPromptVersion: true,
}

func malformed(format string, args ...any) error {
	return grantErr(KindMalformedStore, format, args...)
}

// decodeGrants reads a grants file document. A nil document (no
// file, or an empty one) holds no grants.
func decodeGrants(doc *yaml.Node) ([]Grant, error) {
	if doc == nil || len(doc.Content) == 0 {
		return nil, nil
	}
	p := rootMapping(doc)
	if p == nil {
		return nil, malformed("document root is not a mapping")
	}
	for i := 0; i+1 < len(p.Content); i += 2 {
		if k := p.Content[i].Value; k != keyVersion && k != keyGrants {
			return nil, malformed("unknown key %q", k)
		}
	}
	v := mappingChild(p, keyVersion)
	if v == nil {
		return nil, malformed("version is missing")
	}
	if v.Value != strconv.Itoa(grantsVersion) {
		return nil, malformed("version %q is not supported (want %d)", v.Value, grantsVersion)
	}
	list := mappingChild(p, keyGrants)
	if list == nil {
		return nil, nil
	}
	if list.Kind != yaml.SequenceNode {
		return nil, malformed("grants is not a list")
	}
	out := make([]Grant, 0, len(list.Content))
	seen := map[grantKey]bool{}
	for i, n := range list.Content {
		g, err := decodeGrant(n)
		if err != nil {
			return nil, malformed("grants[%d]: %v", i, err)
		}
		if seen[g.key()] {
			return nil, malformed("grants[%d]: duplicate %s for %q", i, g.Capability, g.Subject)
		}
		seen[g.key()] = true
		out = append(out, g)
	}
	sortGrants(out)
	return out, nil
}

func decodeGrant(n *yaml.Node) (Grant, error) {
	if n.Kind != yaml.MappingNode {
		return Grant{}, fmt.Errorf("not a mapping")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !grantFields[n.Content[i].Value] {
			return Grant{}, fmt.Errorf("unknown key %q", n.Content[i].Value)
		}
	}
	str := func(key string, required bool) (string, error) {
		v := mappingChild(n, key)
		if v == nil {
			if required {
				return "", fmt.Errorf("%s is missing", key)
			}
			return "", nil
		}
		if v.Kind != yaml.ScalarNode {
			return "", fmt.Errorf("%s is not a scalar", key)
		}
		return v.Value, nil
	}
	var g Grant
	var err error
	var c, subj, dec, dur, decided, expires, src, pv string
	for _, f := range []struct {
		key string
		dst *string
		req bool
	}{
		{keyCapability, &c, true}, {keySubject, &subj, true}, {keyDecision, &dec, true},
		{keyDuration, &dur, true}, {keyDecidedAt, &decided, true}, {keyExpiresAt, &expires, false},
		{keyDecisionSource, &src, true}, {keyPromptVersion, &pv, true},
	} {
		if *f.dst, err = str(f.key, f.req); err != nil {
			return Grant{}, err
		}
	}
	g.Capability, g.Subject = Capability(c), subj
	g.Decision, g.Duration, g.DecisionSource = Effect(dec), Duration(dur), DecisionSource(src)
	if g.DecidedAt, err = time.Parse(time.RFC3339, decided); err != nil {
		return Grant{}, fmt.Errorf("decided_at: %w", err)
	}
	if expires != "" {
		if g.ExpiresAt, err = time.Parse(time.RFC3339, expires); err != nil {
			return Grant{}, fmt.Errorf("expires_at: %w", err)
		}
	}
	if g.PromptVersion, err = strconv.Atoi(pv); err != nil {
		return Grant{}, fmt.Errorf("prompt_version: %w", err)
	}
	if err := checkPersistable(g); err != nil {
		return Grant{}, err
	}
	return g, nil
}

// encodeGrants renders gs as a grants file document, sorted.
func encodeGrants(gs []Grant) *yaml.Node {
	sortGrants(gs)
	list := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, g := range gs {
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingChild(m, keyCapability, strNode(string(g.Capability)))
		setMappingChild(m, keySubject, strNode(g.Subject))
		setMappingChild(m, keyDecision, strNode(string(g.Decision)))
		setMappingChild(m, keyDuration, strNode(string(g.Duration)))
		setMappingChild(m, keyDecidedAt, strNode(formatTime(g.DecidedAt)))
		if g.Duration == Until {
			setMappingChild(m, keyExpiresAt, strNode(formatTime(g.ExpiresAt)))
		}
		setMappingChild(m, keyDecisionSource, strNode(string(g.DecisionSource)))
		setMappingChild(m, keyPromptVersion, intNode(g.PromptVersion))
		list.Content = append(list.Content, m)
	}
	doc := newMappingDoc()
	setMappingChild(doc.Content[0], keyVersion, intNode(grantsVersion))
	setMappingChild(doc.Content[0], keyGrants, list)
	return doc
}

func strNode(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func intNode(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
