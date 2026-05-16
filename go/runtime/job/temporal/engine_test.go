package temporal_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"

	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/temporal"
)

// JobWorkflowSuite tests the workflow logic using Temporal's test env.
type JobWorkflowSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *JobWorkflowSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(temporal.JobWorkflow)
}

func (s *JobWorkflowSuite) AfterTest(_, _ string) {
	s.env.AssertExpectations(s.T())
}

func (s *JobWorkflowSuite) TestHappyPath() {
	input := temporal.JobWorkflowInput{
		Queue: "q",
		Type:  "test",
	}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalClaim,
			temporal.ClaimSignal{WorkerID: "w1"})
	}, time.Millisecond)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalComplete,
			temporal.CompleteSignal{Result: []byte(`{"ok":true}`)})
	}, 2*time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.NoError(s.T(), s.env.GetWorkflowError())

	var state temporal.JobState
	require.NoError(s.T(), s.env.GetWorkflowResult(&state))

	assert.Equal(s.T(), string(job.StatusSucceeded), state.Status)
	assert.Equal(s.T(), "w1", state.ClaimedBy)
	assert.Equal(s.T(), 1, state.Attempts)
	assert.NotNil(s.T(), state.CompletedAt)
}

func (s *JobWorkflowSuite) TestFailRetryThenComplete() {
	input := temporal.JobWorkflowInput{
		Queue:       "q",
		Type:        "test",
		MaxAttempts: 3,
	}

	// Attempt 1: claim then fail with retry.
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalClaim,
			temporal.ClaimSignal{WorkerID: "w1"})
	}, time.Millisecond)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalFail,
			temporal.FailSignal{Error: "boom", Retry: true})
	}, 2*time.Millisecond)

	// Attempt 2: claim then complete.
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalClaim,
			temporal.ClaimSignal{WorkerID: "w2"})
	}, 3*time.Millisecond)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalComplete,
			temporal.CompleteSignal{})
	}, 4*time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.NoError(s.T(), s.env.GetWorkflowError())

	var state temporal.JobState
	require.NoError(s.T(), s.env.GetWorkflowResult(&state))

	assert.Equal(s.T(), string(job.StatusSucceeded), state.Status)
	assert.Equal(s.T(), "w2", state.ClaimedBy)
	assert.Equal(s.T(), 2, state.Attempts)
}

func (s *JobWorkflowSuite) TestDeadLetter() {
	input := temporal.JobWorkflowInput{
		Queue:       "q",
		Type:        "test",
		MaxAttempts: 1,
	}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalClaim,
			temporal.ClaimSignal{WorkerID: "w1"})
	}, time.Millisecond)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalFail,
			temporal.FailSignal{Error: "fatal", Retry: true})
	}, 2*time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())

	var state temporal.JobState
	require.NoError(s.T(), s.env.GetWorkflowResult(&state))

	assert.Equal(s.T(), string(job.StatusFailed), state.Status,
		"should be terminal when attempts >= max_attempts")
}

func (s *JobWorkflowSuite) TestTimeout() {
	input := temporal.JobWorkflowInput{Queue: "q", Type: "test"}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalClaim,
			temporal.ClaimSignal{WorkerID: "w1"})
	}, time.Millisecond)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalTimeout, nil)
	}, 2*time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())

	var state temporal.JobState
	require.NoError(s.T(), s.env.GetWorkflowResult(&state))

	assert.Equal(s.T(), string(job.StatusTimeout), state.Status)
	assert.NotNil(s.T(), state.CompletedAt)
}

func (s *JobWorkflowSuite) TestCancelPending() {
	input := temporal.JobWorkflowInput{Queue: "q", Type: "test"}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalCancel, nil)
	}, time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())

	var state temporal.JobState
	require.NoError(s.T(), s.env.GetWorkflowResult(&state))

	assert.Equal(s.T(), string(job.StatusCancelled), state.Status)
}

func (s *JobWorkflowSuite) TestCancelActive() {
	input := temporal.JobWorkflowInput{Queue: "q", Type: "test"}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalClaim,
			temporal.ClaimSignal{WorkerID: "w1"})
	}, time.Millisecond)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalCancel, nil)
	}, 2*time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())

	var state temporal.JobState
	require.NoError(s.T(), s.env.GetWorkflowResult(&state))

	assert.Equal(s.T(), string(job.StatusCancelled), state.Status)
}

func (s *JobWorkflowSuite) TestHeartbeat() {
	input := temporal.JobWorkflowInput{Queue: "q", Type: "test"}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalClaim,
			temporal.ClaimSignal{WorkerID: "w1"})
	}, time.Millisecond)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalHbeat, nil)
	}, 2*time.Millisecond)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalComplete,
			temporal.CompleteSignal{})
	}, 3*time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())

	var state temporal.JobState
	require.NoError(s.T(), s.env.GetWorkflowResult(&state))

	assert.Equal(s.T(), string(job.StatusSucceeded), state.Status)
}

func (s *JobWorkflowSuite) TestDefaultMaxAttempts() {
	input := temporal.JobWorkflowInput{
		Queue: "q",
		Type:  "test",
		// MaxAttempts = 0 → should default to 3.
	}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(temporal.SignalCancel, nil)
	}, time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)

	var state temporal.JobState
	require.NoError(s.T(), s.env.GetWorkflowResult(&state))
	assert.Equal(s.T(), 3, state.MaxAttempts)
}

func (s *JobWorkflowSuite) TestQueryState() {
	input := temporal.JobWorkflowInput{
		Queue: "q",
		Type:  "test",
	}

	// Query after workflow has had time to start.
	s.env.RegisterDelayedCallback(func() {
		val, err := s.env.QueryWorkflow(temporal.QueryState)
		require.NoError(s.T(), err)

		var state temporal.JobState
		require.NoError(s.T(), val.Get(&state))
		assert.Equal(s.T(), string(job.StatusPending), state.Status)
		assert.Equal(s.T(), "q", state.Queue)
		assert.Equal(s.T(), "test", state.Type)

		// Complete the workflow.
		s.env.SignalWorkflow(temporal.SignalCancel, nil)
	}, time.Millisecond)

	s.env.ExecuteWorkflow(temporal.JobWorkflow, input)
	require.True(s.T(), s.env.IsWorkflowCompleted())
}

func TestJobWorkflowSuite(t *testing.T) {
	suite.Run(t, new(JobWorkflowSuite))
}
