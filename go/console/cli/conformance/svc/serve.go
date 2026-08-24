package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/go/conformance/svc"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/api"
)

// serveCmd binds the "kit conformance svc serve" leaf.
func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the conformance grading HTTP service",
		Args:  cobra.NoArgs,
		RunE:  runServe,
	}
	cmd.Flags().Int("port", 0, "Listen port (0 = auto-assign)")
	cmd.Flags().String("addr", "", "Bind address (host portion; overrides 0.0.0.0)")
	cmd.Flags().String("scenarios-root", os.Getenv("KIT_CONF_SVC_SCENARIOS_ROOT"),
		"Root directory containing scenarios/<ns>/<id>/<version>/ (required)")
	cmd.Flags().String("claims-db", os.Getenv("KIT_CONF_SVC_CLAIMS_DB"),
		"SQLite path for the bearer-token claim store (required)")
	cmd.Flags().String("judges-config", os.Getenv("KIT_CONF_SVC_JUDGES_CONFIG"),
		"Path to judges.yaml (omit to refuse all AI judges)")
	cmd.Flags().Int("hard-cap-mb", 64, "Cassette hard cap in megabytes")
	cmd.Flags().Int("soft-cap-mb", 8, "Cassette soft cap in megabytes (informational)")
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	return cmd
}

func runServe(cmd *cobra.Command, _ []string) error {
	port, _ := cmd.Flags().GetInt("port")
	addr, _ := cmd.Flags().GetString("addr")
	scenariosRoot, _ := cmd.Flags().GetString("scenarios-root")
	claimsDB, _ := cmd.Flags().GetString("claims-db")
	judgesConfig, _ := cmd.Flags().GetString("judges-config")
	hardMB, _ := cmd.Flags().GetInt("hard-cap-mb")
	softMB, _ := cmd.Flags().GetInt("soft-cap-mb")

	if scenariosRoot == "" {
		return &output.Error{Code: "USAGE",
			Message:  "--scenarios-root is required (or KIT_CONF_SVC_SCENARIOS_ROOT)",
			ExitCode: 2}
	}
	if claimsDB == "" {
		return &output.Error{Code: "USAGE",
			Message:  "--claims-db is required (or KIT_CONF_SVC_CLAIMS_DB)",
			ExitCode: 2}
	}

	ctx := cmd.Context()

	store, err := svc.NewFSStore(ctx, scenariosRoot)
	if err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("scenario store: %v", err), ExitCode: 1}
	}
	claims, err := svc.OpenSQLClaimStore(claimsDB)
	if err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("claim store: %v", err), ExitCode: 1}
	}
	defer func() { _ = claims.Close() }()

	var judges svc.ModelRegistry
	if judgesConfig == "" {
		judges = svc.NullRegistry{}
	} else {
		j, err := svc.NewConfigRegistry(judgesConfig)
		if err != nil {
			return &output.Error{Code: svc.CodeSvcInternal,
				Message: fmt.Sprintf("judges config: %v", err), ExitCode: 1}
		}
		judges = j
	}

	// LibGrader delegates to the scenario library: every verdict comes
	// from scenario.Grade's assertion walk over the uploaded captures.
	service := svc.NewService(store, claims, svc.LibGrader{})
	service.Judges = judges
	service.Receiver = &svc.CassetteReceiver{
		HardCap: int64(hardMB) << 20,
		SoftCap: int64(softMB) << 20,
	}

	router := api.NewRouter(
		api.WithMiddleware(
			api.RequestID(),
			api.Recovery(func(v any, _ *http.Request) {
				fmt.Fprintf(os.Stderr, "panic: %v\n", v)
			}),
		),
	)
	service.Mount(router)

	listenAddr := addr
	if listenAddr == "" {
		listenAddr = ""
	}
	ln, err := net.Listen("tcp", listenAddr+":"+strconv.Itoa(port))
	if err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("listen: %v", err), ExitCode: 1}
	}
	bound := ln.Addr().(*net.TCPAddr)

	startup, _ := json.Marshal(map[string]any{
		"port":             bound.Port,
		"pid":              os.Getpid(),
		"version":          svc.Version,
		"scenarios_loaded": countScenarios(ctx, store),
	})
	fmt.Fprintln(cmd.OutOrStdout(), string(startup))

	srv := &http.Server{Handler: router}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}

// countScenarios returns the total visible scenario count for the
// startup JSON line.
func countScenarios(ctx context.Context, store svc.ScenarioStore) int {
	nss, _ := store.Namespaces(ctx)
	n := 0
	for _, ns := range nss {
		ms, _ := store.List(ctx, ns)
		n += len(ms)
	}
	return n
}
