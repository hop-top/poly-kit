package consent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"hop.top/kit/go/core/id"
)

// auditLogPrefix is the TypeID prefix of audit log ids.
const auditLogPrefix = "clog"

// FileAuditor appends audit events as JSON lines to the per-tool audit
// log, perms 0600 in a 0700 directory. It is the durable source of
// truth for consent events: each record gets the log id and the next
// sequence number, and a Relay delivers them from there.
//
// One process writes a log at a time; concurrent writers to the same
// tool's log are not coordinated.
type FileAuditor struct {
	path string

	mu     sync.Mutex
	loaded bool
	log    string
	seq    uint64
}

// NewFileAuditor writes to the audit log of DefaultTool().
func NewFileAuditor() (*FileAuditor, error) { return NewFileAuditorFor(DefaultTool()) }

// NewFileAuditorFor writes to <XDG_STATE_HOME>/<tool>/consent/audit.jsonl.
func NewFileAuditorFor(tool string) (*FileAuditor, error) {
	if err := validateTool(tool); err != nil {
		return nil, err
	}
	p, err := stateFile(tool, auditFileName)
	if err != nil {
		return nil, err
	}
	return NewFileAuditorAt(p), nil
}

// NewFileAuditorAt writes to path.
func NewFileAuditorAt(path string) *FileAuditor { return &FileAuditor{path: path} }

// Path returns the audit log path.
func (a *FileAuditor) Path() string { return a.path }

// Record stamps e with the log id and the next sequence number and
// appends it, fsynced, before returning.
func (a *FileAuditor) Record(ctx context.Context, e AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadLocked(); err != nil {
		return err
	}
	e.Log, e.Seq = a.log, a.seq+1
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := ensureDir(filepath.Dir(a.path)); err != nil {
		return err
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("consent: audit open: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("consent: audit write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("consent: audit sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	a.seq = e.Seq
	return nil
}

// LogID returns the id of the log, minting one for a log not written
// yet.
func (a *FileAuditor) LogID() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadLocked(); err != nil {
		return "", err
	}
	return a.log, nil
}

// ReadAfter returns the records with Seq > after, in order.
func (a *FileAuditor) ReadAfter(after uint64) ([]AuditEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	all, err := a.readAll()
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for _, e := range all {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, nil
}

func (a *FileAuditor) loadLocked() error {
	if a.loaded {
		return nil
	}
	all, err := a.readAll()
	if err != nil {
		return err
	}
	if n := len(all); n > 0 {
		a.log, a.seq = all[n-1].Log, all[n-1].Seq
	} else {
		if a.log, err = id.New(auditLogPrefix); err != nil {
			return fmt.Errorf("consent: mint audit log id: %w", err)
		}
	}
	a.loaded = true
	return nil
}

// readAll parses the whole log. A record that does not parse, or that
// breaks the log id or the sequence, makes the log unreadable: the
// next use is refused rather than audited into a broken trail.
func (a *FileAuditor) readAll() ([]AuditEvent, error) {
	b, err := os.ReadFile(a.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("consent: audit read: %w", err)
	}
	var out []AuditEvent
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		var e AuditEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("consent: audit log %s line %d: %w", a.path, n, err)
		}
		if len(out) > 0 && (e.Log != out[0].Log || e.Seq != out[len(out)-1].Seq+1) {
			return nil, fmt.Errorf("consent: audit log %s line %d: log id or sequence out of order", a.path, n)
		}
		if len(out) == 0 && (e.Log == "" || e.Seq != 1) {
			return nil, fmt.Errorf("consent: audit log %s line %d: first record must be seq 1 with a log id", a.path, n)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("consent: audit read: %w", err)
	}
	return out, nil
}
