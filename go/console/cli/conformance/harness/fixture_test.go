package harness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	kitcli "hop.top/kit/go/console/cli"
	xrr "hop.top/xrr"
	xrrexec "hop.top/xrr/adapters/exec"
	xrrgrpc "hop.top/xrr/adapters/grpc"
	xrrhttp "hop.top/xrr/adapters/http"
	xrrredis "hop.top/xrr/adapters/redis"
	xrrsql "hop.top/xrr/adapters/sql"
)

// fixturePayload is the structured stdout shape the JSON leaves
// emit. AssertJSONSchema validates against this.
type fixturePayload struct {
	Outcome   string `json:"outcome"`
	MissionID string `json:"mission_id"`
}

// buildFixture returns a cobra root mounting one or two leaves per
// xrr adapter (exec, http, grpc, redis, sql, fs) plus a handful of
// edge-case leaves for the JSON-schema / exit-class / destructive
// flows.
//
// Each leaf takes a --behavior flag steering which cassette it
// emits, so a single leaf can exercise both passing and failing
// scenarios per assertion. Cassettes land in the dir specified by
// XRR_CASSETTE_DIR; if the env var is unset the leaf is a no-op
// (so AssertCapabilityRoundtrip's --help walk doesn't trip
// adapter machinery).
func buildFixture() *cobra.Command {
	root := &cobra.Command{
		Use:          "fixture",
		Short:        "fixture CLI for harness self-tests",
		SilenceUsage: true,
	}

	addExec(root)
	addHTTP(root)
	addGRPC(root)
	addRedis(root)
	addSQL(root)
	addFS(root)
	addJSONLeaves(root)
	addExitLeaves(root)
	addDestructiveLeaves(root)
	addInteractiveLeaf(root)
	addIdempotentLeaves(root)
	return root
}

// session returns the xrr session bound to XRR_CASSETTE_DIR. nil
// (no session) when env is unset — leaves no-op gracefully.
func session() (xrr.Session, error) {
	dir := os.Getenv("XRR_CASSETTE_DIR")
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	mode := xrr.Mode(os.Getenv("XRR_MODE"))
	if mode == "" {
		mode = xrr.ModeRecord
	}
	return xrr.NewSession(mode, xrr.NewFileCassette(dir)), nil
}

// recordHTTP wraps an HTTP request via xrr; do is the real call
// (here, a stub returning success).
func recordHTTP(s xrr.Session, method, url string) error {
	if s == nil {
		return nil
	}
	a := xrrhttp.NewAdapter()
	_, err := s.Record(context.Background(), a,
		&xrrhttp.Request{Method: method, URL: url},
		func() (xrr.Response, error) {
			return &xrrhttp.Response{Status: 200, Body: "ok"}, nil
		})
	return err
}

func recordSQL(s xrr.Session, query string) error {
	if s == nil {
		return nil
	}
	a := xrrsql.NewAdapter()
	_, err := s.Record(context.Background(), a,
		&xrrsql.Request{Query: query},
		func() (xrr.Response, error) {
			return &xrrsql.Response{Rows: nil, Affected: 1}, nil
		})
	return err
}

func recordRedis(s xrr.Session, cmd string, args ...string) error {
	if s == nil {
		return nil
	}
	a := xrrredis.NewAdapter()
	_, err := s.Record(context.Background(), a,
		&xrrredis.Request{Command: cmd, Args: args},
		func() (xrr.Response, error) {
			return &xrrredis.Response{Result: "OK"}, nil
		})
	return err
}

func recordGRPC(s xrr.Session, service, method string) error {
	if s == nil {
		return nil
	}
	a := xrrgrpc.NewAdapter()
	_, err := s.Record(context.Background(), a,
		&xrrgrpc.Request{Service: service, Method: method, Message: []byte{}},
		func() (xrr.Response, error) {
			return &xrrgrpc.Response{StatusCode: 0, Message: []byte{}}, nil
		})
	return err
}

func recordExec(s xrr.Session, argv ...string) error {
	if s == nil {
		return nil
	}
	a := xrrexec.NewAdapter()
	_, err := s.Record(context.Background(), a,
		&xrrexec.Request{Argv: argv},
		func() (xrr.Response, error) {
			return &xrrexec.Response{Stdout: "", ExitCode: 0}, nil
		})
	return err
}

