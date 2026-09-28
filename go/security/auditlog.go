package security

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// AuditRecordVersion is the version stamped on every record the log
// writes, as the "v" field. A verifier refuses a version it does not
// know rather than guess at its hashing rule.
const AuditRecordVersion = 1

// Tamper detection has its own class in kit's exit-code taxonomy: a
// broken chain is evidence that something edited the log, which no
// retry and no change of arguments clears, and which an operator must
// tell apart from "the verifier could not run" (GENERIC, 1) and "there
// is no log here" (NOT_FOUND, 3). The slot is recorded in
// hop.top/kit/go/console/output/envelope's ExtensionBand.
const (
	// CodeTamperDetected is the class symbol a failed verification
	// carries in the error envelope.
	CodeTamperDetected = "TAMPER_DETECTED"
	// ExitTamperDetected is the exit code a failed verification
	// carries.
	ExitTamperDetected = 71
)

// SyncMode selects when the log asks the operating system to flush
// appended records to stable storage.
type SyncMode int

const (
	// SyncNever writes every record with one write(2) and leaves the
	// flush to the operating system. A record survives the process
	// crashing the moment Append returns; it can be lost if the
	// machine loses power before the kernel writes it back. The log
	// still syncs on rotation and on Close. This is the default: it
	// costs no more than the hashing over an unsynced file sink.
	SyncNever SyncMode = iota
	// SyncAlways syncs after every record. Append returns only once
	// the record is on stable storage, at the cost of one fsync per
	// record (milliseconds on most disks).
	SyncAlways
	// SyncPeriodic syncs at most once per SyncInterval, from a
	// background goroutine, when records were appended since the last
	// sync. It bounds what a power loss can take to one interval.
	SyncPeriodic
)

// defaultSyncInterval is SyncPeriodic's interval when none is set.
const defaultSyncInterval = time.Second

// AuditLogOptions tunes durability, rotation, and retention. The zero
// value is SyncNever with a single file that never rotates.
type AuditLogOptions struct {
	// Sync selects the flush policy. See SyncMode.
	Sync SyncMode
	// SyncInterval is SyncPeriodic's interval. Zero means one second.
	SyncInterval time.Duration
	// MaxBytes rotates the active file before an append would take
	// it past this size. The rotated file keeps the chain: the next
	// file's first record carries the hash of the rotated file's
	// last. Zero never rotates.
	MaxBytes int64
	// MaxSegments is how many rotated files are kept; the oldest are
	// deleted after each rotation. Zero keeps every rotated file.
	MaxSegments int
	// Now is the clock stamped on each record. Nil means time.Now.
	Now func() time.Time
}

// ErrAuditLogBusy is returned by OpenAuditLog when another process
// holds the log open. Two writers would fork the chain, so the second
// is refused rather than serialized.
var ErrAuditLogBusy = errors.New("security: audit log is open in another process")

// ErrNotAuditLog is returned by OpenAuditLog when the file at the path
// exists but its records are not audit-chain records: appending to an
// unrelated file would corrupt it and chain nothing.
var ErrNotAuditLog = errors.New("security: not an audit chain")

// ErrAuditLogClosed is returned by Append after Close.
var ErrAuditLogClosed = errors.New("security: audit log is closed")

// AuditEntry identifies one appended record.
type AuditEntry struct {
	// Seq is the record's position in the chain, from 1.
	Seq uint64 `json:"seq"`
	// Hash is the hex SHA-256 of the record, which the next record
	// carries as its prev.
	Hash string `json:"hash"`
}

// AuditLog is a tamper-evident, append-only log: JSON Lines on disk,
// one record per line, each carrying the SHA-256 of the one before it.
// Editing, reordering, or deleting a record breaks the chain at that
// record, and [VerifyAuditLog] reports the first break.
//
// A record is one line:
//
//	{"v":1,"seq":2,"at":"2026-09-28T10:00:00Z","prev":"<hex>","rec":{...},"hash":"<hex>"}
//
// hash is the hex SHA-256 of the line's bytes up to and excluding
// `,"hash":` with a closing `}` appended; prev is the previous
// record's hash, empty on the chain's first record. rec is the
// caller's JSON payload, stored verbatim.
//
// An AuditLog is safe for concurrent use; appends are serialized. One
// process at a time may hold a log open (an advisory lock on
// "<path>.lock").
type AuditLog struct {
	path string
	opts AuditLogOptions
	now  func() time.Time
	lock *flock.Flock

	mu          sync.Mutex
	f           *os.File
	size        int64
	seq         uint64
	head        string
	activeFirst uint64 // seq of the active file's first record
	dirty       bool
	closed      bool
	buf         []byte

	stopSync chan struct{}
	syncDone chan struct{}
}

