package util

import (
	"errors"
	"strings"
	"testing"
)

func TestDo_NilError(t *testing.T) {
	got := Do(42, nil)
	if got != 42 {
		t.Fatalf("Do(42, nil) = %d, want 42", got)
	}
}

func TestDo_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Do did not panic")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "boom") {
			t.Fatalf("panic message = %v, want contains 'boom'", r)
		}
	}()
	Do(0, errors.New("boom"))
}

func TestOK_NilError(t *testing.T) {
	OK(nil) // should not panic
}

func TestOK_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("OK did not panic")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "fail") {
			t.Fatalf("panic message = %v, want contains 'fail'", r)
		}
	}()
	OK(errors.New("fail"))
}

func TestDo_PanicMessageIncludesError(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg := r.(string)
		if !strings.HasPrefix(msg, "must: ") {
			t.Fatalf("panic = %q, want prefix 'must: '", msg)
		}
	}()
	Do("", errors.New("specific error text"))
}
