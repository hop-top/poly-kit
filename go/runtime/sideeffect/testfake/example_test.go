package testfake_test

import (
	"testing"

	"hop.top/kit/go/runtime/sideeffect"
	"hop.top/kit/go/runtime/sideeffect/testfake"
)

func writeConfig(fs sideeffect.FS) error {
	return fs.WriteFile("/etc/app.yaml", []byte("key: v"), 0o600)
}

func TestWriteConfig(t *testing.T) {
	fs := testfake.NewFS(t).Allow(func(c testfake.Call) bool {
		return c.Method == "FS.WriteFile" // anything else fails the test
	})
	if err := writeConfig(fs); err != nil {
		t.Fatal(err)
	}
	testfake.AssertCalled(t, fs.Calls(), func(c testfake.Call) bool {
		return c.Method == "FS.WriteFile" && c.Args[0] == "/etc/app.yaml"
	})
}
