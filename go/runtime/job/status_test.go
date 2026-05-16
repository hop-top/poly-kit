package job_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"
)

func TestNewStateMachine_ValidTransitions(t *testing.T) {
	sm := job.NewStateMachine(nil)
	ctx := context.Background()

	tests := []struct {
		from, to job.Status
	}{
		{job.StatusPending, job.StatusActive},
		{job.StatusPending, job.StatusCancelled},
		{job.StatusActive, job.StatusSucceeded},
		{job.StatusActive, job.StatusFailed},
		{job.StatusActive, job.StatusTimeout},
		{job.StatusActive, job.StatusPending},
		{job.StatusActive, job.StatusCancelled},
		{job.StatusFailed, job.StatusPending},
		{job.StatusTimeout, job.StatusPending},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			err := sm.Transition(ctx,
				domain.State(tt.from),
				domain.State(tt.to),
				false,
			)
			require.NoError(t, err)
		})
	}
}

func TestNewStateMachine_InvalidTransitions(t *testing.T) {
	sm := job.NewStateMachine(nil)
	ctx := context.Background()

	tests := []struct {
		from, to job.Status
	}{
		{job.StatusPending, job.StatusSucceeded},
		{job.StatusPending, job.StatusFailed},
		{job.StatusSucceeded, job.StatusPending},
		{job.StatusSucceeded, job.StatusActive},
		{job.StatusCancelled, job.StatusPending},
		{job.StatusFailed, job.StatusActive},
		{job.StatusTimeout, job.StatusActive},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			err := sm.Transition(ctx,
				domain.State(tt.from),
				domain.State(tt.to),
				false,
			)
			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrInvalidTransition)
		})
	}
}

func TestAllowedFrom(t *testing.T) {
	sm := job.NewStateMachine(nil)

	tests := []struct {
		state    job.Status
		expected []domain.State
	}{
		{job.StatusPending, []domain.State{
			domain.State(job.StatusActive),
			domain.State(job.StatusCancelled),
		}},
		{job.StatusActive, []domain.State{
			domain.State(job.StatusSucceeded),
			domain.State(job.StatusFailed),
			domain.State(job.StatusTimeout),
			domain.State(job.StatusPending),
			domain.State(job.StatusCancelled),
		}},
		{job.StatusFailed, []domain.State{
			domain.State(job.StatusPending),
		}},
		{job.StatusTimeout, []domain.State{
			domain.State(job.StatusPending),
		}},
		{job.StatusSucceeded, nil},
		{job.StatusCancelled, nil},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			got := sm.AllowedFrom(domain.State(tt.state))
			assert.Equal(t, tt.expected, got)
		})
	}
}
