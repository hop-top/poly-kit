package observability

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/transport/cmdsurface"
)

// TestRefusalCodeDeadlineExceeded pins that a run the per-command
// deadline cut short is counted as the deadline_exceeded refusal.
func TestRefusalCodeDeadlineExceeded(t *testing.T) {
	err := fmt.Errorf("%w: ping ran past its 1s deadline", cmdsurface.ErrDeadlineExceeded)
	assert.Equal(t, RefusalDeadlineExceeded, RefusalCode(err))
	outcome, code := verdict(cmdsurface.Result{}, err)
	assert.Equal(t, OutcomeRefused, outcome)
	assert.Equal(t, RefusalDeadlineExceeded, code)
}
