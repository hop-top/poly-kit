package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
)

// rootWith is a tiny test fixture builder so each test case can read
// linearly without rebuilding cobra trees by hand.
func rootWith(name string, mods ...func(*cobra.Command)) *cobra.Command {
	root := &cobra.Command{Use: name}
	for _, m := range mods {
		m(root)
	}
	return root
}

func addChild(name string, mods ...func(*cobra.Command)) func(*cobra.Command) {
	return func(parent *cobra.Command) {
		c := &cobra.Command{Use: name, Run: func(*cobra.Command, []string) {}}
		for _, m := range mods {
			m(c)
		}
		parent.AddCommand(c)
	}
}

// findCommand returns the projected toolspec.Command with the given
// leaf name, or nil. Recursive so deep trees work too.
func findCommand(cmds []toolspec.Command, name string) *toolspec.Command {
	for i := range cmds {
		if cmds[i].Name == name {
			return &cmds[i]
		}
		if c := findCommand(cmds[i].Children, name); c != nil {
			return c
		}
	}
	return nil
}

// --- Basic shape ---------------------------------------------------

func TestWalkCobra_NilRoot(t *testing.T) {
	spec := WalkCobra(nil)
	require.NotNil(t, spec)
	assert.Empty(t, spec.Name)
	assert.Empty(t, spec.Commands)
}

func TestWalkCobra_EmptyRoot(t *testing.T) {
	root := rootWith("mytool")
	spec := WalkCobra(root)
	require.NotNil(t, spec)
	assert.Equal(t, "mytool", spec.Name)
	assert.Empty(t, spec.Commands)
}

func TestWalkCobra_SimpleChildren(t *testing.T) {
	root := rootWith("mytool",
		addChild("list"),
		addChild("create"),
	)
	spec := WalkCobra(root)
	require.Len(t, spec.Commands, 2)
	names := []string{spec.Commands[0].Name, spec.Commands[1].Name}
	assert.ElementsMatch(t, []string{"list", "create"}, names)
}

func TestWalkCobra_RecursiveChildren(t *testing.T) {
	root := rootWith("mytool",
		addChild("task",
			addChild("list"),
			addChild("create"),
			addChild("delete"),
		),
	)
	spec := WalkCobra(root)
	require.Len(t, spec.Commands, 1)
	task := spec.Commands[0]
	assert.Equal(t, "task", task.Name)
	require.Len(t, task.Children, 3)
	assert.NotNil(t, findCommand(spec.Commands, "delete"))
}

// --- Skip rules ----------------------------------------------------

func TestWalkCobra_SkipsHidden(t *testing.T) {
	root := rootWith("mytool",
		addChild("public"),
		addChild("secret", func(c *cobra.Command) { c.Hidden = true }),
	)
	spec := WalkCobra(root)
	assert.NotNil(t, findCommand(spec.Commands, "public"))
	assert.Nil(t, findCommand(spec.Commands, "secret"))
}

func TestWalkCobra_IncludeHidden(t *testing.T) {
	root := rootWith("mytool",
		addChild("public"),
		addChild("secret", func(c *cobra.Command) { c.Hidden = true }),
	)
	spec := WalkCobra(root, WithIncludeHidden())
	assert.NotNil(t, findCommand(spec.Commands, "secret"))
}

func TestWalkCobra_SkipsBuiltins(t *testing.T) {
	root := rootWith("mytool",
		addChild("real"),
		addChild("help"),
		addChild("completion"),
		addChild("__complete"),
	)
	spec := WalkCobra(root)
	assert.NotNil(t, findCommand(spec.Commands, "real"))
	assert.Nil(t, findCommand(spec.Commands, "help"))
	assert.Nil(t, findCommand(spec.Commands, "completion"))
	assert.Nil(t, findCommand(spec.Commands, "__complete"))
}

