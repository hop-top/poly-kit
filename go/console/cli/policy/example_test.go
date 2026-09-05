package policy_test

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli/policy"
)

func ExampleEngine() {
	root := &cobra.Command{Use: "kit"}
	del := &cobra.Command{
		Use:         "delete",
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	drop := &cobra.Command{
		Use:         "drop",
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	root.AddCommand(del, drop)

	p := policy.Policy{
		Name:           "ops",
		Allow:          map[policy.SideEffect][]string{policy.SideEffectDestructive: {"delete:*"}},
		RequireConfirm: []string{"delete:*"},
	}
	e := policy.NewEngine(p, 1)

	allowed, confirm, _ := e.Authorize(del)
	fmt.Println("delete:", allowed, confirm)
	allowed, _, reason := e.Authorize(drop)
	fmt.Println("drop:", allowed, reason)

	fmt.Println(e.RecordOp(del))
	fmt.Println(errors.Is(e.RecordOp(del), policy.ErrMaxOpsExceeded))
	// Output:
	// delete: true true
	// drop: false policy: destructive not allowed for drop
	// <nil>
	// true
}
