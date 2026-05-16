package usp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountTransitions_SingleSession(t *testing.T) {
	session := []ParsedCommand{
		{Tool: "git", SubCmd: "add"},
		{Tool: "git", SubCmd: "commit"},
		{Tool: "git", SubCmd: "push"},
	}
	tm := CountTransitions([][]ParsedCommand{session}, 1)
	assert.Equal(t, 1, tm["git add"]["git commit"])
	assert.Equal(t, 1, tm["git commit"]["git push"])
}

func TestCountTransitions_MultipleSessions(t *testing.T) {
	s1 := []ParsedCommand{
		{Tool: "git", SubCmd: "add"},
		{Tool: "git", SubCmd: "commit"},
	}
	s2 := []ParsedCommand{
		{Tool: "git", SubCmd: "add"},
		{Tool: "git", SubCmd: "commit"},
	}
	tm := CountTransitions([][]ParsedCommand{s1, s2}, 1)
	assert.Equal(t, 2, tm["git add"]["git commit"])
}

func TestCountTransitions_MinCountFilter(t *testing.T) {
	s1 := []ParsedCommand{
		{Tool: "git", SubCmd: "add"},
		{Tool: "git", SubCmd: "commit"},
	}
	s2 := []ParsedCommand{
		{Tool: "git", SubCmd: "status"},
		{Tool: "git", SubCmd: "diff"},
	}
	tm := CountTransitions([][]ParsedCommand{s1, s2}, 2)
	// Both transitions have count=1, so all pruned.
	assert.Empty(t, tm)
}

func TestCountTransitions_NoSubCmd(t *testing.T) {
	session := []ParsedCommand{
		{Tool: "ls"},
		{Tool: "cat"},
	}
	tm := CountTransitions([][]ParsedCommand{session}, 1)
	assert.Equal(t, 1, tm["ls"]["cat"])
}

func TestCountTransitions_CrossTool(t *testing.T) {
	session := []ParsedCommand{
		{Tool: "git", SubCmd: "commit"},
		{Tool: "gh", SubCmd: "pr"},
	}
	tm := CountTransitions([][]ParsedCommand{session}, 1)
	assert.Equal(t, 1, tm["git commit"]["gh pr"])
}
