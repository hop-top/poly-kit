package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli/conformance/harness/classifier"
	"hop.top/kit/go/console/cli/conformance/harness/diff"
	xrr "hop.top/xrr"
)

// PlanApplyReplay runs cmd twice and asserts the second run
// produced no net change in the cassette directory. The two runs
// are recorded into sibling cassette dirs (apply-1, apply-2);
// after the second run, the diff between them must be empty per
// the cassette equality rule (multiset of (adapter, fingerprint)
// modulo recorded_at noise).
//
// Both applies run under xrr.ModeRecord — NOT ModeReplay. Replay
// would short-circuit do() and the harness would assert nothing
// about whether the second apply re-issued the same calls.
//
// On failure the diff is reported as the failure message and the
// two cassette dirs are left on disk under t.TempDir() so the
// adopter can post-mortem them.
//
// Adopters wire xrr.SessionFromEnv into their adapter call sites;
// PlanApplyReplay exports XRR_CASSETTE_DIR + XRR_MODE around each
// invocation so the adopter's wrappers pick up the right dir.
func PlanApplyReplay(t TB, cmd *cobra.Command, opts ...Option) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("PlanApplyReplay: cmd is nil")
		return
	}
	c := apply(opts)

	// Per-apply sandboxes. WithCassetteDir overrides the base; the
	// two applies land in <base>/apply-1 and <base>/apply-2.
	base := c.cassetteDir
	if base == "" {
		base = t.TempDir()
	}
	dir1 := filepath.Join(base, "apply-1")
	dir2 := filepath.Join(base, "apply-2")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatalf("PlanApplyReplay: mkdir apply-1: %v", err)
	}
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatalf("PlanApplyReplay: mkdir apply-2: %v", err)
	}

	// First apply.
	c1 := *c
	c1.cassetteDir = dir1
	c1.mode = xrr.ModeRecord
	res1 := runCaptured(&c1, cmd)
	if res1.runErr != nil {
		t.Errorf("PlanApplyReplay: first apply failed: %v\n  stderr: %s",
			res1.runErr, truncate(res1.stderr.String(), 500))
		return
	}

	// Second apply, fresh cobra args.
	c2 := *c
	c2.cassetteDir = dir2
	c2.mode = xrr.ModeRecord
	res2 := runCaptured(&c2, cmd)
	if res2.runErr != nil {
		t.Errorf("PlanApplyReplay: second apply failed: %v\n  stderr: %s",
			res2.runErr, truncate(res2.stderr.String(), 500))
		return
	}

	d, err := diff.Cassettes(dir1, dir2)
	if err != nil {
		t.Errorf("PlanApplyReplay: diff: %v", err)
		return
	}
	if d.Empty() {
		return
	}

	// Mark read-class modified entries as informational only.
	ov := c.classifierOverrides()
	readClass := func(adapter string, req map[string]any) bool {
		if req == nil {
			// Re-read the canonical request from disk for the entry.
			return false
		}
		return classifier.Classify(adapter, anyReqFromPayload(adapter, req), ov) == classifier.ClassRead
	}
	// Augment Format by pre-classifying each modified entry; the
	// closure above is the contract diff.Format expects.
	msg := d.Format(readClass)
	t.Errorf("PlanApplyReplay: %s", msg)
}

// anyReqFromPayload reconstructs a typed xrr request from the
// YAML-decoded request payload. The classifier dispatch uses it to
// decide whether a modified diff entry is Read-class. Unknown
// adapters return nil → ClassUnknown → "not read".
func anyReqFromPayload(adapter string, payload map[string]any) any {
	if payload == nil {
		return nil
	}
	switch adapter {
	case "http":
		method, _ := payload["method"].(string)
		url, _ := payload["url"].(string)
		return &xrrHTTPReq{Method: method, URL: url}
	case "sql":
		q, _ := payload["query"].(string)
		return &xrrSQLReq{Query: q}
	case "grpc":
		svc, _ := payload["service"].(string)
		m, _ := payload["method"].(string)
		return &xrrGRPCReq{Service: svc, Method: m}
	case "redis":
		cmd, _ := payload["command"].(string)
		args, _ := payload["args"].([]any)
		strs := make([]string, 0, len(args))
		for _, a := range args {
			strs = append(strs, fmt.Sprintf("%v", a))
		}
		return &xrrRedisReq{Command: cmd, Args: strs}
	case "fs":
		op, _ := payload["op"].(string)
		return &classifier.FSRequest{Op: op}
	case "exec":
		argv, _ := payload["argv"].([]any)
		strs := make([]string, 0, len(argv))
		for _, a := range argv {
			strs = append(strs, fmt.Sprintf("%v", a))
		}
		return &xrrExecReq{Argv: strs}
	}
	return nil
}
