package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/storage/kv/sqlite"
	"hop.top/kit/go/transport/cmdsurface"
)

// usageFile is the file, in the tool's XDG state directory, that holds
// the served usage counts — policy budgets and quotas — when the file
// store is chosen with [WithUsageFile] or services.usage_store: file.
const usageFile = "usage.db"

// serveKeyUsageStore selects where the usage counts live when no
// store was given with [WithUsageStore].
const serveKeyUsageStore = "services.usage_store"

// The values of services.usage_store.
const (
	usageStoreMemory = "memory" // default: counts die with the process
	usageStoreFile   = "file"   // $XDG_STATE_HOME/<tool>/usage.db
)

// usageState is the Root's usage ledger: one per process, shared by
// every service, so a principal's budget is one budget whichever
// surface it calls on. Quotas keep a count per service in the same
// ledger.
type usageState struct {
	store  kv.Store
	file   bool // WithUsageFile
	once   sync.Once
	ledger *cmdsurface.UsageLedger
	err    error
}

// WithUsageStore keeps the served usage counts — the per-caller
// budgets a --policy's callers section sets, and the services' quota
// blocks' counts — in store instead of in memory. Pass a shared store
// (etcd, TiDB) when several instances serve the same callers and must
// count them together. The Root does not close it. It wins over
// [WithUsageFile] and services.usage_store.
func WithUsageStore(store kv.Store) func(*cli.Root) {
	return func(r *cli.Root) { stateOf(r).serveAuth.usage.store = store }
}

// WithUsageFile keeps the served usage counts in the SQLite file
// $XDG_STATE_HOME/<tool>/usage.db, so a restart resets nothing. The
// default is memory: counts start over with the process. It wins over
// services.usage_store; [WithUsageStore] wins over it.
func WithUsageFile() func(*cli.Root) {
	return func(r *cli.Root) { stateOf(r).serveAuth.usage.file = true }
}

// usageLedgerOf returns the Root's usage ledger, opening the store on
// first use: the one [WithUsageStore] gave, else the file when
// [WithUsageFile] or services.usage_store: file asks for it, else
// memory. It is a function rather than a Root method so a tool that
// serves nothing does not link it (see ServeBridgeOptions).
func usageLedgerOf(r *cli.Root) (*cmdsurface.UsageLedger, error) {
	u := &stateOf(r).serveAuth.usage
	u.once.Do(func() {
		store, err := u.open(r)
		if err != nil {
			u.err = err
			return
		}
		u.ledger = cmdsurface.NewUsageLedger(store)
	})
	return u.ledger, u.err
}

// open returns the store the ledger counts in.
func (u *usageState) open(r *cli.Root) (kv.Store, error) {
	if u.store != nil {
		return u.store, nil
	}
	if u.file {
		return openUsageFile(r.Config.Name)
	}
	switch v := strings.ToLower(strings.TrimSpace(r.Viper.GetString(serveKeyUsageStore))); v {
	case "", usageStoreMemory:
		return memory.New(), nil
	case usageStoreFile:
		return openUsageFile(r.Config.Name)
	default:
		return nil, fmt.Errorf("%s: unknown store %q; use %s or %s",
			serveKeyUsageStore, v, usageStoreMemory, usageStoreFile)
	}
}

// openUsageFile opens the default usage store for tool.
func openUsageFile(tool string) (kv.Store, error) {
	dir, err := xdg.StateDir(tool)
	if err != nil {
		return nil, fmt.Errorf("usage store: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("usage store: %w", err)
	}
	store, err := sqlite.NewContext(context.Background(), filepath.Join(dir, usageFile))
	if err != nil {
		return nil, fmt.Errorf("usage store: %w", err)
	}
	return store, nil
}
