package scope_test

import (
	"fmt"
	"os"

	scopecmd "hop.top/kit/go/console/cli/scope"
	scopepkg "hop.top/kit/go/core/scope"
)

func ExampleCmd() {
	restore := scopepkg.SetDefault(scopepkg.New().
		SetMode(scopepkg.Strict).
		Allow("/srv/app/**").
		Deny("/srv/app/secrets/**"))
	defer restore()

	root := scopecmd.Cmd()
	root.SetOut(os.Stdout)
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetArgs([]string{"check", "/srv/app/secrets/db.env", "--op", "write"})

	err := root.Execute()
	fmt.Println("denied:", scopecmd.IsDeniedExit(err))
	// Output:
	// PATH                     OP     DECISION
	// /srv/app/secrets/db.env  write  denied
	// denied: true
}
