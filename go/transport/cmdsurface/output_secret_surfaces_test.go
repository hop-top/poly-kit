package cmdsurface_test

// A command declaring secret output, called over each transport that
// serves one: the caller's response carries the value as the command
// produced it, byte for byte what an undeclared twin answers, and none
// of the bridge's records holds it.

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
	"hop.top/kit/go/transport/mcpsdk"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// osSurface is one transport serving the tree.
type osSurface struct {
	name string
	// call invokes the leaf and returns the response bytes.
	call func(t *testing.T, leaf string) string
	// stream invokes the leaf on the transport's streaming path and
	// returns what the caller received; nil when it has none.
	stream func(t *testing.T, leaf string) string
}

func osOptions(rig *cmdsurface.OSRig) []cmdsurface.Option {
	return []cmdsurface.Option{cmdsurface.WithRunner(cmdsurface.OSRunner()), cmdsurface.WithSinks(rig.Specs()...)}
}

func osREST(t *testing.T, rig *cmdsurface.OSRig) osSurface {
	b := cmdsurface.New(cmdsurface.OSTree(), osOptions(rig)...)
	b.Expose("*", cmdsurface.SurfaceREST)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	get := func(t *testing.T, route string) string {
		_, body := do(t, http.MethodGet, srv.URL+route, "", nil)
		return body
	}
	return osSurface{
		name:   "rest",
		call:   func(t *testing.T, leaf string) string { return get(t, "/v1/commands/"+leaf) },
		stream: func(t *testing.T, leaf string) string { return get(t, "/v1/commands/"+leaf+"/stream") },
	}
}

func osRPC(t *testing.T, rig *cmdsurface.OSRig) osSurface {
	b := cmdsurface.New(cmdsurface.OSTree(), osOptions(rig)...)
	b.Expose("*", cmdsurface.SurfaceRPC)
	srv := newMountServer()
	if err := cmdsurface.MountRPC(b, srv); err != nil {
		t.Fatal(err)
	}
	ts := newH2CServer(t, srv)
	client := cmdsurfacev1connect.NewCommandsClient(h2cClient(), ts.URL, connect.WithProtoJSON())
	return osSurface{
		name: "rpc",
		call: func(t *testing.T, leaf string) string {
			resp, err := client.Invoke(context.Background(), invocation(leaf))
			if err != nil {
				return err.Error()
			}
			raw, _ := protojson.Marshal(resp.Msg)
			return string(raw)
		},
		stream: func(t *testing.T, leaf string) string {
			stream, err := client.InvokeStream(context.Background(), invocation(leaf))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()
			var out strings.Builder
			for stream.Receive() {
				raw, _ := protojson.Marshal(stream.Msg())
				out.Write(raw)
				out.WriteByte('\n')
			}
			if err := stream.Err(); err != nil {
				out.WriteString(err.Error())
			}
			return out.String()
		},
	}
}

func osMCP(t *testing.T, rig *cmdsurface.OSRig) osSurface {
	b := cmdsurface.New(cmdsurface.OSTree(), osOptions(rig)...)
	b.Expose("*", cmdsurface.SurfaceMCP)
	r := api.NewRouter()
	if err := mcpsdk.Mount(b, r); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "os", Version: "0"}, nil)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return osSurface{
		name: "mcp",
		call: func(t *testing.T, leaf string) string {
			res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: leaf})
			if err != nil {
				return err.Error()
			}
			raw, _ := json.Marshal(res)
			return string(raw)
		},
	}
}

func osSocket(t *testing.T, rig *cmdsurface.OSRig) osSurface {
	dir, err := os.MkdirTemp("", "os")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	svc := transportsvc.NewTransportService("socket", cmdsurface.OSTree(), cmdsurface.SurfaceSocket,
		socket.New(path), transportsvc.Expose("*"), transportsvc.WithBridgeOptions(osOptions(rig)...))
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	errc := make(chan error, 1)
	go func() { errc <- svc.Start(ctx, func() { ready <- struct{}{} }) }()
	select {
	case <-ready:
	case err := <-errc:
		cancel()
		t.Fatalf("socket: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("socket never ready")
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
		cancel()
		select {
		case <-errc:
		case <-time.After(5 * time.Second):
		}
	})
	return osSurface{
		name: "socket",
		call: func(t *testing.T, leaf string) string {
			conn, err := net.Dial("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if err := json.NewEncoder(conn).Encode(socket.Request{Path: []string{leaf}}); err != nil {
				t.Fatal(err)
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			return line
		},
	}
}

var osSurfaces = []struct {
	name  string
	build func(*testing.T, *cmdsurface.OSRig) osSurface
}{{"rest", osREST}, {"rpc", osRPC}, {"mcp", osMCP}, {"socket", osSocket}}

func osAll() []string {
	return []string{cmdsurface.OSValue, cmdsurface.OSNested, cmdsurface.OSRow1, cmdsurface.OSRow2, cmdsurface.OSText}
}

func assertRecordsHold(t *testing.T, rig *cmdsurface.OSRig, what string, values ...string) {
	t.Helper()
	all := rig.Written()
	per := rig.PerSink()
	for _, v := range values {
		if !strings.Contains(all, v) {
			continue
		}
		for name, b := range per {
			if strings.Contains(b, v) {
				t.Errorf("%s: %q reached the %s sink:\n%s", what, v, name, b)
			}
		}
	}
}

func TestSecretOutputSurfaces_CallerKeepsValueRecordsDoNot(t *testing.T) {
	for _, sf := range osSurfaces {
		t.Run(sf.name, func(t *testing.T) {
			rig, control := cmdsurface.NewOSRig(t), cmdsurface.NewOSRig(t)
			decl, twin := sf.build(t, rig), sf.build(t, control)

			// Structured output, nested fields and a list of rows.
			got, want := decl.call(t, "reveal"), twin.call(t, "plain")
			for _, v := range []string{cmdsurface.OSValue, cmdsurface.OSNested, cmdsurface.OSRow1, cmdsurface.OSRow2} {
				if !strings.Contains(got, v) {
					t.Errorf("caller lost %q: %s", v, got)
				}
			}
			if got != want {
				t.Errorf("response differs from the undeclared twin's:\n%s\n%s", got, want)
			}
			// Text only.
			if r := decl.call(t, "text"); !strings.Contains(r, cmdsurface.OSText) {
				t.Errorf("caller lost the text value: %s", r)
			}
			// A failure carrying the value in its message.
			_ = decl.call(t, "fails")

			assertRecordsHold(t, rig, decl.name, osAll()...)
			// The harness would have seen a leak.
			if !strings.Contains(control.Written(), cmdsurface.OSNested) {
				t.Fatalf("%s: the undeclared twin left no value in any record; the check above proves nothing", decl.name)
			}
		})
	}
}

func TestSecretOutputSurfaces_StreamRecordsHoldNoValue(t *testing.T) {
	for _, sf := range osSurfaces {
		t.Run(sf.name, func(t *testing.T) {
			rig := cmdsurface.NewOSRig(t)
			s := sf.build(t, rig)
			if s.stream == nil {
				t.Skip("no streaming path on this transport")
			}
			got := s.stream(t, "reveal")
			if !strings.Contains(got, cmdsurface.OSValue) {
				t.Errorf("caller's stream lost the value: %s", got)
			}
			assertRecordsHold(t, rig, s.name+" stream", osAll()...)
		})
	}
}
