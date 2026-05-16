package bus

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"hop.top/kit/go/storage/sqlstore"
)

const defaultPollInterval = 100 * time.Millisecond

const eventsMigration = `
CREATE TABLE IF NOT EXISTS events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  topic      TEXT    NOT NULL,
  source     TEXT    NOT NULL,
  timestamp  TEXT    NOT NULL,
  payload    TEXT    NOT NULL,
  created_at TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_id ON events(id);
`

// SQLiteOption configures the SQLiteAdapter.
type SQLiteOption func(*SQLiteAdapter)

// WithPollInterval sets the polling interval for subscriber delivery.
func WithPollInterval(d time.Duration) SQLiteOption {
	return func(a *SQLiteAdapter) { a.pollInterval = d }
}

// SQLiteAdapter persists events to SQLite and delivers them to
// subscribers via polling. Suitable for cross-process event sharing.
type SQLiteAdapter struct {
	store        *sqlstore.Store
	pollInterval time.Duration

	mu     sync.RWMutex
	subs   []sqliteSub
	nextID uint64
	closed atomic.Bool

	wg     sync.WaitGroup
	cancel context.CancelFunc
}

type sqliteSub struct {
	id      uint64
	pattern string
	sync    Handler
	async   AsyncHandler
	lastID  int64
}

// NewSQLiteAdapter opens (or creates) a SQLite-backed adapter at path.
// WAL mode is enabled automatically via sqlstore.
func NewSQLiteAdapter(path string, opts ...SQLiteOption) (*SQLiteAdapter, error) {
	store, err := sqlstore.Open(path, sqlstore.Options{
		MigrateSQL: eventsMigration,
	})
	if err != nil {
		return nil, err
	}

	// Enable WAL mode for concurrent readers.
	if _, err := store.DB().Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = store.Close()
		return nil, err
	}

	a := &SQLiteAdapter{
		store:        store,
		pollInterval: defaultPollInterval,
	}
	for _, o := range opts {
		o(a)
	}

	// Seed lastID to current max so subscribers only see new events.
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(1)
	go a.pollLoop(ctx)

	return a, nil
}

// Publish persists the event and returns. Delivery happens via polling.
func (a *SQLiteAdapter) Publish(ctx context.Context, e Event) error {
	if a.closed.Load() {
		return ErrBusClosed
	}

	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = a.store.DB().ExecContext(ctx,
		`INSERT INTO events (topic, source, timestamp, payload, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		string(e.Topic), e.Source,
		e.Timestamp.UTC().Format(time.RFC3339Nano),
		string(payload), now,
	)
	return err
}

func (a *SQLiteAdapter) Subscribe(pattern string, h Handler) Unsubscribe {
	a.mu.Lock()
	id := a.nextID
	a.nextID++
	lastID := a.currentMaxID()
	a.subs = append(a.subs, sqliteSub{
		id: id, pattern: pattern, sync: h, lastID: lastID,
	})
	a.mu.Unlock()
	return a.unsub(id)
}

func (a *SQLiteAdapter) SubscribeAsync(pattern string, h AsyncHandler) Unsubscribe {
	a.mu.Lock()
	id := a.nextID
	a.nextID++
	lastID := a.currentMaxID()
	a.subs = append(a.subs, sqliteSub{
		id: id, pattern: pattern, async: h, lastID: lastID,
	})
	a.mu.Unlock()
	return a.unsub(id)
}

func (a *SQLiteAdapter) unsub(id uint64) Unsubscribe {
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for i, s := range a.subs {
			if s.id == id {
				a.subs = append(a.subs[:i], a.subs[i+1:]...)
				return
			}
		}
	}
}

// currentMaxID returns the current max event ID. Caller must hold mu.
func (a *SQLiteAdapter) currentMaxID() int64 {
	var maxID sql.NullInt64
	_ = a.store.DB().QueryRow("SELECT MAX(id) FROM events").Scan(&maxID)
	if maxID.Valid {
		return maxID.Int64
	}
	return 0
}

func (a *SQLiteAdapter) pollLoop(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final drain before exit.
			a.deliverPending(context.Background())
			return
		case <-ticker.C:
			a.deliverPending(ctx)
		}
	}
}

func (a *SQLiteAdapter) deliverPending(ctx context.Context) {
	a.mu.RLock()
	if len(a.subs) == 0 {
		a.mu.RUnlock()
		return
	}

	// Find minimum lastID across all subscribers.
	minID := a.subs[0].lastID
	for _, s := range a.subs[1:] {
		if s.lastID < minID {
			minID = s.lastID
		}
	}
	a.mu.RUnlock()

	// Fetch events newer than minID.
	rows, err := a.store.DB().QueryContext(ctx,
		`SELECT id, topic, source, timestamp, payload FROM events WHERE id > ? ORDER BY id`,
		minID,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	type dbEvent struct {
		id        int64
		topic     string
		source    string
		timestamp string
		payload   string
	}

	var events []dbEvent
	for rows.Next() {
		var ev dbEvent
		if err := rows.Scan(&ev.id, &ev.topic, &ev.source, &ev.timestamp, &ev.payload); err != nil {
			continue
		}
		events = append(events, ev)
	}

	if len(events) == 0 {
		return
	}

	// Snapshot subscribers under lock, then release before invoking
	// handlers. This prevents deadlock when a handler calls
	// Subscribe/Unsubscribe.
	a.mu.RLock()
	snap := make([]sqliteSub, len(a.subs))
	copy(snap, a.subs)
	a.mu.RUnlock()

	type lastIDUpdate struct {
		subID  uint64
		lastID int64
	}
	var updates []lastIDUpdate

	for i := range snap {
		for _, ev := range events {
			if ev.id <= snap[i].lastID {
				continue
			}
			if !Topic(ev.topic).Match(snap[i].pattern) {
				if ev.id > snap[i].lastID {
					snap[i].lastID = ev.id
				}
				continue
			}

			ts, _ := time.Parse(time.RFC3339Nano, ev.timestamp)
			var payload any
			_ = json.Unmarshal([]byte(ev.payload), &payload)

			event := Event{
				Topic:     Topic(ev.topic),
				Source:    ev.source,
				Timestamp: ts,
				Payload:   payload,
			}

			if snap[i].sync != nil {
				_ = snap[i].sync(context.Background(), event)
			}
			if snap[i].async != nil {
				h := snap[i].async
				a.wg.Add(1)
				go func(fn AsyncHandler, ev Event) {
					defer a.wg.Done()
					fn(context.Background(), ev)
				}(h, event)
			}

			snap[i].lastID = ev.id
		}
		updates = append(updates, lastIDUpdate{subID: snap[i].id, lastID: snap[i].lastID})
	}

	// Re-acquire lock to update lastID values on live subscribers.
	a.mu.Lock()
	for _, u := range updates {
		for j := range a.subs {
			if a.subs[j].id == u.subID && u.lastID > a.subs[j].lastID {
				a.subs[j].lastID = u.lastID
				break
			}
		}
	}
	a.mu.Unlock()
}

// Close stops polling, waits for in-flight delivery, and closes the store.
func (a *SQLiteAdapter) Close(ctx context.Context) error {
	if a.closed.Swap(true) {
		return nil
	}

	a.cancel()

	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	a.mu.Lock()
	a.subs = nil
	a.mu.Unlock()

	return a.store.Close()
}
