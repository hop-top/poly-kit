package security_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/security"
)

func ExampleVerifyAuditLog() {
	dir, _ := os.MkdirTemp("", "audit")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "audit.chain")

	log, err := security.OpenAuditLog(path, security.AuditLogOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}
	_, _ = log.Append([]byte(`{"path":"widget add","caller":"alice"}`))
	_, _ = log.Append([]byte(`{"path":"widget purge","caller":"bob"}`))
	_ = log.Close()

	rep, _ := security.VerifyAuditLog(path)
	fmt.Println("holds:", rep.OK(), "records:", rep.Records)

	// Rewrite history: bob's call becomes eve's.
	raw, _ := os.ReadFile(path)
	_ = os.WriteFile(path, []byte(strings.Replace(string(raw), `"bob"`, `"eve"`, 1)), 0o600)
	rep, _ = security.VerifyAuditLog(path)
	fmt.Println("holds:", rep.OK(), "line:", rep.Break.Line)
	// Output:
	// holds: true records: 2
	// holds: false line: 2
}