func TestWalkCobra_SkipsSpecCommandAnnotation(t *testing.T) {
	root := rootWith("mytool",
		addChild("real"),
		addChild("spec", func(c *cobra.Command) {
			c.Annotations = map[string]string{"kit/spec-command": "true"}
		}),
	)
	spec := WalkCobra(root)
	assert.NotNil(t, findCommand(spec.Commands, "real"))
	assert.Nil(t, findCommand(spec.Commands, "spec"))
}

func TestWalkCobra_CustomSkip(t *testing.T) {
	root := rootWith("mytool",
		addChild("keep"),
		addChild("drop-me"),
	)
	spec := WalkCobra(root, WithSkip(func(c *cobra.Command) bool {
		return c.Name() == "drop-me"
	}))
	assert.NotNil(t, findCommand(spec.Commands, "keep"))
	assert.Nil(t, findCommand(spec.Commands, "drop-me"))
}

// --- Safety inference ----------------------------------------------

func TestWalkCobra_DefaultSafetySafe(t *testing.T) {
	root := rootWith("mytool", addChild("list"))
	spec := WalkCobra(root)
	list := findCommand(spec.Commands, "list")
	require.NotNil(t, list)
	require.NotNil(t, list.Safety)
	assert.Equal(t, toolspec.SafetyLevelSafe, list.Safety.Level)
	assert.False(t, list.Safety.RequiresConfirmation)
}

func TestWalkCobra_DefaultSafetyDestructive(t *testing.T) {
	for _, name := range []string{"delete", "remove", "rm", "destroy", "purge", "drop"} {
		t.Run(name, func(t *testing.T) {
			root := rootWith("mytool", addChild(name))
			spec := WalkCobra(root)
			cmd := findCommand(spec.Commands, name)
			require.NotNil(t, cmd, "command %q missing from spec", name)
			require.NotNil(t, cmd.Safety, "Safety nil for %q", name)
			assert.Equal(t, toolspec.SafetyLevelDangerous, cmd.Safety.Level)
			assert.True(t, cmd.Safety.RequiresConfirmation)
		})
	}
}

func TestWalkCobra_CustomSafety(t *testing.T) {
	root := rootWith("mytool", addChild("delete"))
	spec := WalkCobra(root, WithCustomSafety(func(c *cobra.Command) *toolspec.Safety {
		// Override: never classify as dangerous.
		return &toolspec.Safety{Level: toolspec.SafetyLevelCaution}
	}))
	del := findCommand(spec.Commands, "delete")
	require.NotNil(t, del)
	require.NotNil(t, del.Safety)
	assert.Equal(t, toolspec.SafetyLevelCaution, del.Safety.Level)
	assert.False(t, del.Safety.RequiresConfirmation)
}

func TestWalkCobra_CustomSafetyNil(t *testing.T) {
	// Returning nil from the safety classifier produces a Command
	// with no Safety field (consumers see it as "unknown").
	root := rootWith("mytool", addChild("list"))
	spec := WalkCobra(root, WithCustomSafety(func(c *cobra.Command) *toolspec.Safety {
		return nil
	}))
	list := findCommand(spec.Commands, "list")
	require.NotNil(t, list)
	assert.Nil(t, list.Safety)
}

// --- Deprecation ---------------------------------------------------

func TestWalkCobra_DeprecatedCobraField(t *testing.T) {
	root := rootWith("mytool",
		addChild("old", func(c *cobra.Command) {
			c.Deprecated = "use 'new' instead"
		}),
	)
	spec := WalkCobra(root)
	old := findCommand(spec.Commands, "old")
	require.NotNil(t, old)
	assert.True(t, old.Deprecated)
	assert.Equal(t, "use 'new' instead", old.DeprecatedSince)
}

