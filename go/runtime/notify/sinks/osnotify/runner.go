package osnotifysink

import (
	"context"
	"os/exec"
)

// runner abstracts os/exec.CommandContext so unit tests can assert
// command construction without shelling out. Production code uses
// execRunner; tests inject a fake via the unexported withRunner
// Option.
type runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

// execRunner is the production runner that actually shells out via
// exec.CommandContext. Honors ctx cancellation through the standard
// os/exec semantics.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}
