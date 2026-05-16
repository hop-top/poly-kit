// Command routellm-server starts an OpenAI-compatible HTTP server that
// routes chat completion requests using the native router engine.
//
// Usage:
//
//	routellm-server \
//	  --strong-model gpt-4-1106-preview \
//	  --weak-model mixtral-8x7b-instruct-v0.1 \
//	  --routers random \
//	  --addr :6060
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"hop.top/kit/go/ai/llm/router"
)

func main() {
	var (
		addr        = flag.String("addr", ":6060", "listen address")
		strongModel = flag.String("strong-model", "gpt-4-1106-preview",
			"strong model name")
		weakModel = flag.String("weak-model",
			"mixtral-8x7b-instruct-v0.1", "weak model name")
		routers = flag.String("routers", "random",
			"comma-separated list of routers to enable")
	)
	flag.Parse()

	reg := router.NewRegistry()

	for _, name := range strings.Split(*routers, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		switch name {
		case "random":
			if err := reg.Register("random",
				router.NewRandomRouter(nil)); err != nil {
				log.Fatalf("register router %q: %v", name, err)
			}
		default:
			log.Fatalf("unknown router %q; available: random", name)
		}
	}

	pair := router.ModelPair{
		Strong: *strongModel,
		Weak:   *weakModel,
	}

	ctrl := router.NewController(reg, pair)
	srv := router.NewServer(ctrl)

	httpSrv := &http.Server{
		Addr:    *addr,
		Handler: srv,
	}

	// Graceful shutdown.
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		fmt.Printf("routellm-server listening on %s\n", *addr)
		fmt.Printf("  strong=%s weak=%s routers=%s\n",
			*strongModel, *weakModel, *routers)
		if err := httpSrv.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-done
	fmt.Println("\nshutting down...")

	ctx, cancel := context.WithTimeout(
		context.Background(), 5*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
	fmt.Println("done")
}
