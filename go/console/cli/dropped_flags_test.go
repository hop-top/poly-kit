package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"hop.top/kit/go/console/cli"
)

func TestDroppedFlagsAbsentFromHelp(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.Cmd.SetArgs([]string{"--help"})
	_ = r.Cmd.Execute()
	out := buf.String()
	for _, gone := range []string{"--profile", "--instance"} {
		if strings.Contains(out, gone) {
			t.Errorf("%s still advertised in help", gone)
		}
	}
	if !strings.Contains(out, "--offline") {
		t.Error("--offline missing from help")
	}
}

func TestDroppedFlagsRejected(t *testing.T) {
	for _, arg := range []string{"--profile=x", "--instance=y"} {
		r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
		var buf bytes.Buffer
		r.Cmd.SetOut(&buf)
		r.Cmd.SetErr(&buf)
		r.Cmd.SetArgs([]string{arg})
		if err := r.Cmd.Execute(); err == nil {
			t.Errorf("%s was accepted; expected unknown-flag error", arg)
		}
	}
}
