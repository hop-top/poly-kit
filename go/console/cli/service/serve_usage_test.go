package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/memory"
)

func usageDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "tool", usageFile)
}

func TestUsageDefaultsToMemory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r := authRoot(t)
	ledger, err := usageLedgerOf(r)
	require.NoError(t, err)
	_, err = ledger.Add(context.Background(), "k", time.Hour, 1, 0)
	require.NoError(t, err)

	_, isMem := ledger.Store().(*memory.Store)
	assert.True(t, isMem, "the default store is memory")
	_, err = os.Stat(usageDB(t))
	assert.True(t, os.IsNotExist(err), "no usage file is created by default")
}

func TestWithUsageFilePersistsAcrossReopen(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()

	r1 := authRoot(t, WithUsageFile())
	l1, err := usageLedgerOf(r1)
	require.NoError(t, err)
	_, err = l1.Add(ctx, "k", time.Hour, 3, 0)
	require.NoError(t, err)
	require.NoError(t, l1.Store().Close())
	_, err = os.Stat(usageDB(t))
	require.NoError(t, err, "the usage file exists")

	r2 := authRoot(t, WithUsageFile())
	l2, err := usageLedgerOf(r2)
	require.NoError(t, err)
	u, err := l2.Usage(ctx, "k", time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 3, u.Ops, "the count survives a re-open")
	require.NoError(t, l2.Store().Close())
}

func TestUsageConfigKeySelectsFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r := authRoot(t)
	r.Viper.Set(serveKeyUsageStore, "file")
	l, err := usageLedgerOf(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Store().Close() })
	_, err = os.Stat(usageDB(t))
	assert.NoError(t, err)
}

func TestUsageConfigKeyRejectsUnknown(t *testing.T) {
	r := authRoot(t)
	r.Viper.Set(serveKeyUsageStore, "etcd")
	_, err := usageLedgerOf(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), serveKeyUsageStore)
}

func TestWithUsageStoreWins(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	own := memory.New()
	r := authRoot(t, WithUsageStore(own), WithUsageFile())
	r.Viper.Set(serveKeyUsageStore, "file")
	l, err := usageLedgerOf(r)
	require.NoError(t, err)
	assert.Same(t, own, l.Store())
	_, err = os.Stat(usageDB(t))
	assert.True(t, os.IsNotExist(err), "the given store is used, not the file")
}
