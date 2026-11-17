package sqldb

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type codeErr int

func (c codeErr) Error() string { return fmt.Sprintf("code %d", int(c)) }
func (c codeErr) Code() int     { return int(c) }

func TestIsBusy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain text locked", errors.New("database is locked (5)"), false},
		{"busy", codeErr(5), true},
		{"busy wrapped", fmt.Errorf("ping: %w", codeErr(5)), true},
		{"busy recovery ext", codeErr(5 | 1<<8), true},
		{"busy snapshot ext", codeErr(5 | 2<<8), true},
		{"locked", codeErr(6), true},
		{"locked sharedcache ext", codeErr(6 | 1<<8), true},
		{"cantopen", codeErr(14), false},
		{"constraint", codeErr(19), false},
	}
	for _, c := range cases {
		if got := isBusy(c.err); got != c.want {
			t.Errorf("%s: isBusy = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestOpenErrorIsDriverBusy pins the real driver error shape isBusy
// relies on: a held lock yields *sqlite.Error with code SQLITE_BUSY.
func TestOpenErrorIsDriverBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	conn, err := holder.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}

	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_, err = other.ExecContext(t.Context(), "CREATE TABLE x (a)")
	var se *sqlite.Error
	if !errors.As(err, &se) {
		t.Fatalf("want *sqlite.Error, got %T: %v", err, err)
	}
	if se.Code()&0xff != sqliteBusy {
		t.Errorf("code = %d, want primary %d", se.Code(), sqliteBusy)
	}
	if !isBusy(err) {
		t.Error("isBusy false for real driver busy error")
	}
}

func TestRetryOpenNonBusyNotRetried(t *testing.T) {
	calls := 0
	boom := codeErr(14)
	_, err := retryOpen(5*time.Second, func() (*sql.DB, error) {
		calls++
		return nil, boom
	})
	if calls != 1 {
		t.Errorf("attempts = %d, want 1", calls)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}

func TestRetryOpenBusyThenOK(t *testing.T) {
	calls := 0
	want := new(sql.DB)
	got, err := retryOpen(5*time.Second, func() (*sql.DB, error) {
		calls++
		if calls < 3 {
			return nil, codeErr(5)
		}
		return want, nil
	})
	if err != nil || got != want || calls != 3 {
		t.Errorf("got=%v err=%v calls=%d", got, err, calls)
	}
}

func TestRetryOpenGivesUpAtDeadline(t *testing.T) {
	calls := 0
	start := time.Now()
	_, err := retryOpen(150*time.Millisecond, func() (*sql.DB, error) {
		calls++
		return nil, codeErr(5)
	})
	elapsed := time.Since(start)
	if !isBusy(err) {
		t.Errorf("err = %v, want the busy error", err)
	}
	if calls < 2 {
		t.Errorf("attempts = %d, want retries", calls)
	}
	if elapsed < 150*time.Millisecond || elapsed > 600*time.Millisecond {
		t.Errorf("elapsed = %v, want ~150ms", elapsed)
	}
}
