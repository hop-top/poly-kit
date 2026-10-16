package cli_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The packages and modules a CLI built on cli.Root alone must not link.
// Each entry is a cut made on purpose; a failure names the one that
// came back. Asking about go/console/cli is the same as asking about a
// minimal main, because a main that builds a Root links exactly cli's
// closure; the rootprobe main is asked too.
var (
	// forbiddenPackages are the opt-in planes and what only they need.
	forbiddenPackages = map[string]string{
		// The MCP service and its SDK: go/console/cli/mcpserve.
		"hop.top/kit/go/console/cli/mcpserve": "the mcp service is opt-in (mcpserve.With)",
		"hop.top/kit/go/transport/mcpsdk":     "MCP belongs in go/console/cli/mcpserve",
		// transport/rpc registers the generic CRUD proto types at init,
		// which the linker cannot drop.
		"hop.top/kit/go/console/cli/rpcserve": "the rpc service is opt-in (rpcserve.With)",
		"hop.top/kit/go/transport/rpc":        "the rpc service belongs in go/console/cli/rpcserve",
		// The permissions: rule evaluator is CEL, megabytes of it.
		"hop.top/kit/go/console/cli/celpermission": "the CEL rule evaluator is opt-in (celpermission.With)",
		// The serve plane: go/console/cli/service.
		"hop.top/kit/go/console/cli/service":    "the serve plane is opt-in (go/console/cli/service)",
		"hop.top/kit/go/console/cli/svcconfig":  "service configuration belongs to the serve plane",
		"hop.top/kit/go/console/serve":          "the supervisor belongs to the serve plane",
		"hop.top/kit/go/transport/api":          "the HTTP api belongs to the serve plane",
		"hop.top/kit/go/transport/authn":        "serve authentication belongs to the serve plane",
		"hop.top/kit/go/transport/cmdsurface":   "the bridge belongs to the serve plane; cli reads cmdsurface/admitted",
		"hop.top/kit/go/transport/socket":       "the socket service belongs to the serve plane",
		"hop.top/kit/go/transport/transportsvc": "the transport seam belongs to the serve plane",
		"hop.top/kit/go/security":               "audit chains and TLS belong to the serve plane",
		"hop.top/kit/go/storage/kv":             "the usage ledger store belongs to the serve plane",
		"hop.top/kit/go/storage/kv/memory":      "the usage ledger store belongs to the serve plane",
		"hop.top/kit/go/storage/kv/sqlite":      "the usage ledger store belongs to the serve plane",
		// The SQLite stores: opt-in packages of their own.
		"hop.top/kit/go/runtime/bus/sqlite":           "the SQLite bus adapter is opt-in (sqlite.NewAdapter)",
		"hop.top/kit/go/console/cli/idemstore/sqlite": "the SQLite idempotency store is opt-in (sqlite.Open)",
	}
	forbiddenModulePrefixes = map[string]string{
		"github.com/modelcontextprotocol/go-sdk": "MCP belongs in go/console/cli/mcpserve",
		// cel-go moved to cel.dev/cel-go in v0.32; both paths guarded.
		"cel.dev/cel-go":                   "CEL belongs in go/console/cli/celpermission",
		"github.com/google/cel-go":         "CEL belongs in go/console/cli/celpermission",
		"google.golang.org/protobuf":       "protobuf comes with the serve plane",
		"connectrpc.com/connect":           "connect comes with the serve plane",
		"github.com/go-jose/go-jose/v4":    "JOSE comes with serve authentication",
		"github.com/danielgtaylor/huma/v2": "huma comes with the HTTP api",
		"github.com/klauspost/compress":    "response compression comes with the HTTP plane",
		"github.com/rs/cors":               "CORS comes with the HTTP plane",
		"github.com/gofrs/flock":           "file locks come with the serve plane",
		"github.com/aws/aws-lambda-go":     "the Lambda adapter comes with the bridge",
		"github.com/robfig/cron/v3":        "cron comes with the bridge",
	}
)

// closure returns the non-standard packages and the modules target
// links, from go list -deps run in this package's directory.
func closure(t *testing.T, target string) (pkgs, mods map[string]bool) {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", "-f",
		"{{if not .Standard}}{{.ImportPath}} {{if .Module}}{{.Module.Path}}{{end}}{{end}}",
		target).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", target, err, out)
	}
	pkgs, mods = map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		pkgs[f[0]] = true
		if len(f) > 1 {
			mods[f[1]] = true
		}
	}
	return pkgs, mods
}

// TestCLIRootLinksNoServePlane pins that a kit CLI which never serves
// links none of the serve plane, nor any opt-in service: they live in
// go/console/cli/service, mcpserve, rpcserve and celpermission, and
// only tools importing them pay for them.
func TestCLIRootLinksNoServePlane(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	for _, target := range []string{"hop.top/kit/go/console/cli", "./testdata/rootprobe"} {
		t.Run(target, func(t *testing.T) {
			pkgs, mods := closure(t, target)
			if !pkgs["hop.top/kit/go/console/cli"] {
				t.Fatalf("%s does not link go/console/cli; the guard is not looking where it should", target)
			}
			for p, why := range forbiddenPackages {
				if pkgs[p] {
					t.Errorf("%s links %s: %s", target, p, why)
				}
			}
			for m := range mods {
				for prefix, why := range forbiddenModulePrefixes {
					if m == prefix || strings.HasPrefix(m, prefix+"/") {
						t.Errorf("%s links module %s: %s", target, m, why)
					}
				}
			}
		})
	}
}
