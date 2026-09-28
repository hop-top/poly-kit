package cmdsurface

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hop.top/kit/go/storage/kv"
)

// ledgerPrefix is the kv key prefix every ledger entry is stored
// under.
const ledgerPrefix = "kit/usage/"

// UsageLedger counts usage — calls and bytes — per key over fixed
// windows, on a [kv.Store], so a count outlives the process that kept
// it and a restart resets nothing. The policy engine's per-caller
// budgets and the quota gate keep their counts in one.
//
// Windows are fixed and aligned to the Unix epoch: a one-hour window
// runs from the top of one hour to the next, a day from midnight UTC.
// A count belongs to the window it was made in and is never carried
// into the next.
//
// A ledger serializes its own operations, so check-and-count is
// atomic within one process. Processes sharing a store each count
// correctly but may overshoot a limit by the calls they admit at the
// same instant; kv offers no compare-and-set to prevent it.
type UsageLedger struct {
	store kv.Store
	now   func() time.Time
	mu    sync.Mutex
}

// Usage is what a key has used in one window.
type Usage struct {
	// Ops counts calls.
	Ops int64 `json:"ops"`
	// Bytes counts the bytes the calls produced.
	Bytes int64 `json:"bytes"`
	// Start is when the window began.
	Start time.Time `json:"-"`
	// Reset is when the window ends and the count starts over.
	Reset time.Time `json:"-"`
}

// NewUsageLedger returns a ledger over store. A store that also
// implements [kv.TTLStore] expires each window's entry once the
// window has passed; any other store has a key's past windows
// deleted when its next window is first written.
func NewUsageLedger(store kv.Store) *UsageLedger {
	return &UsageLedger{store: store, now: time.Now}
}

// Store returns the ledger's store.
func (l *UsageLedger) Store() kv.Store { return l.store }

// window returns the window of length w holding now.
func window(now time.Time, w time.Duration) (start, reset time.Time) {
	start = now.Truncate(w)
	return start, start.Add(w)
}

// entryKey is the storage key of key's window starting at start.
func entryKey(key string, w time.Duration, start time.Time) string {
	return entryPrefix(key) + strconv.FormatInt(int64(w/time.Second), 10) + "/" +
		strconv.FormatInt(start.Unix(), 10)
}

// entryPrefix is the storage prefix of every window of key. The key is
// query-escaped, so it holds no "/" and no key's prefix is another's.
func entryPrefix(key string) string {
	return ledgerPrefix + url.QueryEscape(key) + "/"
}

// Usage returns what key has used in the current window of length w.
func (l *UsageLedger) Usage(ctx context.Context, key string, w time.Duration) (Usage, error) {
	if err := checkWindow(w); err != nil {
		return Usage{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	u, _, err := l.load(ctx, key, w)
	return u, err
}

// Take counts one call against key in the current window of length w
// when the window has counted fewer than maxOps, and reports whether
// it did. The usage returned is the window's after the call.
func (l *UsageLedger) Take(ctx context.Context, key string, w time.Duration, maxOps int64) (Usage, bool, error) {
	if err := checkWindow(w); err != nil {
		return Usage{}, false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	u, fresh, err := l.load(ctx, key, w)
	if err != nil || u.Ops >= maxOps {
		return u, false, err
	}
	u.Ops++
	return u, true, l.save(ctx, key, w, u, fresh)
}

// Add counts ops calls and bytes bytes against key in the current
// window of length w, and returns the window's usage after them.
func (l *UsageLedger) Add(ctx context.Context, key string, w time.Duration, ops, bytes int64) (Usage, error) {
	if err := checkWindow(w); err != nil {
		return Usage{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	u, fresh, err := l.load(ctx, key, w)
	if err != nil {
		return u, err
	}
	u.Ops += ops
	u.Bytes += bytes
	return u, l.save(ctx, key, w, u, fresh)
}

// Reset forgets every window key has counted.
func (l *UsageLedger) Reset(ctx context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.deleteWindows(ctx, key, "")
}

// Keys returns every key starting with prefix that has a count
// stored, sorted.
func (l *UsageLedger) Keys(ctx context.Context, prefix string) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stored, err := l.store.List(ctx, ledgerPrefix+url.QueryEscape(prefix))
	if err != nil {
		return nil, fmt.Errorf("cmdsurface: usage ledger: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range stored {
		rest := strings.TrimPrefix(s, ledgerPrefix)
		esc, _, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		key, err := url.QueryUnescape(esc)
		if err != nil || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out, nil
}

// load reads key's current window. fresh reports that nothing was
// stored for it yet.
func (l *UsageLedger) load(ctx context.Context, key string, w time.Duration) (u Usage, fresh bool, err error) {
	start, reset := window(l.now(), w)
	raw, ok, err := l.store.Get(ctx, entryKey(key, w, start))
	u.Start, u.Reset = start, reset
	if err != nil {
		return u, false, fmt.Errorf("cmdsurface: usage ledger: %w", err)
	}
	if !ok {
		return u, true, nil
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		// A corrupt entry is not a reason to refuse a caller; it
		// counts from zero again.
		u.Ops, u.Bytes = 0, 0
	}
	return u, false, nil
}

// save writes u as key's current window.
func (l *UsageLedger) save(ctx context.Context, key string, w time.Duration, u Usage, fresh bool) error {
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	k := entryKey(key, w, u.Start)
	if ttl, ok := l.store.(kv.TTLStore); ok {
		// Kept a whole window past its end, so a clock a little
		// behind still finds it.
		err = ttl.PutWithTTL(ctx, k, raw, u.Reset.Sub(l.now())+w)
	} else {
		if fresh {
			if err := l.deleteWindows(ctx, key, k); err != nil {
				return err
			}
		}
		err = l.store.Put(ctx, k, raw)
	}
	if err != nil {
		return fmt.Errorf("cmdsurface: usage ledger: %w", err)
	}
	return nil
}

// deleteWindows deletes every stored window of key but keep.
func (l *UsageLedger) deleteWindows(ctx context.Context, key, keep string) error {
	stored, err := l.store.List(ctx, entryPrefix(key))
	if err != nil {
		return fmt.Errorf("cmdsurface: usage ledger: %w", err)
	}
	for _, s := range stored {
		if s == keep {
			continue
		}
		if err := l.store.Delete(ctx, s); err != nil {
			return fmt.Errorf("cmdsurface: usage ledger: %w", err)
		}
	}
	return nil
}

func checkWindow(w time.Duration) error {
	if w < time.Second {
		return fmt.Errorf("cmdsurface: usage ledger: window %s is under a second", w)
	}
	return nil
}
