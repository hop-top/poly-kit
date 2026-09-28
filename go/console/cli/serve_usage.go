package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/sqlite"
	"hop.top/kit/go/transport/cmdsurface"
)

// usageFile is the file, in the tool's XDG state directory, that holds
// the served usage counts — policy budgets and quotas — when no store
// was given with [WithUsageStore].
const usageFile = "usage.db"

// usageState is the Root's usage ledger: one per process, shared by
// every service, so a principal's budget is one budget whichever
// surface it calls on. Quotas keep a count per service in the same
// ledger.
type usageState struct {
	store  kv.Store
	once   sync.Once
	ledger *cmdsurface.UsageLedger
	err    error
}

// WithUsageStore keeps the served usage counts — the per-caller
// budgets a --policy's callers section sets, and the services' quota
// blocks' counts — in store instead of the
// default SQLite file $XDG_STATE_HOME/<tool>/usage.db. Pass a shared
// store (etcd, TiDB) when several instances serve the same callers
// and must count them together. The Root does not close it.
func WithUsageStore(store kv.Store) func(*Root) {
	return func(r *Root) { r.serveAuth.usage.store = store }
}

// usageLedgerOf returns the Root's usage ledger, opening the default
// store on first use. It is a function rather than a Root method so a
// tool that serves nothing does not link it (see ServeBridgeOptions).
func usageLedgerOf(r *Root) (*cmdsurface.UsageLedger, error) {
	u := &r.serveAuth.usage
	u.once.Do(func() {
		if u.store != nil {
			u.ledger = cmdsurface.NewUsageLedger(u.store)
			return
		}
		store, err := openUsageFile(r.Config.Name)
		if err != nil {
			u.err = err
			return
		}
		u.ledger = cmdsurface.NewUsageLedger(store)
	})
	return u.ledger, u.err
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
