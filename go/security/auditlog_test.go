package security_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/security"
)

func openLog(t *testing.T, path string, opts security.AuditLogOptions) *security.AuditLog {
	t.Helper()
	l, err := security.OpenAuditLog(path, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func appendN(t *testing.T, l *security.AuditLog, from, n int) {
	t.Helper()
	for i := from; i < from+n; i++ {
		_, err := l.Append([]byte(fmt.Sprintf(`{"n":%d}`, i)))
		require.NoError(t, err)
	}
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.SplitAfter(strings.TrimSuffix(string(b), "\n"), "\n")
}

func writeLines(t *testing.T, path string, ls []string) {
	t.Helper()
	out := strings.Join(ls, "")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(out), 0o600))
}

func verify(t *testing.T, path string) security.AuditReport {
	t.Helper()
	rep, err := security.VerifyAuditLog(path)
	require.NoError(t, err)
	return rep
}

func TestAuditLogAppendThenVerifyHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.chain")
	l := openLog(t, path, security.AuditLogOptions{})
	appendN(t, l, 1, 5)
	head := l.Head()
	require.NoError(t, l.Close())

	rep := verify(t, path)
	assert.True(t, rep.OK(), "%+v", rep.Break)
	assert.Equal(t, uint64(5), rep.Records)
	assert.Equal(t, uint64(1), rep.FirstSeq)
	assert.Equal(t, head, rep.Head)

	// The payload is stored verbatim; the record is one JSON line.
	first := lines(t, path)[0]
	assert.Contains(t, first, `"seq":1,`)
	assert.Contains(t, first, `"prev":"",`)
	assert.Contains(t, first, `"rec":{"n":1}`)
}

// Every way of altering a record breaks the chain at that record.
func TestAuditLogVerifyReportsFirstBreak(t *testing.T) {
	cases := map[string]struct {
		alter      func([]string) []string
		wantLine   int
		wantReason string
	}{
		"edited payload": {
			alter: func(ls []string) []string {
				ls[2] = strings.Replace(ls[2], `{"n":3}`, `{"n":9}`, 1)
				return ls
			},
			wantLine: 3, wantReason: "edited",
		},
		"edited timestamp": {
			alter: func(ls []string) []string {
				ls[1] = strings.Replace(ls[1], `"at":"2026`, `"at":"2025`, 1)
				return ls
			},
			wantLine: 2, wantReason: "edited",
		},
		"record deleted": {
			alter:    func(ls []string) []string { return append(ls[:1], ls[2:]...) },
			wantLine: 2, wantReason: "missing or reordered",
		},
		"records swapped": {
			alter: func(ls []string) []string {
				ls[1], ls[2] = ls[2], ls[1]
				return ls
			},
			wantLine: 2, wantReason: "missing or reordered",
		},
		"head deleted": {
			alter:    func(ls []string) []string { return ls[1:] },
			wantLine: 1, wantReason: "records before it are missing",
		},
		"line mangled": {
			alter: func(ls []string) []string {
				ls[3] = "not a record\n"
				return ls
			},
			wantLine: 4, wantReason: "malformed",
		},
		"record rehashed after edit": {
			// An in-place edit that also rewrites the stored hash field
			// passes the record's own check; the next record's prev,
			// linked to the hash computed from the original bytes,
			// catches it.
			alter: func(ls []string) []string {
				ls[1] = rehash(t, strings.Replace(ls[1], `{"n":2}`, `{"n":7}`, 1))
				return ls
			},
			wantLine: 3, wantReason: "replaced or inserted",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.chain")
			l := openLog(t, path, security.AuditLogOptions{
				Now: func() time.Time { return time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC) },
			})
			appendN(t, l, 1, 5)
			require.NoError(t, l.Close())

			writeLines(t, path, tc.alter(lines(t, path)))
			rep := verify(t, path)
			require.False(t, rep.OK(), "tampering went undetected")
			assert.Equal(t, path, rep.Break.File)
			assert.Equal(t, tc.wantLine, rep.Break.Line, rep.Break.Reason)
			assert.Contains(t, rep.Break.Reason, tc.wantReason)
		})
	}
}

