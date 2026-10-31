package celpermission_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/celpermission"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/transport/cmdsurface"
)

// vectors mirrors contracts/celpermission-v1/vectors.json.
type vectors struct {
	Suites []struct {
		Name  string       `json:"name"`
		Rules []vectorRule `json:"rules"`
		Cases []vectorCase `json:"cases"`
	} `json:"suites"`
	CompileErrors []struct {
		Name          string       `json:"name"`
		Rules         []vectorRule `json:"rules"`
		ErrorPrefix   string       `json:"error_prefix"`
		ErrorContains string       `json:"error_contains"`
	} `json:"compile_errors"`
}

type vectorRule struct {
	Name      string `json:"name"`
	When      string `json:"when"`
	Effect    string `json:"effect"`
	Otherwise string `json:"otherwise"`
	Message   string `json:"message"`
}

type vectorCase struct {
	Name         string        `json:"name"`
	Rules        *[]vectorRule `json:"rules"`
	Request      vectorRequest `json:"request"`
	Allowed      bool          `json:"allowed"`
	Reason       string        `json:"reason"`
	ReasonPrefix string        `json:"reason_prefix"`
}

type vectorRequest struct {
	Caller     string         `json:"caller"`
	Tenant     string         `json:"tenant"`
	Scopes     []string       `json:"scopes"`
	Source     string         `json:"source"`
	Surface    string         `json:"surface"`
	RemoteAddr string         `json:"remote_addr"`
	Path       []string       `json:"path"`
	Args       []string       `json:"args"`
	Flags      map[string]any `json:"flags"`
}

func toRules(in []vectorRule) []policy.PermissionRule {
	out := make([]policy.PermissionRule, 0, len(in))
	for _, r := range in {
		out = append(out, policy.PermissionRule{
			Name: r.Name, When: r.When, Effect: r.Effect, Otherwise: r.Otherwise, Message: r.Message,
		})
	}
	return out
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "celpermission-v1", "vectors.json"))
	require.NoError(t, err)
	var v vectors
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}

func meta(r vectorRequest) cmdsurface.Meta {
	m := cmdsurface.Meta{
		Surface: cmdsurface.Surface(r.Surface),
		Caller:  r.Caller,
		Tenant:  r.Tenant,
		Extra:   map[string]string{},
	}
	switch r.Source {
	case "verified":
		m.Established = cmdsurface.EstablishedVerified
	case "transport":
		m.Established = cmdsurface.EstablishedTransport
	}
	if len(r.Scopes) > 0 {
		m.Extra["scopes"] = strings.Join(r.Scopes, ",")
	}
	if r.RemoteAddr != "" {
		m.Extra["remote_addr"] = r.RemoteAddr
	}
	return m
}

// TestConformanceCelPermission runs the shared vectors.
func TestConformanceCelPermission(t *testing.T) {
	v := loadVectors(t)
	ran := 0
	for _, s := range v.Suites {
		for _, c := range s.Cases {
			t.Run(s.Name+"/"+c.Name, func(t *testing.T) {
				rules := s.Rules
				if c.Rules != nil {
					rules = *c.Rules
				}
				b := bridge(t, nil, toRules(rules)...)
				_, err := b.Invoke(context.Background(), cmdsurface.Invocation{
					Path: c.Request.Path, Args: c.Request.Args, Flags: c.Request.Flags, Meta: meta(c.Request),
				})
				if c.Allowed {
					require.NoError(t, err)
					return
				}
				require.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)
				// The bridge prefixes the surface; the reason is the tail.
				_, reason, _ := strings.Cut(err.Error(), " on "+c.Request.Surface+": ")
				if c.Reason != "" {
					assert.Equal(t, c.Reason, reason)
				}
				if c.ReasonPrefix != "" {
					assert.True(t, strings.HasPrefix(reason, c.ReasonPrefix), reason)
				}
			})
			ran++
		}
	}
	require.GreaterOrEqual(t, ran, 40)

	for _, c := range v.CompileErrors {
		t.Run("compile/"+c.Name, func(t *testing.T) {
			gate, err := celpermission.New(toRules(c.Rules))
			require.Error(t, err)
			assert.Nil(t, gate)
			assert.True(t, strings.HasPrefix(err.Error(), c.ErrorPrefix), err.Error())
			assert.Contains(t, err.Error(), c.ErrorContains)
		})
	}
}
