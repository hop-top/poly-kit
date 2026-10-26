// Command gonode is the Go peer of the bus network cross-language
// harness. It runs one bus with a NetworkAdapter, either serving peers
// (-mode hub) or dialing one (-mode client), and talks JSON lines:
//
//   - stdout: {"event":"ready","url":...} once serving or connected, then
//     {"event":"got","topic":...,"source":...,"payload":...} for every
//     event its bus delivers, local publishes included;
//   - stdin: {"cmd":"publish","topic":...,"source":...,"payload":...}
//     publishes on its bus; {"cmd":"quit"} or EOF closes and exits.
//
// The Rust side drives it from tests/bus_network_crosslang.rs.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"hop.top/kit/go/runtime/bus"
)

type command struct {
	Cmd     string `json:"cmd"`
	Topic   string `json:"topic"`
	Source  string `json:"source"`
	Payload any    `json:"payload"`
}

var outMu sync.Mutex

func emit(v map[string]any) {
	b, _ := json.Marshal(v)
	outMu.Lock()
	defer outMu.Unlock()
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gonode:", err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "hub", "hub or client")
	listen := flag.String("listen", "127.0.0.1:0", "hub: listen address")
	connect := flag.String("connect", "", "client: peer URL")
	origin := flag.String("origin", "", "origin id")
	token := flag.String("token", "", "shared auth token")
	relay := flag.Bool("relay", false, "hub: relay between peers")
	flag.Parse()

	b := bus.New()
	var opts []bus.NetworkOption
	if *origin != "" {
		opts = append(opts, bus.WithOriginID(*origin))
	}
	if *token != "" {
		opts = append(opts, bus.WithAuth(&bus.StaticTokenAuth{Token_: *token}))
	}
	opts = append(opts, bus.WithRelay(*relay))
	n := bus.NewNetworkAdapter(b, opts...)

	b.Subscribe("#", func(_ context.Context, e bus.Event) error {
		if strings.HasPrefix(string(e.Topic), "kit.runtime.") {
			return nil
		}
		emit(map[string]any{"event": "got", "topic": string(e.Topic), "source": e.Source, "payload": e.Payload})
		return nil
	})

	var srv *http.Server
	switch *mode {
	case "hub":
		ln, err := net.Listen("tcp", *listen)
		if err != nil {
			return err
		}
		srv = &http.Server{Handler: n.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = srv.Serve(ln) }()
		emit(map[string]any{"event": "ready", "url": "ws://" + ln.Addr().String() + "/"})
	case "client":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := n.Connect(ctx, *connect)
		cancel()
		if err != nil {
			return err
		}
		emit(map[string]any{"event": "ready", "url": *connect})
	default:
		return errors.New("unknown -mode " + *mode)
	}

	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var c command
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return err
		}
		if c.Cmd == "quit" {
			break
		}
		if c.Cmd == "publish" {
			_ = b.Publish(context.Background(), bus.NewEvent(bus.Topic(c.Topic), c.Source, c.Payload))
		}
	}
	_ = n.Close()
	if srv != nil {
		_ = srv.Close()
	}
	return b.Close(context.Background())
}
