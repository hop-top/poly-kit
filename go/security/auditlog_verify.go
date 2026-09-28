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
	"sort"
	"strconv"
	"strings"
)

// AuditReport is the outcome of verifying one audit log.
type AuditReport struct {
	// Path is the active file's path, as given to VerifyAuditLog.
	Path string `json:"path"`
	// Files is how many files were read: rotated files plus the
	// active one.
	Files int `json:"files"`
	// Records is how many records verified before the first break,
	// or in total when there is none.
	Records uint64 `json:"records"`
	// FirstSeq is the first record's seq. Above 1 when retention
	// deleted the oldest rotated files.
	FirstSeq uint64 `json:"first_seq,omitempty"`
	// Head is the last verified record. Compare it with a head
	// recorded earlier, elsewhere, to detect a truncated tail.
	Head AuditEntry `json:"head"`
	// Break is the first place the chain does not hold; nil when it
	// holds throughout.
	Break *AuditBreak `json:"break,omitempty"`
	// TornTail reports a final line without its newline in the active
	// file: a write in progress while the log was read, or one the
	// writer died during. It is not counted and is not a break; the
	// next OpenAuditLog moves it aside.
	TornTail bool `json:"torn_tail,omitempty"`
}

// OK reports whether the chain holds throughout.
func (r AuditReport) OK() bool { return r.Break == nil }

// AuditBreak locates the first record at which a chain does not hold.
type AuditBreak struct {
	// File is the file holding the record.
	File string `json:"file"`
	// Line is the record's 1-based line in File.
	Line int `json:"line"`
	// Seq is the seq the record claims, when it could be read.
	Seq uint64 `json:"seq,omitempty"`
	// Reason says what does not hold.
	Reason string `json:"reason"`
}

// Error renders the break as one line.
func (b *AuditBreak) Error() string {
	return fmt.Sprintf("%s:%d: %s", b.File, b.Line, b.Reason)
}

// VerifyAuditLog walks the log at path — the rotated files oldest
// first, then the active file — and checks every record: its hash
// matches its bytes, its prev matches the previous record's hash, and
// its seq follows the previous one. It stops at the first break.
//
// The returned error is for a log that could not be read (none at
// path wraps os.ErrNotExist); a log that was read and does not hold is
// a nil error and a report whose Break is set.
//
// What verification cannot see: records removed from the tail, and
// whole rotated files removed from the head, leave a chain that holds.
// Record Head somewhere else to catch the first; the second is what
// retention does by design.
func VerifyAuditLog(path string) (AuditReport, error) {
	rep := AuditReport{Path: path}
	segs, err := listSegments(path)
	if err != nil {
		return rep, err
	}
	files := make([]segment, 0, len(segs)+1)
	files = append(files, segs...)
	if _, err := os.Stat(path); err == nil {
		files = append(files, segment{path: path})
	} else if !errors.Is(err, os.ErrNotExist) {
		return rep, err
	}
	if len(files) == 0 {
		return rep, fmt.Errorf("security: no audit log at %s: %w", path, os.ErrNotExist)
	}
	v := verifier{rep: &rep}
	for i, seg := range files {
		rep.Files++
		if err := v.file(seg, i == len(files)-1); err != nil {
			return rep, err
		}
		if rep.Break != nil {
			break
		}
	}
	return rep, nil
}

// verifier carries the chain state across files.
type verifier struct {
	rep  *AuditReport
	seq  uint64
	head string
}

// file verifies one file; last marks the active file, the only one
// allowed a torn final line.
func (v *verifier) file(seg segment, last bool) error {
	f, err := os.Open(seg.path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 && errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if line[len(line)-1] != '\n' {
			if last {
				v.rep.TornTail = true
				return nil
			}
			v.fail(seg.path, n, 0, "final line has no newline in a rotated file")
			return nil
		}
		first := n == 1
		if reason, seq := v.record(line[:len(line)-1], seg, first); reason != "" {
			v.fail(seg.path, n, seq, reason)
			return nil
		}
	}
}

