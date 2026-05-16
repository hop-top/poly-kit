package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
)

// ServeCmd returns the `serve` command exposing missions as a REST API.
func ServeCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Expose missions as a REST API",
		Long:  "Starts an HTTP server serving the mission archive over JSON endpoints.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /missions", handleListMissions)
			mux.HandleFunc("GET /missions/{id}", handleGetMission)

			fmt.Printf("  Listening on %s\n", addr)
			fmt.Println("  Endpoints:")
			fmt.Println("    GET /missions      — list all missions")
			fmt.Println("    GET /missions/{id} — get mission by ID")

			srv := &http.Server{Addr: addr, Handler: mux}
			ctx := cmd.Context()
			go func() {
				<-ctx.Done()
				_ = srv.Shutdown(context.Background())
			}()
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "Listen address")
	return cmd
}

func handleListMissions(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data.Missions)
}

func handleGetMission(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, ok := data.FindMission(id)
	if !ok {
		http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}
