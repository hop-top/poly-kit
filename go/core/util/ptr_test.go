package util

import "testing"

func TestTo(t *testing.T) {
	p := To(42)
	if *p != 42 {
		t.Fatalf("To(42) = %d, want 42", *p)
	}

	s := To("hello")
	if *s != "hello" {
		t.Fatalf("To(hello) = %q, want hello", *s)
	}

	b := To(true)
	if *b != true {
		t.Fatal("To(true) = false")
	}
}

func TestOr_NonNil(t *testing.T) {
	v := 7
	got := Or(&v, 99)
	if got != 7 {
		t.Fatalf("Or(&7, 99) = %d, want 7", got)
	}
}

func TestOr_Nil(t *testing.T) {
	got := Or[int](nil, 99)
	if got != 99 {
		t.Fatalf("Or(nil, 99) = %d, want 99", got)
	}
}

func TestZero(t *testing.T) {
	ip := Zero[int]()
	if *ip != 0 {
		t.Fatalf("Zero[int]() = %d, want 0", *ip)
	}

	sp := Zero[string]()
	if *sp != "" {
		t.Fatalf("Zero[string]() = %q, want empty", *sp)
	}

	bp := Zero[bool]()
	if *bp != false {
		t.Fatal("Zero[bool]() = true, want false")
	}
}

type testStruct struct {
	Name string
	Age  int
}

func TestTo_Struct(t *testing.T) {
	s := To(testStruct{Name: "x", Age: 1})
	if s.Name != "x" || s.Age != 1 {
		t.Fatalf("To(struct) = %+v", *s)
	}
}

func TestZero_Struct(t *testing.T) {
	s := Zero[testStruct]()
	if s.Name != "" || s.Age != 0 {
		t.Fatalf("Zero[struct] = %+v", *s)
	}
}
