package serve_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/console/serve"
)

// noopService is the smallest serve.Service: it reports ready and
// then blocks until canceled.
type noopService struct{ name string }

func (s noopService) Name() string { return s.name }

func (s noopService) Start(ctx context.Context, ready func()) error {
	ready()
	<-ctx.Done()
	return nil
}

func (s noopService) Ready() bool { return true }

func (s noopService) Stop(context.Context) error { return nil }

func ExampleResolve() {
	reg := serve.NewRegistry()
	reg.Register(noopService{name: "api"})
	reg.Register(noopService{name: "mcp"})

	configs := map[string]serve.Config{
		"api": {Enabled: true},
		"mcp": {Enabled: false},
	}

	// Supervisor form: every configured AND enabled service.
	all := serve.Resolve(reg, serve.Request{Configs: configs})
	fmt.Println(all.Selected, all.Skipped, all.Err == nil)

	// Selector form: the named service, even when disabled.
	one := serve.Resolve(reg, serve.Request{Args: []string{"mcp"}, Configs: configs})
	fmt.Println(one.Selected, one.Explicit)

	// Unknown name: the refusal already carries its exit code.
	bad := serve.Resolve(reg, serve.Request{Args: []string{"nope"}, Configs: configs})
	fmt.Println(bad.Err.ExitCode, serve.ExitCodeFor(serve.OutcomeUnknownService))

	// Output:
	// [api] [mcp] true
	// [mcp] true
	// 3 3
}
