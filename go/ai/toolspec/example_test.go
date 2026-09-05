package toolspec_test

import (
	"fmt"

	"hop.top/kit/go/ai/toolspec"
)

// ExampleToolSpec_FindCommand shows the breadth-first lookup: a shallow
// match wins over a deeper one with the same name.
func ExampleToolSpec_FindCommand() {
	spec := &toolspec.ToolSpec{
		Name: "myctl",
		Commands: []toolspec.Command{
			{Name: "deploy", Children: []toolspec.Command{
				{Name: "rollback"},
				{Name: "status"},
			}},
			{Name: "status"},
		},
	}

	fmt.Println(spec.FindCommand("deploy").Name)
	fmt.Println(spec.FindCommand("rollback").Name)
	fmt.Println(spec.FindCommand("missing") == nil)
	// Output:
	// deploy
	// rollback
	// true
}

// ExampleMerge shows that overlay only fills fields the base leaves empty,
// and that slice fields are all-or-nothing rather than element-wise.
func ExampleMerge() {
	base := &toolspec.ToolSpec{
		Name:     "myctl",
		Commands: []toolspec.Command{{Name: "deploy"}},
	}
	overlay := &toolspec.ToolSpec{
		Name:          "ignored",
		SchemaVersion: "1",
		Commands:      []toolspec.Command{{Name: "status"}, {Name: "logs"}},
		Flags:         []toolspec.Flag{{Name: "--verbose"}},
	}

	merged := toolspec.Merge(base, overlay)

	// Base wins where it is non-empty.
	fmt.Println(merged.Name)
	// Overlay fills the empty scalar.
	fmt.Println(merged.SchemaVersion)
	// Base's non-empty slice is kept whole; overlay's is not merged in.
	fmt.Println(len(merged.Commands), merged.Commands[0].Name)
	// Base's empty slice is replaced wholesale.
	fmt.Println(len(merged.Flags), merged.Flags[0].Name)
	// Output:
	// myctl
	// 1
	// 1 deploy
	// 1 --verbose
}

// ExampleSourceFunc adapts a plain function to the Source interface.
func ExampleSourceFunc() {
	src := toolspec.SourceFunc(func(tool string) (*toolspec.ToolSpec, error) {
		return &toolspec.ToolSpec{Name: tool}, nil
	})

	spec, err := src.Resolve("docker")
	if err != nil {
		panic(err)
	}
	fmt.Println(spec.Name)
	// Output:
	// docker
}