// addExec / addHTTP / etc. register two leaves: one read-shaped,
// one write-shaped.
func addExec(root *cobra.Command) {
	read := leaf("exec-read", "read", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		// Read-only exec: classifier is conservative by default
		// (treats all exec as Write), but harness.WithExecClassifier
		// in the test will reclassify "ls" to Read.
		return recordExec(s, "ls", "-la")
	})
	read.Flags().Bool("dry-run", false, "dry-run mode")
	write := leaf("exec-write", "write", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		return recordExec(s, "rm", "-rf", "/tmp/x")
	})
	write.Flags().Bool("dry-run", false, "dry-run mode")
	root.AddCommand(read, write)
}

func addHTTP(root *cobra.Command) {
	read := leaf("http-read", "read", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		return recordHTTP(s, "GET", "http://example/list")
	})
	write := leaf("http-write", "write", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if dryRun {
			return recordHTTP(s, "GET", "http://example/list")
		}
		return recordHTTP(s, "POST", "http://example/missions")
	})
	dryRunRebel := leaf("http-rebel", "write", func(cmd *cobra.Command, args []string) error {
		// Ignores --dry-run: always POSTs.
		s, _ := session()
		return recordHTTP(s, "POST", "http://example/missions")
	})
	write.Flags().Bool("dry-run", false, "dry-run mode")
	dryRunRebel.Flags().Bool("dry-run", false, "dry-run mode")
	root.AddCommand(read, write, dryRunRebel)
}

func addGRPC(root *cobra.Command) {
	read := leaf("grpc-read", "read", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		return recordGRPC(s, "missions.Svc", "ListMissions")
	})
	write := leaf("grpc-write", "write", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		return recordGRPC(s, "missions.Svc", "CreateMission")
	})
	root.AddCommand(read, write)
}

func addRedis(root *cobra.Command) {
	read := leaf("redis-read", "read", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		return recordRedis(s, "GET", "k")
	})
	write := leaf("redis-write", "write", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		return recordRedis(s, "SET", "k", "v")
	})
	root.AddCommand(read, write)
}

func addSQL(root *cobra.Command) {
	read := leaf("sql-read", "read", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		return recordSQL(s, "SELECT 1")
	})
	write := leaf("sql-write", "write", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if dryRun {
			return recordSQL(s, "SELECT count(*) FROM missions")
		}
		return recordSQL(s, "INSERT INTO missions (id) VALUES (1)")
	})
	write.Flags().Bool("dry-run", false, "dry-run mode")
	root.AddCommand(read, write)
}

// addFS uses kit-level annotations only; xrr's fs adapter is not
// available in v0.1.0-alpha.3 so we emit nothing through xrr here.
// The fs-classifier path is exercised via the classifier package
// tests; the leaf exists to keep the fixture symmetric.
func addFS(root *cobra.Command) {
	fs := leaf("fs-write", "write", func(cmd *cobra.Command, args []string) error {
		return nil
	})
	root.AddCommand(fs)
}

func addJSONLeaves(root *cobra.Command) {
	schemaJSON, _ := json.Marshal(map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"required":             []string{"outcome", "mission_id"},
		"additionalProperties": false,
		"properties": map[string]any{
			"outcome":    map[string]any{"type": "string"},
			"mission_id": map[string]any{"type": "string"},
		},
	})

	pass := leaf("json-pass", "read", func(cmd *cobra.Command, args []string) error {
		payload := fixturePayload{Outcome: "ok", MissionID: "m-1"}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(payload)
	})
	pass.Annotations["kit/output-schema"] = string(schemaJSON)
	pass.Annotations["kit/output-schema-version"] = "1"
	pass.Flags().String("format", "json", "output format")

	miss := leaf("json-missing-field", "read", func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), `{"outcome":"ok"}`)
		return nil
	})
	miss.Annotations["kit/output-schema"] = string(schemaJSON)
	miss.Annotations["kit/output-schema-version"] = "1"
	miss.Flags().String("format", "json", "output format")

	bad := leaf("json-bad", "read", func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), `{"outcome": "simulated", "extra": ,}`)
		return nil
	})
	bad.Annotations["kit/output-schema"] = string(schemaJSON)
	bad.Annotations["kit/output-schema-version"] = "1"
	bad.Flags().String("format", "json", "output format")

	root.AddCommand(pass, miss, bad)
}

