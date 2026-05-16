package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterSetFlag_PrefixOnly(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	sf := RegisterSetFlag(cmd, "tag", "Manage tags", FlagDisplayPrefix)

	// --tag should exist
	assert.NotNil(t, cmd.Flags().Lookup("tag"))
	// --add-tag / --remove-tag should NOT exist
	assert.Nil(t, cmd.Flags().Lookup("add-tag"))
	assert.Nil(t, cmd.Flags().Lookup("remove-tag"))

	cmd.SetArgs([]string{"--tag", "feat", "--tag", "+docs"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, []string{"feat", "docs"}, sf.Values())
}

func TestRegisterSetFlag_VerboseOnly(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	sf := RegisterSetFlag(cmd, "tag", "Manage tags", FlagDisplayVerbose)

	// --add-tag / --remove-tag should exist
	assert.NotNil(t, cmd.Flags().Lookup("add-tag"))
	assert.NotNil(t, cmd.Flags().Lookup("remove-tag"))
	assert.NotNil(t, cmd.Flags().Lookup("clear-tag"))
	// --tag prefix should also be registered (hidden)
	assert.NotNil(t, cmd.Flags().Lookup("tag"))

	cmd.SetArgs([]string{"--add-tag", "feat", "--add-tag", "bug", "--remove-tag", "bug"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, []string{"feat"}, sf.Values())
}

func TestRegisterSetFlag_VerboseOnlyStillParsesPrefix(t *testing.T) {
	cmd := &cobra.Command{Use: "test", Run: func(*cobra.Command, []string) {}}
	sf := RegisterSetFlag(cmd, "tag", "tags", FlagDisplayVerbose)
	cmd.SetArgs([]string{"--tag", "+feat"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, []string{"feat"}, sf.Values())
}

func TestRegisterSetFlag_Both(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	sf := RegisterSetFlag(cmd, "tag", "Manage tags", FlagDisplayBoth)

	// All forms should exist
	assert.NotNil(t, cmd.Flags().Lookup("tag"))
	assert.NotNil(t, cmd.Flags().Lookup("add-tag"))
	assert.NotNil(t, cmd.Flags().Lookup("remove-tag"))
	assert.NotNil(t, cmd.Flags().Lookup("clear-tag"))

	// Mix both styles
	cmd.SetArgs([]string{"--tag", "a", "--add-tag", "b", "--tag", "-a"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, []string{"b"}, sf.Values())
}

func TestRegisterSetFlag_VerboseClear(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	sf := RegisterSetFlag(cmd, "tag", "Manage tags", FlagDisplayVerbose)
	sf.Set("pre-existing")

	cmd.SetArgs([]string{"--clear-tag"})
	require.NoError(t, cmd.Execute())
	assert.Empty(t, sf.Values())
}

func TestRegisterSetFlag_VerboseAddWithPrefixChar(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	sf := RegisterSetFlag(cmd, "tag", "Manage tags", FlagDisplayVerbose)

	// --add-tag "+ppl" should add literal "+ppl", not interpret the +
	cmd.SetArgs([]string{"--add-tag", "+ppl"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, []string{"+ppl"}, sf.Values())
}

func TestRegisterSetFlag_VerboseRemoveWithPrefixChar(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	sf := RegisterSetFlag(cmd, "tag", "Manage tags", FlagDisplayVerbose)
	sf.Add("+ppl")

	// --remove-tag "+ppl" should remove literal "+ppl"
	cmd.SetArgs([]string{"--remove-tag", "+ppl"})
	require.NoError(t, cmd.Execute())
	assert.Empty(t, sf.Values())
}

func TestRegisterTextFlag_VerboseAppendWithPrefixChar(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	tf := RegisterTextFlag(cmd, "desc", "Description", FlagDisplayVerbose)

	// --desc-append "+1 improvement" should append literal "+1 improvement"
	cmd.SetArgs([]string{"--desc", "base", "--desc-append", "+1 improvement"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "base\n+1 improvement", tf.Value())
}

func TestRegisterTextFlag_VerbosePrependWithPrefixChar(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	tf := RegisterTextFlag(cmd, "desc", "Description", FlagDisplayVerbose)

	// --desc-prepend "^caret" should prepend literal "^caret"
	cmd.SetArgs([]string{"--desc", "base", "--desc-prepend", "^caret"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "^caret\nbase", tf.Value())
}

func TestRegisterSetFlag_DefaultIsPrefix(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	RegisterSetFlag(cmd, "tag", "Manage tags", 0)

	assert.NotNil(t, cmd.Flags().Lookup("tag"))
	assert.Nil(t, cmd.Flags().Lookup("add-tag"))
}

func TestRegisterTextFlag_PrefixOnly(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	tf := RegisterTextFlag(cmd, "desc", "Description", FlagDisplayPrefix)

	assert.NotNil(t, cmd.Flags().Lookup("desc"))
	assert.Nil(t, cmd.Flags().Lookup("desc-append"))

	cmd.SetArgs([]string{"--desc", "base", "--desc", "+line2"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "base\nline2", tf.Value())
}

func TestRegisterTextFlag_VerboseOnly(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	tf := RegisterTextFlag(cmd, "desc", "Description", FlagDisplayVerbose)

	assert.NotNil(t, cmd.Flags().Lookup("desc"))
	assert.NotNil(t, cmd.Flags().Lookup("desc-append"))
	assert.NotNil(t, cmd.Flags().Lookup("desc-prepend"))

	cmd.SetArgs([]string{"--desc", "base", "--desc-append", "added"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "base\nadded", tf.Value())
}

func TestRegisterTextFlag_VerboseAppendInline(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	tf := RegisterTextFlag(cmd, "desc", "Description", FlagDisplayVerbose)

	cmd.SetArgs([]string{"--desc", "hello", "--desc-append-inline", " world"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "hello world", tf.Value())
}
