package celpermission_test

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/celpermission"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/transport/cmdsurface"
)

func Example() {
	// In a tool: cli.New(cfg, cli.WithPolicy(...), celpermission.With(), ...)
	// compiles the permissions: block of the --policy it serves under.
	gate, err := celpermission.New([]policy.PermissionRule{{
		Name:      "acme-only",
		When:      `principal.tenant == "acme"`,
		Effect:    policy.RuleAllow,
		Otherwise: policy.RuleDeny,
		Message:   "acme tenants only",
	}})
	if err != nil {
		panic(err)
	}

	root := &cobra.Command{Use: "tool"}
	root.AddCommand(&cobra.Command{
		Use:         "list",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	})
	b := cmdsurface.New(root, cmdsurface.WithPermission(gate))
	b.Expose("*", cmdsurface.SurfaceREST)

	_, err = b.Invoke(context.Background(), cmdsurface.Invocation{
		Path: []string{"list"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Caller: "bob", Tenant: "globex"},
	})
	fmt.Println(err)

	// Output:
	// cmdsurface: permission denied: list on rest: permission rule "acme-only": acme tenants only
}
