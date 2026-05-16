package thefuck_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec/sources/thefuck"
)

const permDeniedRule = `def match(command):
    return 'permission denied' in command.output

def get_new_command(command):
    return 'sudo {}'.format(command.script)
`

const cmdNotFoundRule = `def match(command):
    return 'command not found' in command.output

def get_new_command(command):
    return 'apt install {}'.format(cmd)
`

const genericRule = `def match(command):
    return 'invalid option' in command.output

def get_new_command(command):
    return command.script.replace('--bad', '--good')
`

const sudoRule = `def match(command):
    return 'operation not permitted' in command.output

def get_new_command(command):
    return 'sudo {}'.format(command.script)
`

func TestParseRule_Cause_Permission(t *testing.T) {
	ep, err := thefuck.ParseRule("perm_check", permDeniedRule)
	require.NoError(t, err)
	require.NotNil(t, ep)
	assert.Equal(t, "permission", ep.Cause,
		"'permission denied' pattern classified as permission")
}

func TestParseRule_Cause_MissingDep(t *testing.T) {
	ep, err := thefuck.ParseRule("install_hint", cmdNotFoundRule)
	require.NoError(t, err)
	require.NotNil(t, ep)
	assert.Equal(t, "missing_dep", ep.Cause,
		"'command not found' pattern classified as missing_dep")
}

func TestParseRule_Cause_Default(t *testing.T) {
	ep, err := thefuck.ParseRule("option_fix", genericRule)
	require.NoError(t, err)
	require.NotNil(t, ep)
	assert.Equal(t, "bad_input", ep.Cause,
		"generic pattern falls back to bad_input")
}

func TestParseRule_Cause_FromName(t *testing.T) {
	ep, err := thefuck.ParseRule("sudo_command", sudoRule)
	require.NoError(t, err)
	require.NotNil(t, ep)
	assert.Equal(t, "permission", ep.Cause,
		"rule name containing 'sudo' classified as permission")
}

func TestParseRule_Fixes(t *testing.T) {
	ep, err := thefuck.ParseRule("perm_check", permDeniedRule)
	require.NoError(t, err)
	require.NotNil(t, ep)
	require.NotEmpty(t, ep.Fixes, "Fixes populated")
	assert.Equal(t, ep.Fix, ep.Fixes[0],
		"Fixes[0] matches Fix field")
}

func TestParseRule_Confidence_Single(t *testing.T) {
	ep, err := thefuck.ParseRule("perm_check", permDeniedRule)
	require.NoError(t, err)
	require.NotNil(t, ep)
	assert.Equal(t, float32(0.9), ep.Confidence,
		"single pattern yields 0.9 confidence")
}

func TestParseRule_Confidence_Multi(t *testing.T) {
	// gitPushRule has two patterns (git push + set-upstream).
	ep, err := thefuck.ParseRule("git_push", gitPushRule)
	require.NoError(t, err)
	require.NotNil(t, ep)
	assert.Equal(t, float32(0.8), ep.Confidence,
		"multi-pattern yields 0.8 confidence")
}