// record checks one line against the chain and advances it. It returns
// a reason when the chain does not hold here, with the seq the line
// claims when it has one.
func (v *verifier) record(line []byte, seg segment, first bool) (string, uint64) {
	rec, err := parseRecordLine(line)
	if err != nil {
		return "malformed record: " + err.Error(), 0
	}
	if rec.V != AuditRecordVersion {
		return fmt.Sprintf("unsupported record version %d", rec.V), rec.Seq
	}
	if rec.computed != rec.Hash {
		return "hash does not match the record's bytes: the record was edited", rec.Seq
	}
	switch v.rep.Records {
	case 0:
		// The oldest record on disk anchors the chain. The first ever
		// record has seq 1 and no prev; a later one is legitimate only
		// at the start of a rotated file retention left behind, and
		// that file's name pins which seq it must start at.
		if seg.first != 0 && rec.Seq != seg.first {
			return fmt.Sprintf("file is named for seq %d but starts at seq %d", seg.first, rec.Seq), rec.Seq
		}
		if seg.first == 0 && rec.Seq != 1 {
			return fmt.Sprintf("chain starts at seq %d with no earlier file: records before it are missing", rec.Seq), rec.Seq
		}
		if rec.Seq == 1 && rec.Prev != "" {
			return "first record carries a prev hash", rec.Seq
		}
		v.rep.FirstSeq = rec.Seq
	default:
		if rec.Seq != v.seq+1 {
			return fmt.Sprintf("seq %d follows seq %d: records are missing or reordered", rec.Seq, v.seq), rec.Seq
		}
		if rec.Prev != v.head {
			return fmt.Sprintf("prev does not match the hash of seq %d: a record was replaced or inserted", v.seq), rec.Seq
		}
		if first && seg.first != 0 && rec.Seq != seg.first {
			return fmt.Sprintf("file is named for seq %d but starts at seq %d", seg.first, rec.Seq), rec.Seq
		}
	}
	// The chain advances on the hash computed from the record's bytes,
	// never on the hash field stored beside them: a record edited in
	// place with its stored hash rewritten to match still breaks the
	// next record's prev link.
	v.seq, v.head = rec.Seq, rec.computed
	v.rep.Records++
	v.rep.Head = AuditEntry{Seq: rec.Seq, Hash: rec.computed}
	return "", 0
}

func (v *verifier) fail(file string, line int, seq uint64, reason string) {
	v.rep.Break = &AuditBreak{File: file, Line: line, Seq: seq, Reason: reason}
}

// parsedRecord is one record line, decoded.
type parsedRecord struct {
	V    int    `json:"v"`
	Seq  uint64 `json:"seq"`
	Prev string `json:"prev"`
	// Hash is the hash the line states; computed is the hash of the
	// line's bytes, the only one the chain advances on.
	Hash     string `json:"-"`
	computed string
}

// hashField is what separates a record's hashed body from its hash.
var hashField = []byte(`,"hash":"`)

// parseRecordLine splits a record line into its body and stated hash,
// decodes the body's chain fields, and hashes the body.
func parseRecordLine(line []byte) (*parsedRecord, error) {
	i := bytes.LastIndex(line, hashField)
	if i < 0 || !bytes.HasSuffix(line, []byte(`"}`)) {
		return nil, errors.New("no trailing hash field")
	}
	stated := string(line[i+len(hashField) : len(line)-2])
	if len(stated) != 2*sha256.Size || !isLowerHex(stated) {
		return nil, errors.New("hash is not 64 lowercase hex digits")
	}
	body := make([]byte, 0, i+1)
	body = append(append(body, line[:i]...), '}')
	var rec parsedRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	rec.Hash, rec.computed = stated, hex.EncodeToString(sum[:])
	return &rec, nil
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// segment is one file of a log: a rotated file, which carries the seq
// of its first record in its name, or the active file (first == 0).
type segment struct {
	path  string
	first uint64
}

// segmentDigits pads rotated-file suffixes so names sort in chain
// order.
const segmentDigits = 20

// segmentPath names the rotated file whose first record is seq.
func segmentPath(path string, seq uint64) string {
	s := strconv.FormatUint(seq, 10)
	return path + "." + strings.Repeat("0", segmentDigits-len(s)) + s
}

// listSegments returns path's rotated files, oldest first.
func listSegments(path string) ([]segment, error) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []segment
	for _, e := range entries {
		name := e.Name()
		suffix, ok := strings.CutPrefix(name, base+".")
		if !ok || len(suffix) != segmentDigits || e.IsDir() {
			continue
		}
		seq, err := strconv.ParseUint(suffix, 10, 64)
		if err != nil || seq == 0 {
			continue
		}
		out = append(out, segment{path: filepath.Join(dir, name), first: seq})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].first < out[j].first })
	return out, nil
}