func TestWalkCobra_DeprecatedAnnotations(t *testing.T) {
	root := rootWith("mytool",
		addChild("old", func(c *cobra.Command) {
			c.Annotations = map[string]string{
				"kit/deprecated-since": "1.2",
				"kit/replaced-by":      "newcmd",
			}
		}),
	)
	spec := WalkCobra(root)
	old := findCommand(spec.Commands, "old")
	require.NotNil(t, old)
	assert.True(t, old.Deprecated)
	assert.Equal(t, "1.2", old.DeprecatedSince)
	assert.Equal(t, "newcmd", old.ReplacedBy)
}

func TestWalkCobra_WithoutDeprecated(t *testing.T) {
	root := rootWith("mytool",
		addChild("old", func(c *cobra.Command) {
			c.Deprecated = "use 'new'"
		}),
	)
	spec := WalkCobra(root, WithoutDeprecated())
	old := findCommand(spec.Commands, "old")
	require.NotNil(t, old, "command should still appear; only deprecation metadata suppressed")
	assert.False(t, old.Deprecated)
	assert.Empty(t, old.DeprecatedSince)
}

// --- Contract from annotations -------------------------------------

func TestWalkCobra_ContractFromAnnotations(t *testing.T) {
	root := rootWith("mytool",
		addChild("write", func(c *cobra.Command) {
			c.Annotations = map[string]string{
				"kit/side-effect": "write",
				"kit/idempotent":  "yes",
			}
		}),
	)
	spec := WalkCobra(root)
	w := findCommand(spec.Commands, "write")
	require.NotNil(t, w)
	require.NotNil(t, w.Contract)
	assert.True(t, w.Contract.Idempotent)
	assert.Equal(t, []string{"write"}, w.Contract.SideEffects)
}

func TestWalkCobra_ContractAbsent(t *testing.T) {
	root := rootWith("mytool", addChild("plain"))
	spec := WalkCobra(root)
	p := findCommand(spec.Commands, "plain")
	require.NotNil(t, p)
	assert.Nil(t, p.Contract)
}

// --- Aliases -------------------------------------------------------

func TestWalkCobra_AliasesPropagate(t *testing.T) {
	root := rootWith("mytool",
		addChild("list", func(c *cobra.Command) {
			c.Aliases = []string{"ls", "l"}
		}),
	)
	spec := WalkCobra(root)
	list := findCommand(spec.Commands, "list")
	require.NotNil(t, list)
	assert.Equal(t, []string{"ls", "l"}, list.Aliases)
}

// --- Flags: persistent at root, local at command ------------------

func TestWalkCobra_PersistentFlagsAtRoot(t *testing.T) {
	root := rootWith("mytool")
	root.PersistentFlags().String("config", "", "config path")
	root.PersistentFlags().BoolP("verbose", "v", false, "verbose")
	spec := WalkCobra(root)
	require.Len(t, spec.Flags, 2)
	names := []string{spec.Flags[0].Name, spec.Flags[1].Name}
	assert.ElementsMatch(t, []string{"config", "verbose"}, names)
}

func TestWalkCobra_LocalFlagsOnCommand(t *testing.T) {
	root := rootWith("mytool",
		addChild("deploy", func(c *cobra.Command) {
			c.Flags().String("env", "staging", "target env")
			c.Flags().BoolP("dry-run", "n", false, "no-op")
		}),
	)
	spec := WalkCobra(root)
	dep := findCommand(spec.Commands, "deploy")
	require.NotNil(t, dep)
	require.Len(t, dep.Flags, 2)
}

func TestWalkCobra_LocalFlagsExcludeInherited(t *testing.T) {
	// A persistent flag declared on the root must NOT show up as a
	// local flag on children — that would double-count it (already
	// at spec.Flags root level).
	root := rootWith("mytool", addChild("list"))
	root.PersistentFlags().String("config", "", "config path")
	spec := WalkCobra(root)
	list := findCommand(spec.Commands, "list")
	require.NotNil(t, list)
	for _, f := range list.Flags {
		assert.NotEqual(t, "config", f.Name, "inherited persistent flag leaked into local flags")
	}
}

