package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The engine's pre-run hook opens kit's document store for the run of
// `kit serve` that will start the api service, and for nothing else.
// It used to match any command named serve, so `kit conformance svc
// serve` opened (and created) the store too.

func TestEnginePrepare_KitServeOpensStore(t *testing.T) {
	sandbox := isolateKitBinary(t)
	root, eng := newKitRoot("dev")
	defer eng.close()

	data := filepath.Join(sandbox, "engine")
	cmd, args, err := root.Cmd.Find([]string{"serve"})
	require.NoError(t, err)
	require.NoError(t, cmd.ParseFlags([]string{"--data", data}))

	require.NoError(t, eng.prepare(root, cmd, args))
	_, err = os.Stat(filepath.Join(data, "documents.db"))
	require.NoError(t, err, "kit serve must open the engine store")
}

func TestEnginePrepare_OtherServeLeavesLeftAlone(t *testing.T) {
	sandbox := isolateKitBinary(t)
	root, eng := newKitRoot("dev")
	defer eng.close()

	cmd, args, err := root.Cmd.Find([]string{"conformance", "svc", "serve"})
	require.NoError(t, err)
	require.NoError(t, eng.prepare(root, cmd, args))
	require.Nil(t, eng.ds, "conformance svc serve must not open the engine store")

	// End to end: the grading service's own validation answers, and no
	// engine store appears anywhere.
	var stderr bytes.Buffer
	root.Cmd.SetOut(&bytes.Buffer{})
	root.Cmd.SetErr(&stderr)
	root.Cmd.SetArgs([]string{"conformance", "svc", "serve", "--format", "json"})
	err = root.Execute(context.Background())
	require.Error(t, err)
	require.Contains(t, stderr.String(), "--scenarios-root")
	require.NoError(t, filepath.WalkDir(sandbox, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), "documents.db") {
			t.Errorf("engine store created at %s", p)
		}
		return nil
	}))
}
