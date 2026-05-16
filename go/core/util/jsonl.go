package util

import (
	"bufio"
	"encoding/json"
	"io"
)

// Writer writes JSON objects as newline-delimited JSON.
type Writer struct {
	w io.Writer
}

// NewWriter returns a Writer that writes JSONL to w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Write marshals v as JSON and writes it followed by a newline.
func (w *Writer) Write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w.WriteRaw(data)
}

// WriteRaw writes raw bytes followed by a newline.
func (w *Writer) WriteRaw(data []byte) error {
	if _, err := w.w.Write(data); err != nil {
		return err
	}
	_, err := w.w.Write([]byte{'\n'})
	return err
}

// Reader reads JSON objects from newline-delimited JSON.
type Reader struct {
	scanner *bufio.Scanner
}

// NewReader returns a Reader that reads JSONL from r.
func NewReader(r io.Reader) *Reader {
	return &Reader{scanner: bufio.NewScanner(r)}
}

// Read scans the next line and unmarshals it into v.
func (r *Reader) Read(v any) error {
	raw, err := r.ReadRaw()
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// ReadRaw returns the next line as raw bytes.
func (r *Reader) ReadRaw() ([]byte, error) {
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return r.scanner.Bytes(), nil
}

// Each iterates over all lines in r, unmarshaling each into T and
// calling fn. Stops on EOF or first error from fn.
func Each[T any](r io.Reader, fn func(T) error) error {
	rd := NewReader(r)
	for {
		var v T
		if err := rd.Read(&v); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := fn(v); err != nil {
			return err
		}
	}
}