// OpenAuditLog opens the log at path for appending, creating it (and
// its directory, mode 0700) when absent. It resumes the chain from the
// last record on disk, whether that is in the active file or, right
// after a rotation, in the newest rotated file.
//
// A final line without its newline — a write the process did not
// finish before it died — is moved out of the file into
// "<path>.torn-<unix-nanos>" before appending resumes, so the chain
// continues from the last complete record and the fragment is kept
// for inspection.
func OpenAuditLog(path string, opts AuditLogOptions) (*AuditLog, error) {
	if path == "" {
		return nil, errors.New("security: audit log path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("security: audit log dir: %w", err)
	}
	lk := flock.New(path + ".lock")
	ok, err := lk.TryLock()
	if err != nil {
		return nil, fmt.Errorf("security: audit log lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAuditLogBusy, path)
	}
	l := &AuditLog{path: path, opts: opts, now: opts.Now, lock: lk}
	if l.now == nil {
		l.now = time.Now
	}
	if err := l.resume(); err != nil {
		_ = lk.Unlock()
		return nil, err
	}
	if opts.Sync == SyncPeriodic {
		l.startPeriodicSync()
	}
	return l, nil
}

// Path returns the active file's path.
func (l *AuditLog) Path() string { return l.path }

// resume truncates a torn tail, opens the active file for appending,
// and recovers seq and head from the last record on disk.
func (l *AuditLog) resume() error {
	if err := quarantineTornTail(l.path); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("security: open audit log: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("security: stat audit log: %w", err)
	}
	l.f, l.size = f, st.Size()

	last, first, err := l.lastRecordOnDisk()
	if err != nil {
		_ = f.Close()
		return err
	}
	if last != nil {
		// Chain on from the hash of the bytes on disk, not the hash
		// field stored beside them.
		l.seq, l.head = last.Seq, last.computed
	}
	l.activeFirst = l.seq + 1
	if first != nil {
		l.activeFirst = first.Seq
	}
	return nil
}

// lastRecordOnDisk returns the chain's last record and the active
// file's first record (nil when the active file is empty).
func (l *AuditLog) lastRecordOnDisk() (last, first *parsedRecord, err error) {
	if l.size > 0 {
		if last, err = recordAt(l.path, readLastLine, "last"); err != nil {
			return nil, nil, err
		}
		if first, err = recordAt(l.path, readFirstLine, "first"); err != nil {
			return nil, nil, err
		}
		return last, first, nil
	}
	segs, err := listSegments(l.path)
	if err != nil || len(segs) == 0 {
		return nil, nil, err
	}
	last, err = recordAt(segs[len(segs)-1].path, readLastLine, "last")
	return last, nil, err
}

// recordAt reads one line of path with read and parses it as a record.
func recordAt(path string, read func(string) ([]byte, error), which string) (*parsedRecord, error) {
	line, err := read(path)
	if err != nil {
		return nil, fmt.Errorf("security: read audit log: %w", err)
	}
	rec, err := parseRecordLine(line)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s record: %v", ErrNotAuditLog, path, which, err)
	}
	return rec, nil
}

// Append adds one record carrying payload, which must be one JSON
// value; it is stored verbatim (compacted when it spans lines).
// Append returns once the record is written, and — under SyncAlways —
// synced.
func (l *AuditLog) Append(payload []byte) (AuditEntry, error) {
	if !json.Valid(payload) {
		return AuditEntry{}, errors.New("security: audit payload is not valid JSON")
	}
	if bytes.ContainsAny(payload, "\r\n") {
		var c bytes.Buffer
		if err := json.Compact(&c, payload); err != nil {
			return AuditEntry{}, err
		}
		payload = c.Bytes()
	}
	at := l.now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return AuditEntry{}, ErrAuditLogClosed
	}
	seq := l.seq + 1
	line, hash := appendRecord(l.buf[:0], seq, at, l.head, payload)
	l.buf = line
	if l.opts.MaxBytes > 0 && l.size > 0 && l.size+int64(len(line)) > l.opts.MaxBytes {
		if err := l.rotate(); err != nil {
			return AuditEntry{}, err
		}
	}
	n, err := l.f.Write(line)
	l.size += int64(n)
	if err != nil {
		return AuditEntry{}, fmt.Errorf("security: audit append: %w", err)
	}
	l.seq, l.head = seq, hash
	switch l.opts.Sync {
	case SyncAlways:
		if err := l.f.Sync(); err != nil {
			return AuditEntry{Seq: seq, Hash: hash}, fmt.Errorf("security: audit sync: %w", err)
		}
	case SyncPeriodic:
		l.dirty = true
	}
	return AuditEntry{Seq: seq, Hash: hash}, nil
}

// Head returns the last record's position and hash; zero before the
// first record. Recording it somewhere the log's writer cannot reach
// is what makes truncation of the log's tail detectable.
func (l *AuditLog) Head() AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return AuditEntry{Seq: l.seq, Hash: l.head}
}

