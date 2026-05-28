// White-box tests for buildFlagSet precedence rules. Targets the
// --with-bus-workflows vs --without-bus-workflows conflict resolution
// (Comment 3293191447): when both flags are supplied, --without- must
// win because "off" is the safer default behavior and the comment
// pinned the same rule.
package kitinit

import (
	"testing"
)

// callBuildFlagSet replicates the InitCmd-internal call shape without
// invoking the full RunE path. We construct InitCmd (which wires the
// cobra FlagSet declarations) then call buildFlagSet directly with the
// same pointer locals after parsing a custom argv. This isolates the
// switch in buildFlagSet that resolves --with/--without conflicts.
func callBuildFlagSet(t *testing.T, argv []string) *FlagSet {
	t.Helper()
	cmd := InitCmd(nil)
	// Don't run cmd.Execute() — that would trigger RunE and pull in
	// detect/Gather/bootstrap. We only need the FlagSet parse step
	// to populate Changed() so buildFlagSet's branches fire.
	cmd.SetArgs(argv)
	// Parse the flags without running RunE. cobra's Flags().Parse
	// honors Changed() on the FlagSet just like cmd.Execute() would.
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("ParseFlags(%v): %v", argv, err)
	}

	getBool := func(name string) bool {
		v, err := cmd.Flags().GetBool(name)
		if err != nil {
			t.Fatalf("GetBool %s: %v", name, err)
		}
		return v
	}
	withBus := getBool("with-bus-workflows")
	withoutBus := getBool("without-bus-workflows")
	withGH := getBool("with-github-workflows")
	withoutGH := getBool("without-github-workflows")
	withPrePr := getBool("with-githook-pre-pr")
	withoutPrePr := getBool("without-githook-pre-pr")
	withPostPr := getBool("with-githook-post-pr-open")
	withoutPostPr := getBool("without-githook-post-pr-open")

	var (
		fromFlag, moduleFlag, modeFlag, accountTypeFlag, orgFlag,
		visibilityFlag, licenseFlag, defaultBranchFlag,
		emailFlag, themeFlag, descriptionFlag string
		authorFlag  []string
		runtimeFlag []string
		tierFlag    int
		noGitHubFlag, noPushFlag, hopFlag,
		dryRunFlag, forceFlag, yesFlag bool
	)
	return buildFlagSet(cmd,
		&fromFlag, &moduleFlag, runtimeFlag, &tierFlag, &modeFlag,
		&accountTypeFlag, &orgFlag, &visibilityFlag, &noGitHubFlag,
		&noPushFlag, &licenseFlag, &hopFlag, &defaultBranchFlag,
		authorFlag, &emailFlag, &themeFlag, &descriptionFlag,
		&dryRunFlag, &forceFlag, &yesFlag,
		&withGH, &withoutGH,
		&withPrePr, &withoutPrePr,
		&withPostPr, &withoutPostPr,
		&withBus, &withoutBus,
	)
}

// TestBuildFlagSet_WithoutBusWorkflowsWinsWhenBothSet pins the
// resolution rule: --without-bus-workflows wins when both --with- and
// --without- are passed. The current code does the opposite (--with-
// wins). Comment 3293191447 calls this out as a real bug because the
// init.go doc-comment explicitly claims --without- wins.
func TestBuildFlagSet_WithoutBusWorkflowsWinsWhenBothSet(t *testing.T) {
	t.Parallel()
	fs := callBuildFlagSet(t, []string{
		"--with-bus-workflows",
		"--without-bus-workflows",
	})
	if fs.WithBusWorkflows == nil {
		t.Fatal("WithBusWorkflows == nil; expected a non-nil *bool resolving to false")
	}
	if *fs.WithBusWorkflows {
		t.Errorf("WithBusWorkflows = true; want false (--without- must win when both flags are set)")
	}
}

// TestBuildFlagSet_WithBusWorkflowsAloneSetsTrue is a control: only
// --with-bus-workflows passed → WithBusWorkflows resolves true.
func TestBuildFlagSet_WithBusWorkflowsAloneSetsTrue(t *testing.T) {
	t.Parallel()
	fs := callBuildFlagSet(t, []string{"--with-bus-workflows"})
	if fs.WithBusWorkflows == nil {
		t.Fatal("WithBusWorkflows == nil; expected non-nil *bool resolving to true")
	}
	if !*fs.WithBusWorkflows {
		t.Errorf("WithBusWorkflows = false; want true when --with- supplied alone")
	}
}

// TestBuildFlagSet_WithoutBusWorkflowsAloneSetsFalse is a control:
// only --without-bus-workflows passed → WithBusWorkflows resolves
// false (explicitly disabled).
func TestBuildFlagSet_WithoutBusWorkflowsAloneSetsFalse(t *testing.T) {
	t.Parallel()
	fs := callBuildFlagSet(t, []string{"--without-bus-workflows"})
	if fs.WithBusWorkflows == nil {
		t.Fatal("WithBusWorkflows == nil; expected non-nil *bool resolving to false")
	}
	if *fs.WithBusWorkflows {
		t.Errorf("WithBusWorkflows = true; want false when --without- supplied alone")
	}
}
