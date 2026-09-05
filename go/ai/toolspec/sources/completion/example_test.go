package completion_test

import (
	"fmt"

	"hop.top/kit/go/ai/toolspec/sources/completion"
)

func Example() {
	script := `_demo_commands=(
  'list:List items'
  'add:Add an item'
)
_arguments '--verbose[Print more output]'`
	spec := completion.ParseZshCompletion("demo", script)
	for _, c := range spec.Commands {
		fmt.Println("command:", c.Name)
	}
	for _, f := range spec.Flags {
		fmt.Println("flag:", f.Name, f.Description)
	}
	// Output:
	// command: list
	// command: add
	// flag: --verbose Print more output
}