// codedError implements the coded interface exit_code_class.go uses
// to derive an exit-code class from the inner error.
type codedError struct {
	code string
	msg  string
}

func (e *codedError) Error() string { return e.msg }
func (e *codedError) Code() string  { return e.code }

func addExitLeaves(root *cobra.Command) {
	ok := leaf("exit-ok", "read", func(cmd *cobra.Command, args []string) error {
		return nil
	})
	ok.Annotations["kit/exit-codes"] = "OK"

	nf := leaf("exit-not-found", "read", func(cmd *cobra.Command, args []string) error {
		return &codedError{code: "NOT_FOUND", msg: "no such thing"}
	})
	nf.Annotations["kit/exit-codes"] = "OK,NOT_FOUND"

	bad := leaf("exit-wrong-class", "read", func(cmd *cobra.Command, args []string) error {
		return &codedError{code: "UNAUTHORIZED", msg: "token expired"}
	})
	bad.Annotations["kit/exit-codes"] = "OK,NOT_FOUND"

	root.AddCommand(ok, nf, bad)
}

func addDestructiveLeaves(root *cobra.Command) {
	// proper-gated: refuses without --confirm, mutates with --confirm.
	proper := leaf("delete", "destructive", func(cmd *cobra.Command, args []string) error {
		confirm, _ := cmd.Flags().GetString("confirm")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		s, _ := session()
		if confirm != "yes" {
			return &codedError{code: "UNAUTHORIZED", msg: "confirmation required"}
		}
		if dryRun {
			return nil
		}
		return recordSQL(s, "DELETE FROM missions WHERE id = 1")
	})
	proper.Flags().String("confirm", "", "confirmation mode")
	proper.Flags().Bool("dry-run", false, "")

	// broken-gated: proceeds without --confirm (bad).
	broken := leaf("delete-broken", "destructive", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		return recordSQL(s, "DELETE FROM missions WHERE id = 1")
	})
	broken.Flags().String("confirm", "", "confirmation mode")
	broken.Flags().Bool("dry-run", false, "")

	root.AddCommand(proper, broken)
}

func addInteractiveLeaf(root *cobra.Command) {
	leaf := leaf("shell", "interactive", func(cmd *cobra.Command, args []string) error {
		return nil
	})
	root.AddCommand(leaf)
}

func addIdempotentLeaves(root *cobra.Command) {
	idempotent := leaf("idempotent-sql", "write", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		// Same SELECT each time; xrr's last-writer-wins means the
		// cassette set is identical across applies.
		return recordSQL(s, "SELECT 1")
	})
	nonIdempotent := leaf("non-idempotent-sql", "write", func(cmd *cobra.Command, args []string) error {
		s, _ := session()
		// Different INSERT each time: timestamp embedded in the
		// statement means a new fingerprint per apply.
		dir := os.Getenv("XRR_CASSETTE_DIR")
		seed := strings.TrimSpace(filenameOf(dir))
		return recordSQL(s, fmt.Sprintf("INSERT INTO t (v) VALUES (%q)", seed))
	})
	root.AddCommand(idempotent, nonIdempotent)
}

// filenameOf returns the last path segment of dir; we use it as the
// fingerprint-perturbing value for non-idempotent-sql so two
// successive applies produce different fingerprints.
func filenameOf(p string) string {
	if p == "" {
		return "x"
	}
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// leaf is a tiny helper to construct a cobra command pre-annotated
// with kit/side-effect. We set the annotation directly (rather than
// via kitcli.SetSideEffect) so the fixture works in environments
// where kit's full Root.Validate machinery hasn't been wired up.
func leaf(name, sideEffect string, run func(*cobra.Command, []string) error) *cobra.Command {
	c := &cobra.Command{
		Use:   name,
		Short: name + " leaf",
		Long:  name + " — fixture leaf for harness self-tests",
		RunE:  run,
	}
	c.Annotations = map[string]string{
		"kit/side-effect": sideEffect,
	}
	return c
}

// Ensure the kitcli import is used at least once. Adopter
// integrations typically read annotations via the typed helpers
// (kitcli.GetOutputSchemaJSON, etc.); the fixture writes the
// annotation directly for simplicity.
var _ = kitcli.GetFormatFlag
