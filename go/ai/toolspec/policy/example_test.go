package policy_test

import (
	"fmt"

	"hop.top/kit/go/ai/toolspec/policy"
)

func Example() {
	table := policy.Default()
	read := table.Resolve(policy.SideEffectRead, policy.NetworkNone)
	push := table.Resolve(policy.SideEffectDestructive, policy.NetworkEgress)
	fmt.Println(read.Action)
	fmt.Println(push.Action)
	// Output:
	// auto-allow
	// deny
}
