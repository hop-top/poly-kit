// Story: as a kit adopter, I want broken CEL to fail at NewEngine /
// withcel.New startup, not at first event, so misconfigured policies
// are caught at boot rather than during a real mutation.
//
// Background: silent compile failures are a classic operational
// foot-gun: a typo in production-only YAML can sit dormant until a
// guard event fires, at which point you discover your policy never
// ran. kit's house style is fail-loud: every CEL expression is
// compiled (and type-checked) at engine init, and any error is
// returned synchronously to the caller.
//
// Acceptance:
//
//	Given a YAML with a syntactically invalid CEL `when:` expression
//	When  withcel.New is called on the parsed config
//	Then  it returns an error and no engine is constructed
package e2e_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

func TestStory_CompileFailsLoud_BadCEL(t *testing.T) {
	t.Parallel()

	yaml := `policies:
  - name: broken-rule
    on: kit.runtime.entity.pre_validated
    when: 'this is not cel ?? !!'
    effect: allow
    otherwise: deny
    message: "broken"
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err, "ParseConfig only validates schema, not CEL syntax")

	eng, err := withcel.New(cfg)
	require.Error(t, err, "withcel.New must surface CEL compile errors at boot")
	assert.Nil(t, eng)
	// The error must mention the failing policy by name so ops can
	// fix the right rule in their YAML.
	assert.Contains(t, err.Error(), "broken-rule")
}

func TestStory_CompileFailsLoud_TypeError(t *testing.T) {
	t.Parallel()

	// CEL type-checks at compile time. Comparing a map to a string
	// is a type error caught BEFORE any event fires.
	yaml := `policies:
  - name: type-mismatch
    on: kit.runtime.entity.pre_validated
    when: 'principal == "admin"'
    effect: allow
    otherwise: deny
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)

	_, err = withcel.New(cfg)
	require.Error(t, err, "type errors in CEL expressions must surface at boot")
}