// Close syncs and closes the log and releases its lock. Further
// Appends fail with ErrAuditLogClosed.
func (l *AuditLog) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	stop := l.stopSync
	l.mu.Unlock()
	if stop != nil {
		close(stop)
		<-l.syncDone
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	errSync := l.f.Sync()
	errClose := l.f.Close()
	errUnlock := l.lock.Unlock()
	return errors.Join(errSync, errClose, errUnlock)
}

// rotate moves the active file aside as "<path>.<first seq>" and
// starts a new one, then applies retention. Called with mu held.
func (l *AuditLog) rotate() error {
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("security: audit rotate sync: %w", err)
	}
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("security: audit rotate close: %w", err)
	}
	if err := os.Rename(l.path, segmentPath(l.path, l.activeFirst)); err != nil {
		return fmt.Errorf("security: audit rotate: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("security: audit rotate open: %w", err)
	}
	l.f, l.size, l.activeFirst = f, 0, l.seq+1
	syncDir(filepath.Dir(l.path))
	return l.prune()
}

// prune deletes the oldest rotated files beyond MaxSegments.
func (l *AuditLog) prune() error {
	if l.opts.MaxSegments <= 0 {
		return nil
	}
	segs, err := listSegments(l.path)
	if err != nil {
		return err
	}
	var errs []error
	for len(segs) > l.opts.MaxSegments {
		if err := os.Remove(segs[0].path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		segs = segs[1:]
	}
	return errors.Join(errs...)
}

func (l *AuditLog) startPeriodicSync() {
	iv := l.opts.SyncInterval
	if iv <= 0 {
		iv = defaultSyncInterval
	}
	l.stopSync = make(chan struct{})
	l.syncDone = make(chan struct{})
	go func() {
		defer close(l.syncDone)
		t := time.NewTicker(iv)
		defer t.Stop()
		for {
			select {
			case <-l.stopSync:
				return
			case <-t.C:
				l.mu.Lock()
				if l.dirty && !l.closed {
					_ = l.f.Sync()
					l.dirty = false
				}
				l.mu.Unlock()
			}
		}
	}()
}

// appendRecord appends one record line to dst and returns it with the
// record's hash. The body is built by hand, not by encoding/json: the
// hash covers these exact bytes, and a verifier recomputes it from the
// bytes on disk without re-encoding anything.
func appendRecord(dst []byte, seq uint64, at time.Time, prev string, payload []byte) ([]byte, string) {
	dst = append(dst, `{"v":`...)
	dst = strconv.AppendInt(dst, AuditRecordVersion, 10)
	dst = append(dst, `,"seq":`...)
	dst = strconv.AppendUint(dst, seq, 10)
	dst = append(dst, `,"at":"`...)
	dst = at.AppendFormat(dst, time.RFC3339Nano)
	dst = append(dst, `","prev":"`...)
	dst = append(dst, prev...)
	dst = append(dst, `","rec":`...)
	dst = append(dst, payload...)
	h := sha256.New()
	h.Write(dst)
	h.Write([]byte{'}'})
	var sum [sha256.Size]byte
	hash := hex.EncodeToString(h.Sum(sum[:0]))
	dst = append(dst, `,"hash":"`...)
	dst = append(dst, hash...)
	dst = append(dst, "\"}\n"...)
	return dst, hash
}

// quarantineTornTail moves a final line that lacks its newline out of
// path, into "<path>.torn-<unix-nanos>".
func quarantineTornTail(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("security: open audit log: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return err
	}
	end := make([]byte, 1)
	if _, err := f.ReadAt(end, st.Size()-1); err != nil {
		return err
	}
	if end[0] == '\n' {
		return nil
	}
	cut, err := lastNewline(f, st.Size())
	if err != nil {
		return err
	}
	frag := make([]byte, st.Size()-cut)
	if _, err := f.ReadAt(frag, cut); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	torn := path + ".torn-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.WriteFile(torn, frag, 0o600); err != nil {
		return fmt.Errorf("security: keep torn record: %w", err)
	}
	if err := f.Truncate(cut); err != nil {
		return fmt.Errorf("security: drop torn record: %w", err)
	}
	return f.Sync()
}

// lastNewline returns the offset just past the last '\n' before size,
// or 0 when there is none.
func lastNewline(f *os.File, size int64) (int64, error) {
	const chunk = 64 << 10
	buf := make([]byte, chunk)
	end := size
	for end > 0 {
		start := max(end-chunk, 0)
		n := int(end - start)
		if _, err := f.ReadAt(buf[:n], start); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, nil
}

// readLastLine returns the last line of path, which ends with '\n',
// without its newline.
func readLastLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	start, err := lastNewline(f, st.Size()-1)
	if err != nil {
		return nil, err
	}
	line := make([]byte, st.Size()-1-start)
	if _, err := f.ReadAt(line, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return line, nil
}

// readFirstLine returns the first line of path, without its newline.
func readFirstLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return bytes.TrimSuffix(line, []byte{'\n'}), nil
}

// syncDir flushes a directory entry change (a rename) where the
// platform supports it; elsewhere it is a no-op.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