func TestWalkCobra_HiddenFlagsSkipped(t *testing.T) {
	root := rootWith("mytool",
		addChild("deploy", func(c *cobra.Command) {
			c.Flags().String("public", "", "ok")
			f := c.Flags().Lookup("public")
			_ = f
			c.Flags().String("secret", "", "")
			c.Flags().Lookup("secret").Hidden = true
		}),
	)
	spec := WalkCobra(root)
	dep := findCommand(spec.Commands, "deploy")
	require.NotNil(t, dep)
	for _, f := range dep.Flags {
		assert.NotEqual(t, "secret", f.Name, "hidden flag leaked")
	}
}

func TestWalkCobra_DeprecatedFlagMarked(t *testing.T) {
	root := rootWith("mytool",
		addChild("deploy", func(c *cobra.Command) {
			c.Flags().String("legacy", "", "")
			require.NoError(t, c.Flags().MarkDeprecated("legacy", "use --modern instead"))
		}),
	)
	spec := WalkCobra(root)
	dep := findCommand(spec.Commands, "deploy")
	require.NotNil(t, dep)
	var legacy *toolspec.Flag
	for i := range dep.Flags {
		if dep.Flags[i].Name == "legacy" {
			legacy = &dep.Flags[i]
		}
	}
	require.NotNil(t, legacy)
	assert.True(t, legacy.Deprecated)
}

// --- Schema version ------------------------------------------------

func TestWalkCobra_SchemaVersionEmpty(t *testing.T) {
	// WalkCobra leaves SchemaVersion empty; RegisterSpecCommand sets
	// it from its own arg. Direct callers set it post-walk.
	root := rootWith("mytool")
	spec := WalkCobra(root)
	assert.Empty(t, spec.SchemaVersion)
}

// --- Integration: deeply-nested tree with mixed annotations -------

func TestWalkCobra_RealisticTree(t *testing.T) {
	root := rootWith("mytool")
	root.PersistentFlags().String("config", "", "config")

	// task list (read)
	// task delete (destructive heuristic)
	// task archive (annotation: write + idempotent)
	root.AddCommand(&cobra.Command{
		Use: "task",
		Run: func(*cobra.Command, []string) {},
	})
	task := root.Commands()[0]
	task.AddCommand(&cobra.Command{
		Use: "list",
		Run: func(*cobra.Command, []string) {},
	})
	task.AddCommand(&cobra.Command{
		Use: "delete",
		Run: func(*cobra.Command, []string) {},
	})
	task.AddCommand(&cobra.Command{
		Use: "archive",
		Run: func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			"kit/side-effect": "write",
			"kit/idempotent":  "yes",
		},
	})

	spec := WalkCobra(root)

	require.Equal(t, "mytool", spec.Name)
	require.Len(t, spec.Flags, 1)
	assert.Equal(t, "config", spec.Flags[0].Name)

	taskCmd := findCommand(spec.Commands, "task")
	require.NotNil(t, taskCmd)
	require.Len(t, taskCmd.Children, 3)

	listCmd := findCommand(spec.Commands, "list")
	require.NotNil(t, listCmd)
	require.NotNil(t, listCmd.Safety)
	assert.Equal(t, toolspec.SafetyLevelSafe, listCmd.Safety.Level)
	assert.Nil(t, listCmd.Contract)

	delCmd := findCommand(spec.Commands, "delete")
	require.NotNil(t, delCmd)
	require.NotNil(t, delCmd.Safety)
	assert.Equal(t, toolspec.SafetyLevelDangerous, delCmd.Safety.Level)
	assert.True(t, delCmd.Safety.RequiresConfirmation)

	archCmd := findCommand(spec.Commands, "archive")
	require.NotNil(t, archCmd)
	require.NotNil(t, archCmd.Contract)
	assert.True(t, archCmd.Contract.Idempotent)
	assert.Equal(t, []string{"write"}, archCmd.Contract.SideEffects)
}
