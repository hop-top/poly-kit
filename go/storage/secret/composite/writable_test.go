package composite_test

import (
	"testing"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/composite"
	"hop.top/kit/go/storage/secret/file"
	"hop.top/kit/go/storage/secret/memory"
)

type declaredStore struct {
	secret.MutableStore
	w bool
}

func (d declaredStore) Writable() bool { return d.w }

func TestWritable(t *testing.T) {
	yes := declaredStore{memory.New(), true}
	no := declaredStore{memory.New(), false}
	cases := []struct {
		name string
		s    *composite.Store
		want bool
	}{
		{"all writable", composite.New(
			composite.Member{Name: "a", Store: yes, Owns: composite.HasPrefix("a/")},
			composite.Member{Name: "b", Store: file.New(t.TempDir(), nil)}), true},
		{"mixed", composite.New(
			composite.Member{Name: "a", Store: yes, Owns: composite.HasPrefix("a/")},
			composite.Member{Name: "m", Store: memory.New()}), false},
		{"declared false", composite.New(composite.Member{Name: "n", Store: no}), false},
		{"ro member ignored", composite.New(
			composite.Member{Name: "a", Store: yes},
			composite.Member{Name: "r", Store: memory.New(), RO: true}), true},
		{"only ro", composite.New(composite.Member{Name: "r", Store: yes, RO: true}), false},
	}
	for _, c := range cases {
		if got := c.s.Writable(); got != c.want {
			t.Errorf("%s: Writable = %v, want %v", c.name, got, c.want)
		}
	}
}
