package projects_test

import (
	"fmt"
	"os"

	"hop.top/kit/go/core/projects"
)

// withTempXDG sets XDG_CONFIG_HOME to a fresh temp dir for the duration
// of an Example function. Examples cannot take *testing.T, so the
// cleanup uses os.RemoveAll via the returned closer.
func withTempXDG() (cleanup func()) {
	dir, err := os.MkdirTemp("", "projects-example-*")
	if err != nil {
		panic(err)
	}
	prev, hadPrev := os.LookupEnv("XDG_CONFIG_HOME")
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	return func() {
		if hadPrev {
			_ = os.Setenv("XDG_CONFIG_HOME", prev)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
		_ = os.RemoveAll(dir)
	}
}

func ExampleRead() {
	defer withTempXDG()()

	if err := projects.Write("ops", projects.Entry{
		Path:   "/Users/jadb/.ops",
		Source: projects.SourceWSM,
	}); err != nil {
		panic(err)
	}

	file, err := projects.Read()
	if err != nil {
		panic(err)
	}

	fmt.Println(file.Projects["ops"].Path)
	// Output: /Users/jadb/.ops
}

func ExampleWrite() {
	defer withTempXDG()()

	err := projects.Write("kit", projects.Entry{
		Path:       "/Users/jadb/.w/ideacrafterslabs/kit",
		StartupCmd: "zsh",
		Source:     projects.SourceWSM,
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("ok")
	// Output: ok
}
