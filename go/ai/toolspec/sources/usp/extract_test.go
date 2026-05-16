package usp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtract_BasicCommands(t *testing.T) {
	calls := []ToolCall{
		{Name: "Bash", Input: "git commit -m 'init'"},
		{Name: "Bash", Input: "docker build -t app ."},
	}
	got := Extract(calls)
	assert.Len(t, got, 2)
	assert.Equal(t, "git", got[0].Tool)
	assert.Equal(t, "commit", got[0].SubCmd)
	assert.Equal(t, []string{"-m", "init"}, got[0].Args)
	assert.Equal(t, "docker", got[1].Tool)
	assert.Equal(t, "build", got[1].SubCmd)
}

func TestExtract_SkipsNonBash(t *testing.T) {
	calls := []ToolCall{
		{Name: "Read", Input: "/some/file.go"},
		{Name: "Bash", Input: "ls -la"},
		{Name: "Grep", Input: "pattern"},
	}
	got := Extract(calls)
	assert.Len(t, got, 1)
	assert.Equal(t, "ls", got[0].Tool)
	assert.Equal(t, "", got[0].SubCmd) // -la is a flag
	assert.Equal(t, []string{"-la"}, got[0].Args)
}

func TestExtract_ShellVariant(t *testing.T) {
	calls := []ToolCall{
		{Name: "shell", Input: "gh pr list"},
		{Name: "Shell", Input: "npm install"},
	}
	got := Extract(calls)
	assert.Len(t, got, 2)
	assert.Equal(t, "gh", got[0].Tool)
	assert.Equal(t, "pr", got[0].SubCmd)
}

func TestExtract_Pipes(t *testing.T) {
	calls := []ToolCall{
		{Name: "Bash", Input: "git log --oneline | head -5"},
	}
	got := Extract(calls)
	assert.Len(t, got, 1)
	assert.Equal(t, "git", got[0].Tool)
	assert.Equal(t, "log", got[0].SubCmd)
	assert.Equal(t, []string{"--oneline"}, got[0].Args)
}

func TestExtract_QuotedStrings(t *testing.T) {
	calls := []ToolCall{
		{Name: "Bash", Input: `git commit -m "hello world"`},
	}
	got := Extract(calls)
	assert.Len(t, got, 1)
	assert.Equal(t, "commit", got[0].SubCmd)
	assert.Equal(t, []string{"-m", "hello world"}, got[0].Args)
}

func TestExtract_EmptyInput(t *testing.T) {
	calls := []ToolCall{
		{Name: "Bash", Input: ""},
		{Name: "Bash", Input: "   "},
	}
	got := Extract(calls)
	assert.Empty(t, got)
}

func TestExtract_PipeInQuotes(t *testing.T) {
	calls := []ToolCall{
		{Name: "Bash", Input: `echo "hello | world"`},
	}
	got := Extract(calls)
	assert.Len(t, got, 1)
	assert.Equal(t, "echo", got[0].Tool)
	assert.Equal(t, "hello | world", got[0].SubCmd)
}

func TestExtract_FlagAsSecondToken(t *testing.T) {
	calls := []ToolCall{
		{Name: "Bash", Input: "ls -la /tmp"},
	}
	got := Extract(calls)
	assert.Len(t, got, 1)
	assert.Equal(t, "ls", got[0].Tool)
	assert.Equal(t, "", got[0].SubCmd)
	assert.Equal(t, []string{"-la", "/tmp"}, got[0].Args)
}
