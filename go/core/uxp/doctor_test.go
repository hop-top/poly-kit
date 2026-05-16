package uxp

import "testing"

func TestNewDoctor(t *testing.T) {
	d := NewDoctor()
	if d == nil {
		t.Fatal("NewDoctor() returned nil")
	}
}

func TestRunEmpty(t *testing.T) {
	d := NewDoctor()
	results := d.Run()
	if len(results) != 0 {
		t.Errorf("Run() on empty doctor = %d checks, want 0", len(results))
	}
}

func TestAddAppendsChecks(t *testing.T) {
	d := NewDoctor()
	d.Add(func() Check { return Check{Name: "a", Status: StatusOK} })
	d.Add(func() Check { return Check{Name: "b", Status: StatusOK} })
	results := d.Run()
	if len(results) != 2 {
		t.Fatalf("Run() = %d checks, want 2", len(results))
	}
	if results[0].Name != "a" || results[1].Name != "b" {
		t.Errorf("checks not appended in order: got %q, %q", results[0].Name, results[1].Name)
	}
}

func TestRunReturnsAllStatuses(t *testing.T) {
	d := NewDoctor()
	d.Add(func() Check { return Check{Name: "ok", Status: StatusOK, Message: "good"} })
	d.Add(func() Check { return Check{Name: "warn", Status: StatusWarn, Message: "meh"} })
	d.Add(func() Check {
		return Check{Name: "fail", Status: StatusFail, Message: "bad", Detail: "details"}
	})
	d.Add(func() Check { return Check{Name: "skip", Status: StatusSkip, Message: "skipped"} })

	results := d.Run()
	if len(results) != 4 {
		t.Fatalf("Run() = %d checks, want 4", len(results))
	}
}

func TestRunSortsBySeverity(t *testing.T) {
	d := NewDoctor()
	// Add in non-severity order.
	d.Add(func() Check { return Check{Name: "skip", Status: StatusSkip} })
	d.Add(func() Check { return Check{Name: "ok", Status: StatusOK} })
	d.Add(func() Check { return Check{Name: "warn", Status: StatusWarn} })
	d.Add(func() Check { return Check{Name: "fail", Status: StatusFail} })

	results := d.Run()
	want := []struct {
		name   string
		status CheckStatus
	}{
		{"fail", StatusFail},
		{"warn", StatusWarn},
		{"ok", StatusOK},
		{"skip", StatusSkip},
	}

	if len(results) != len(want) {
		t.Fatalf("Run() = %d checks, want %d", len(results), len(want))
	}
	for i, w := range want {
		if results[i].Name != w.name || results[i].Status != w.status {
			t.Errorf("results[%d] = {%q, %d}, want {%q, %d}",
				i, results[i].Name, results[i].Status, w.name, w.status)
		}
	}
}

func TestRunSortsBySeverityMultipleSame(t *testing.T) {
	d := NewDoctor()
	d.Add(func() Check { return Check{Name: "ok-1", Status: StatusOK} })
	d.Add(func() Check { return Check{Name: "fail-1", Status: StatusFail} })
	d.Add(func() Check { return Check{Name: "fail-2", Status: StatusFail} })
	d.Add(func() Check { return Check{Name: "warn-1", Status: StatusWarn} })

	results := d.Run()
	// Fails first, then warns, then ok.
	if results[0].Status != StatusFail || results[1].Status != StatusFail {
		t.Errorf("expected first two to be Fail, got %d and %d",
			results[0].Status, results[1].Status)
	}
	if results[2].Status != StatusWarn {
		t.Errorf("expected third to be Warn, got %d", results[2].Status)
	}
	if results[3].Status != StatusOK {
		t.Errorf("expected fourth to be OK, got %d", results[3].Status)
	}
}

// Regression: CheckStoreExists must include the actual error in Detail,
// not just the path. And must distinguish not-found (Warn) from other
// errors like permission denied (Fail).
func TestCheckStoreExists_NotFoundIncludesError(t *testing.T) {
	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			"testcli": {
				Name:           "testcli",
				StoreRootPaths: StorePaths{Data: "/nonexistent/path/xyz"},
			},
		},
	}

	fn := CheckStoreExists("testcli", reg)
	result := fn()

	if result.Status != StatusWarn {
		t.Errorf("not-found should be Warn, got %d", result.Status)
	}
	// Detail must contain the actual error, not just the path.
	if result.Detail == "/nonexistent/path/xyz" {
		t.Error("Detail should include error message, not just path (regression)")
	}
	if result.Detail == "" {
		t.Error("Detail should not be empty")
	}
}

func TestCheckStatusValues(t *testing.T) {
	// Verify iota ordering matches expected severity sort key.
	if StatusOK != 0 {
		t.Errorf("StatusOK = %d, want 0", StatusOK)
	}
	if StatusWarn != 1 {
		t.Errorf("StatusWarn = %d, want 1", StatusWarn)
	}
	if StatusFail != 2 {
		t.Errorf("StatusFail = %d, want 2", StatusFail)
	}
	if StatusSkip != 3 {
		t.Errorf("StatusSkip = %d, want 3", StatusSkip)
	}
}
