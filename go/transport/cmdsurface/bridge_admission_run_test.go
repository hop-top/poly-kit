package cmdsurface

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// admissionLog records audit records for the Admission.Run tests.
type admissionLog struct {
	mu   sync.Mutex
	errs []error
	res  []Result
}

func (a *admissionLog) Emit(_ context.Context, _ Invocation, res Result, err error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errs = append(a.errs, err)
	a.res = append(a.res, res)
	return nil
}

func TestAdmissionRun_RunsOnceAndAuditsTheOutcome(t *testing.T) {
	calls := 0
	log := &admissionLog{}
	b := New(newBridgeTree(), WithRunner(countingRunner(&calls, nil)),
		WithSinks(SinkSpec{Sink: log, OnError: true, OnOK: true}))
	b.Expose("*", SurfaceMCP)

	adm, err := b.Admit(context.Background(), Invocation{
		Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceMCP},
	})
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if calls != 0 || len(log.errs) != 0 {
		t.Fatalf("Admit ran (%d) or recorded (%d)", calls, len(log.errs))
	}

	res, err := adm.Run(context.Background())
	if err != nil || res.Stdout != "ran" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
	if len(log.errs) != 1 || log.errs[0] != nil || log.res[0].Stdout != "ran" {
		t.Fatalf("audit = %+v %+v, want one clean record of the run", log.errs, log.res)
	}
}

func TestAdmissionRun_RefusalNeverReachesARun(t *testing.T) {
	calls := 0
	b := New(newBridgeTree(), WithRunner(countingRunner(&calls, nil)),
		WithPermission(denyCaller("mallory")))
	b.Expose("*", SurfaceMCP)

	adm, err := b.Admit(context.Background(), Invocation{
		Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceMCP, Caller: "mallory"},
	})
	if !errors.Is(err, ErrPermissionDenied) || adm != nil {
		t.Fatalf("Admit = %v, %v; want a permission refusal and no admission", adm, err)
	}
	if calls != 0 {
		t.Fatal("a refused call ran")
	}
}