// rehash recomputes a record line's own hash the way an attacker who
// knows the format would.
func rehash(t *testing.T, line string) string {
	t.Helper()
	i := strings.LastIndex(line, `,"hash":"`)
	require.Positive(t, i)
	body := line[:i] + "}"
	return line[:i] + `,"hash":"` + sha256Hex(body) + `"}` + "\n"
}

// A tail truncation leaves a chain that holds; the head recorded
// before it is what exposes it.
func TestAuditLogTailTruncationShowsInHead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.chain")
	l := openLog(t, path, security.AuditLogOptions{})
	appendN(t, l, 1, 4)
	head := l.Head()
	require.NoError(t, l.Close())

	writeLines(t, path, lines(t, path)[:3])
	rep := verify(t, path)
	assert.True(t, rep.OK())
	assert.NotEqual(t, head, rep.Head)
	assert.Equal(t, uint64(3), rep.Head.Seq)
}

func TestAuditLogResumesChainAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.chain")
	l := openLog(t, path, security.AuditLogOptions{})
	appendN(t, l, 1, 3)
	require.NoError(t, l.Close())

	l = openLog(t, path, security.AuditLogOptions{})
	assert.Equal(t, uint64(3), l.Head().Seq)
	e, err := l.Append([]byte(`{"n":4}`))
	require.NoError(t, err)
	assert.Equal(t, uint64(4), e.Seq)
	require.NoError(t, l.Close())

	rep := verify(t, path)
	assert.True(t, rep.OK(), "%+v", rep.Break)
	assert.Equal(t, uint64(4), rep.Records)
}

// A write the process died during leaves a line without its newline.
// Verification reports it without calling it tampering; reopening
// moves it aside and the chain continues from the last whole record.
func TestAuditLogTornTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.chain")
	l := openLog(t, path, security.AuditLogOptions{})
	appendN(t, l, 1, 2)
	require.NoError(t, l.Close())

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"v":1,"seq":3,"at":"20`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	rep := verify(t, path)
	assert.True(t, rep.OK())
	assert.True(t, rep.TornTail)
	assert.Equal(t, uint64(2), rep.Records)

	l = openLog(t, path, security.AuditLogOptions{})
	appendN(t, l, 3, 1)
	require.NoError(t, l.Close())
	rep = verify(t, path)
	assert.True(t, rep.OK(), "%+v", rep.Break)
	assert.False(t, rep.TornTail)
	assert.Equal(t, uint64(3), rep.Records)

	torn, err := filepath.Glob(path + ".torn-*")
	require.NoError(t, err)
	require.Len(t, torn, 1)
	b, err := os.ReadFile(torn[0])
	require.NoError(t, err)
	assert.Equal(t, `{"v":1,"seq":3,"at":"20`, string(b))
}

func TestAuditLogRotationKeepsTheChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.chain")
	l := openLog(t, path, security.AuditLogOptions{MaxBytes: 700})
	appendN(t, l, 1, 12)
	require.NoError(t, l.Close())

	segs, err := filepath.Glob(path + ".0*")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(segs), 2, "700-byte files hold three records each")

	rep := verify(t, path)
	assert.True(t, rep.OK(), "%+v", rep.Break)
	assert.Equal(t, uint64(12), rep.Records)
	assert.Equal(t, uint64(1), rep.FirstSeq)
	assert.Equal(t, len(segs)+1, rep.Files)

	// Reopening right after a rotation resumes from the rotated file.
	l = openLog(t, path, security.AuditLogOptions{MaxBytes: 700})
	assert.Equal(t, uint64(12), l.Head().Seq)
	require.NoError(t, l.Close())

	// Removing a rotated file from the middle is a break.
	require.NoError(t, os.Remove(segs[1]))
	rep = verify(t, path)
	require.False(t, rep.OK())
	assert.Contains(t, rep.Break.Reason, "missing or reordered")
}

func TestAuditLogRetentionPrunesOldestSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.chain")
	l := openLog(t, path, security.AuditLogOptions{MaxBytes: 700, MaxSegments: 2})
	appendN(t, l, 1, 20)
	require.NoError(t, l.Close())

	segs, err := filepath.Glob(path + ".0*")
	require.NoError(t, err)
	assert.Len(t, segs, 2)

	rep := verify(t, path)
	assert.True(t, rep.OK(), "%+v", rep.Break)
	assert.Greater(t, rep.FirstSeq, uint64(1), "retention dropped the oldest records")
	assert.Equal(t, uint64(20), rep.Head.Seq)

	// The oldest retained file's name pins its first seq: deleting
	// its first record is caught even though the chain is anchored.
	ls := lines(t, segs[0])
	writeLines(t, segs[0], ls[1:])
	rep = verify(t, path)
	require.False(t, rep.OK())
	assert.Contains(t, rep.Break.Reason, "named for seq")
}

func TestAuditLogSecondWriterRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.chain")
	openLog(t, path, security.AuditLogOptions{})
	_, err := security.OpenAuditLog(path, security.AuditLogOptions{})
	assert.ErrorIs(t, err, security.ErrAuditLogBusy)
}

func TestAuditLogRefusesUnrelatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o600))
	_, err := security.OpenAuditLog(path, security.AuditLogOptions{})
	assert.ErrorIs(t, err, security.ErrNotAuditLog)
	b, _ := os.ReadFile(path)
	assert.Equal(t, "hello\n", string(b), "the file is left untouched")
}

func TestAuditLogRejectsInvalidPayload(t *testing.T) {
	l := openLog(t, filepath.Join(t.TempDir(), "audit.chain"), security.AuditLogOptions{})
	_, err := l.Append([]byte(`{"n":`))
	assert.Error(t, err)
	e, err := l.Append([]byte("{\n  \"n\": 1\n}"))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), e.Seq, "a multi-line payload is compacted onto one line")
}

func TestAuditLogAppendAfterCloseFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.chain")
	l, err := security.OpenAuditLog(path, security.AuditLogOptions{})
	require.NoError(t, err)
	require.NoError(t, l.Close())
	_, err = l.Append([]byte(`{}`))
	assert.ErrorIs(t, err, security.ErrAuditLogClosed)
	// Closing released the lock.
	l2, err := security.OpenAuditLog(path, security.AuditLogOptions{})
	require.NoError(t, err)
	require.NoError(t, l2.Close())
}

func TestAuditLogVerifyMissing(t *testing.T) {
	_, err := security.VerifyAuditLog(filepath.Join(t.TempDir(), "absent.chain"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// Concurrent appenders — several services sharing one log in one
// process — produce one unbroken chain.
func TestAuditLogConcurrentAppends(t *testing.T) {
	for name, opts := range map[string]security.AuditLogOptions{
		"no sync":  {},
		"periodic": {Sync: security.SyncPeriodic, SyncInterval: time.Millisecond},
		"always":   {Sync: security.SyncAlways},
		"rotating": {MaxBytes: 2048, MaxSegments: 0},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.chain")
			l := openLog(t, path, opts)
			const writers, each = 8, 50
			var wg sync.WaitGroup
			for w := range writers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := range each {
						_, err := l.Append([]byte(fmt.Sprintf(`{"w":%d,"i":%d}`, w, i)))
						assert.NoError(t, err)
					}
					_ = l.Head()
				}()
			}
			wg.Wait()
			require.NoError(t, l.Close())
			rep := verify(t, path)
			assert.True(t, rep.OK(), "%+v", rep.Break)
			assert.Equal(t, uint64(writers*each), rep.Records)
		})
	}
}

// The package's exit-code slot must agree with kit's record of the
// extension band, or two features can claim one number.
func TestTamperDetectedSlotMatchesKitBand(t *testing.T) {
	found := false
	for _, slot := range output.ExtensionBand() {
		if slot.Class != security.CodeTamperDetected {
			continue
		}
		found = true
		assert.Equal(t, security.ExitTamperDetected, slot.Exit)
		assert.Equal(t, "hop.top/kit/go/security", slot.Owner)
	}
	assert.True(t, found, "TAMPER_DETECTED is missing from kit's extension band")
}

func BenchmarkAuditLogAppend(b *testing.B) {
	path := filepath.Join(b.TempDir(), "audit.chain")
	l, err := security.OpenAuditLog(path, security.AuditLogOptions{})
	require.NoError(b, err)
	defer l.Close()
	payload := bytes.Repeat([]byte("x"), 200)
	payload = append(append([]byte(`{"p":"`), payload...), `"}`...)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := l.Append(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
