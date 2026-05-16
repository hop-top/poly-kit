package util

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type entry struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestJSONLRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	want := entry{Name: "alice", Age: 30}
	if err := w.Write(want); err != nil {
		t.Fatal(err)
	}

	r := NewReader(&buf)
	var got entry
	if err := r.Read(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestJSONLWriteRaw(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	raw := []byte(`{"x":1}`)
	if err := w.WriteRaw(raw); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != `{"x":1}` {
		t.Errorf("got %q", got)
	}
}

func TestJSONLEach(t *testing.T) {
	input := `{"name":"a","age":1}
{"name":"b","age":2}
{"name":"c","age":3}
`
	var names []string
	err := Each(strings.NewReader(input), func(e entry) error {
		names = append(names, e.Name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("got %d entries, want 3", len(names))
	}
	if names[0] != "a" || names[1] != "b" || names[2] != "c" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestJSONLEmptyReader(t *testing.T) {
	r := NewReader(strings.NewReader(""))
	var v entry
	if err := r.Read(&v); err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestJSONLInvalidJSON(t *testing.T) {
	r := NewReader(strings.NewReader("not json\n"))
	var v entry
	if err := r.Read(&v); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestJSONLMultipleObjects(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	entries := []entry{
		{Name: "x", Age: 1},
		{Name: "y", Age: 2},
		{Name: "z", Age: 3},
	}
	for _, e := range entries {
		if err := w.Write(e); err != nil {
			t.Fatal(err)
		}
	}

	r := NewReader(&buf)
	for i, want := range entries {
		var got entry
		if err := r.Read(&got); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
		if got != want {
			t.Errorf("entry %d: got %+v, want %+v", i, got, want)
		}
	}
}
