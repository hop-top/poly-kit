package eval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Load reads a case set: JSONL, one Entry object per line
// ({"case": ..., "reference": {"verdict": ..., "labels": ...}}). Blank
// lines are skipped; unknown fields and bad lines fail, naming the line.
func Load(r io.Reader) ([]Entry, error) {
	br := bufio.NewReader(r)
	var out []Entry
	for line := 1; ; line++ {
		raw, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if t := bytes.TrimSpace(raw); len(t) > 0 {
			var e Entry
			dec := json.NewDecoder(bytes.NewReader(t))
			dec.DisallowUnknownFields()
			if derr := dec.Decode(&e); derr != nil {
				return nil, fmt.Errorf("eval: line %d: %w", line, derr)
			}
			out = append(out, e)
		}
		if err != nil {
			return out, nil
		}
	}
}

// Save writes es as JSONL, one Entry per line.
func Save(w io.Writer, es []Entry) error {
	enc := json.NewEncoder(w)
	for i, e := range es {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("eval: entry %d: %w", i, err)
		}
	}
	return nil
}

// LoadFile is Load on the file at path.
func LoadFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Load(f)
}

// SaveFile is Save to path, replacing it.
func SaveFile(path string, es []Entry) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	return Save(f, es)
}
