package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestSafetyGuard_ReadAlwaysPasses(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	if err := SafetyGuard(cmd, SafetyRead); err != nil {
		t.Fatalf("read should always pass, got: %v", err)
	}
}

func TestSafetyGuard_DangerousRequiresForce(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("force", false, "")

	// Without --force, non-TTY should fail.
	err := SafetyGuard(cmd, SafetyDangerous)
	if err == nil {
		t.Fatal("dangerous without --force should fail in non-TTY")
	}

	// With --force, should pass.
	cmd2 := &cobra.Command{Use: "test"}
	cmd2.Flags().Bool("force", false, "")
	_ = cmd2.Flags().Set("force", "true")
	if err := SafetyGuard(cmd2, SafetyDangerous); err != nil {
		t.Fatalf("dangerous with --force should pass, got: %v", err)
	}
}

func TestSafetyGuard_NonTTYRequiresForce(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("force", false, "")

	// In test env, stdin is not a TTY.
	err := SafetyGuard(cmd, SafetyCaution)
	if err == nil {
		t.Fatal("caution in non-TTY without --force should fail")
	}
}
