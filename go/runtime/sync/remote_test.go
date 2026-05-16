package sync

import (
	gosync "sync"
	"testing"
)

func TestRemoteSet_AddGetRemove(t *testing.T) {
	rs := NewRemoteSet()

	r := Remote{Name: "origin", Mode: Bidirectional}
	if err := rs.Add(r); err != nil {
		t.Fatal(err)
	}
	if rs.Len() != 1 {
		t.Fatalf("expected len 1, got %d", rs.Len())
	}

	got, ok := rs.Get("origin")
	if !ok {
		t.Fatal("expected to find remote")
	}
	if got.Name != "origin" {
		t.Fatalf("expected name origin, got %s", got.Name)
	}

	if err := rs.Remove("origin"); err != nil {
		t.Fatal(err)
	}
	if rs.Len() != 0 {
		t.Fatalf("expected len 0, got %d", rs.Len())
	}
}

func TestRemoteSet_DuplicateNameError(t *testing.T) {
	rs := NewRemoteSet()
	r := Remote{Name: "dup"}
	if err := rs.Add(r); err != nil {
		t.Fatal(err)
	}
	if err := rs.Add(r); err == nil {
		t.Fatal("expected error on duplicate name")
	}
}

func TestRemoteSet_RemoveNotFound(t *testing.T) {
	rs := NewRemoteSet()
	if err := rs.Remove("nope"); err == nil {
		t.Fatal("expected error removing nonexistent remote")
	}
}

func TestRemoteSet_List(t *testing.T) {
	rs := NewRemoteSet()
	_ = rs.Add(Remote{Name: "a", Mode: PushOnly})
	_ = rs.Add(Remote{Name: "b", Mode: PullOnly})

	list := rs.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 remotes, got %d", len(list))
	}
}

func TestRemoteSet_ConcurrentAccess(t *testing.T) {
	rs := NewRemoteSet()
	var wg gosync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := string(rune('A' + n))
			_ = rs.Add(Remote{Name: name})
			rs.Get(name)
			rs.List()
			rs.Len()
		}(i)
	}
	wg.Wait()
}

func TestRemote_FilterApplied(t *testing.T) {
	r := Remote{
		Name: "filtered",
		Filter: func(d Diff) bool {
			return d.EntityType == "allowed"
		},
	}

	allowed := Diff{EntityType: "allowed"}
	blocked := Diff{EntityType: "blocked"}

	if !r.Filter(allowed) {
		t.Fatal("expected filter to pass allowed")
	}
	if r.Filter(blocked) {
		t.Fatal("expected filter to block blocked")
	}
}
