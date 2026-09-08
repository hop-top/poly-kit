package harness

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
)

// TestWithPromptAnswers_PayloadSurvivesPrompt asserts the harness can
// drive a prompted confirm without consuming the command's stdin.
//
// This is the shape adopters could not express before: WithStdin fed
// one reader to both the prompt and the payload, so a y/N answer and
// the payload were indistinguishable. The payload is larger than
// bufio's buffer, which is where the old truncation hid.
func TestWithPromptAnswers_PayloadSurvivesPrompt(t *testing.T) {
	const size = 9001
	body := strings.Repeat("A", size)

	var got []byte
	leaf := &cobra.Command{
		Use: "eat", Short: "eat", Long: "Eat stdin.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := io.ReadAll(cmd.InOrStdin())
			got = b
			return err
		},
	}
	cli.SetSideEffect(leaf, cli.SideEffectDestructive)
	cli.SetIdempotency(leaf, cli.IdempotencyYes)

	r := cli.New(cli.Config{Name: "htool", Version: "0.0.0", Short: "h"})
	r.Cmd.AddCommand(leaf)
	r.AutoRegisterFlags()
	r.WrapRunE()

	c := apply([]Option{
		Args("eat", "--confirm", "prompt"),
		WithStdin(strings.NewReader(body)),
		WithPromptAnswers(strings.NewReader("y\n")),
	})
	res := runCaptured(c, r.Cmd)

	if res.exitCode != 0 {
		t.Fatalf("prompted confirm answered y must proceed, exit=%d err=%v",
			res.exitCode, res.runErr)
	}
	if len(got) != size {
		t.Fatalf("payload truncated by the prompt: got %d bytes, want %d",
			len(got), size)
	}
	if string(got) != body {
		t.Fatal("payload not byte-exact after a prompted confirm")
	}
}

// TestWithPromptAnswers_DeclineRefuses asserts a scripted "n" refuses.
func TestWithPromptAnswers_DeclineRefuses(t *testing.T) {
	leaf := &cobra.Command{
		Use: "eat", Short: "eat", Long: "Eat stdin.",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	cli.SetSideEffect(leaf, cli.SideEffectDestructive)
	cli.SetIdempotency(leaf, cli.IdempotencyYes)

	r := cli.New(cli.Config{Name: "htool", Version: "0.0.0", Short: "h"})
	r.Cmd.AddCommand(leaf)
	r.AutoRegisterFlags()
	r.WrapRunE()

	c := apply([]Option{
		Args("eat", "--confirm", "prompt"),
		WithPromptAnswers(strings.NewReader("n\n")),
	})
	if res := runCaptured(c, r.Cmd); res.exitCode == 0 {
		t.Fatal("a scripted decline must refuse")
	}
}
