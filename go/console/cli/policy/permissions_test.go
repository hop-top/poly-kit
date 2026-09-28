package policy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/policy"
)

// TestLoad_PermissionsBlock pins that the permissions: key is read
// beside the delegation keys, rule by rule, without changing them.
func TestLoad_PermissionsBlock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ops.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
allow:
  destructive: []
permissions:
  - name: tenant-boundary
    when: principal.tenant != "acme"
    effect: deny
    otherwise: allow
    message: acme only
`), 0o600))

	p, err := policy.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "ops", p.Name)
	assert.Equal(t, []string{}, p.Allow[policy.SideEffectDestructive])
	assert.Equal(t, []policy.PermissionRule{{
		Name:      "tenant-boundary",
		When:      `principal.tenant != "acme"`,
		Effect:    policy.RuleDeny,
		Otherwise: policy.RuleAllow,
		Message:   "acme only",
	}}, p.Permissions)
}

// TestLoad_PermissionsBlockRefusesAMalformedRule pins that a rule
// the gate could not apply fails the load, naming the rule, rather
// than being skipped.
func TestLoad_PermissionsBlockRefusesAMalformedRule(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		yaml string
		want string
	}{
		"unnamed": {
			yaml: "permissions:\n  - when: 'true'\n    effect: deny\n    otherwise: allow\n",
			want: "permissions[0]: name required",
		},
		"duplicate": {
			yaml: "permissions:\n" +
				"  - {name: a, when: 'true', effect: deny, otherwise: allow}\n" +
				"  - {name: a, when: 'false', effect: deny, otherwise: allow}\n",
			want: `permission rule "a": duplicate name`,
		},
		"no expression": {
			yaml: "permissions:\n  - {name: a, effect: deny, otherwise: allow}\n",
			want: `permission rule "a": 'when' required`,
		},
		"no effect": {
			yaml: "permissions:\n  - {name: a, when: 'true', otherwise: allow}\n",
			want: `permission rule "a": effect required (allow|deny)`,
		},
		"bad otherwise": {
			yaml: "permissions:\n  - {name: a, when: 'true', effect: deny, otherwise: maybe}\n",
			want: `permission rule "a": otherwise "maybe" invalid (want allow|deny)`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "p.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.yaml), 0o600))
			_, err := policy.Load(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
