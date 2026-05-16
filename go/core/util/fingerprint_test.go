package util

import "testing"

func TestShortDeterministic(t *testing.T) {
	a := Short([]byte("hello"), 0)
	b := Short([]byte("hello"), 0)
	if a != b {
		t.Errorf("not deterministic: %q != %q", a, b)
	}
}

func TestShortDifferentInputs(t *testing.T) {
	a := Short([]byte("hello"), 0)
	b := Short([]byte("world"), 0)
	if a == b {
		t.Errorf("different inputs produced same hash: %q", a)
	}
}

func TestShortDefaultLength(t *testing.T) {
	got := Short([]byte("test"), 0)
	if len(got) != 8 {
		t.Errorf("default length: got %d, want 8", len(got))
	}
}

func TestShortCustomLength(t *testing.T) {
	got := Short([]byte("test"), 16)
	if len(got) != 16 {
		t.Errorf("n=16: got %d chars", len(got))
	}
}

func TestShortEmptyInput(t *testing.T) {
	got := Short([]byte{}, 0)
	if len(got) != 8 {
		t.Errorf("empty input: got %d chars", len(got))
	}
	if got == "" {
		t.Error("empty input produced empty hash")
	}
}

func TestShortString(t *testing.T) {
	a := ShortString("hello", 8)
	b := Short([]byte("hello"), 8)
	if a != b {
		t.Errorf("ShortString != Short: %q != %q", a, b)
	}
}
