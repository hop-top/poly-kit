// Story: as an ops lead, I want async policy subscriptions rejected
// at parse time so that policies cannot silently miss vetoes.
//
// Background: the engine's veto guarantee depends on running
// SYNCHRONOUSLY in front of the publisher's mutation. An async
// handler on a `pre_*` topic would observe the event after the
// mutation already happened — making the policy a notifier, not a
// guard. The YAML schema therefore rejects `async: true` at
// LoadConfig / ParseConfig time, so a misconfigured rule never
// reaches the bus subscription stage.
//
// Acceptance:
//
//	Given a YAML with `async: true` on a policy
//	When  policy.ParseConfig is called
//	Then  it returns an error explaining async is not supported
package e2e_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/policy"
)

func TestStory_AsyncSubscriptionRejected_AtParseTime(t *testing.T) {
	t.Parallel()

	yaml := `policies:
  - name: would-be-async
    on: kit.runtime.entity.pre_validated
    when: 'principal.role == "admin"'
    effect: allow
    otherwise: deny
    async: true
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.Error(t, err, "async policies must be rejected at parse time")
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "async not supported")
	assert.Contains(t, err.Error(), "would-be-async",
		"error must name the offending policy so ops can find it")
}
