package router_test

import (
	"fmt"

	"hop.top/kit/go/console/cli/router"
)

func ExampleCmd() {
	cmd := router.Cmd()
	fmt.Println(cmd.Use)
	for _, sub := range cmd.Commands() {
		fmt.Println(" ", sub.Name())
	}
	// Output:
	// router
	//   config
	//   list
	//   start
	//   stop
}
