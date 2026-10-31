package rpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	crudv1 "hop.top/kit/contracts/proto/crud/v1"
	"hop.top/kit/contracts/proto/crud/v1/crudv1connect"
	"hop.top/kit/go/transport/rpc"
)

// Runs the shared corpus in contracts/rpc-v1 against the Go server. The
// vectors are the source of truth: a failure here means this server
// drifted from the contract, not that a vector should be edited.

const rpcConfDir = "../../../contracts/rpc-v1"

var rpcConfFiles = map[string]bool{"vectors.json": true}

type rpcConfStep struct {
	Name      string            `json:"name"`
	Procedure string            `json:"procedure"`
	Request   json.RawMessage   `json:"request"`
	Raw       string            `json:"raw"`
	Headers   map[string]string `json:"headers"`
	Status    int               `json:"status"`
	Response  json.RawMessage   `json:"response"`
	CodeOnly  string            `json:"code_only"`
}

type rpcConfGroup struct {
	Name string `json:"name"`
	Auth *struct {
		Token string `json:"token"`
	} `json:"auth"`
	Steps []rpcConfStep `json:"steps"`
}

type rpcConfFile struct {
	Schema      string         `json:"$schema"`
	ID          string         `json:"$id"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Groups      []rpcConfGroup `json:"groups"`
}

func loadRPCConf(t *testing.T) rpcConfFile {
	t.Helper()
	dir := filepath.FromSlash(rpcConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && !rpcConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f rpcConfFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("parse vectors.json: %v", err)
	}
	if f.Version != "v1" {
		t.Fatalf("version = %q, want v1", f.Version)
	}
	return f
}

// confEntities is the fixture handler the corpus describes: an
// in-memory store, `w-<n>` ids, sorted List, a panic on demand.
type confEntities struct {
	crudv1connect.UnimplementedEntityServiceHandler
	mu   sync.Mutex
	rows map[string]map[string]any
	seq  int
}

func (s *confEntities) Create(_ context.Context, r *connect.Request[crudv1.CreateRequest]) (*connect.Response[crudv1.CreateResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := r.Msg.GetEntity().AsMap()
	if e["name"] == "panic" {
		panic("kaboom")
	}
	s.seq++
	id := fmt.Sprintf("w-%d", s.seq)
	e["id"] = id
	s.rows[id] = e
	st, err := structpb.NewStruct(e)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&crudv1.CreateResponse{Entity: st}), nil
}

func (s *confEntities) Get(_ context.Context, r *connect.Request[crudv1.GetRequest]) (*connect.Response[crudv1.GetResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.rows[r.Msg.GetId()]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("entity %s not found", r.Msg.GetId()))
	}
	st, _ := structpb.NewStruct(e)
	return connect.NewResponse(&crudv1.GetResponse{Entity: st}), nil
}

func (s *confEntities) List(context.Context, *connect.Request[crudv1.ListRequest]) (*connect.Response[crudv1.ListResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.rows))
	for id := range s.rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := &crudv1.ListResponse{}
	for _, id := range ids {
		st, _ := structpb.NewStruct(s.rows[id])
		out.Entities = append(out.Entities, st)
	}
	return connect.NewResponse(out), nil
}

func (s *confEntities) Update(_ context.Context, r *connect.Request[crudv1.UpdateRequest]) (*connect.Response[crudv1.UpdateResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := r.Msg.GetEntity().AsMap()
	id, _ := e["id"].(string)
	if _, ok := s.rows[id]; !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("entity %s not found", id))
	}
	s.rows[id] = e
	st, _ := structpb.NewStruct(e)
	return connect.NewResponse(&crudv1.UpdateResponse{Entity: st}), nil
}

func (s *confEntities) Delete(_ context.Context, r *connect.Request[crudv1.DeleteRequest]) (*connect.Response[crudv1.DeleteResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, r.Msg.GetId())
	return connect.NewResponse(&crudv1.DeleteResponse{}), nil
}

func newConfServer(t *testing.T, g rpcConfGroup) *httptest.Server {
	t.Helper()
	opts := []rpc.ServerOption{
		rpc.WithInterceptors(rpc.RequestIDInterceptor(), rpc.RecoveryInterceptor(nil)),
	}
	if g.Auth != nil {
		want := "Bearer " + g.Auth.Token
		opts = append([]rpc.ServerOption{rpc.WithInterceptors(rpc.Authenticate(
			func(r *http.Request) (any, error) {
				if r.Header.Get("Authorization") != want {
					return nil, errors.New("bad credentials")
				}
				return "alice", nil
			}))}, opts...)
	}
	srv := rpc.NewServer(opts...)
	path, h := crudv1connect.NewEntityServiceHandler(
		&confEntities{rows: map[string]map[string]any{}},
		connect.WithInterceptors(srv.Interceptors()...),
	)
	srv.Handle(path, h)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func TestConformanceRPC(t *testing.T) {
	f := loadRPCConf(t)
	if len(f.Groups) == 0 {
		t.Fatal("groups is empty")
	}
	for _, g := range f.Groups {
		ts := newConfServer(t, g)
		for _, s := range g.Steps {
			body := s.Raw
			if body == "" {
				body = string(s.Request)
			}
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/crud.v1.EntityService/"+s.Procedure, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			for k, v := range s.Headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s/%s: %v", g.Name, s.Name, err)
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != s.Status {
				t.Errorf("%s/%s: status = %d, want %d (%s)", g.Name, s.Name, resp.StatusCode, s.Status, got)
				continue
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("%s/%s: content-type = %q", g.Name, s.Name, ct)
			}
			var gotV any
			if err := json.Unmarshal(got, &gotV); err != nil {
				t.Errorf("%s/%s: body is not JSON: %q", g.Name, s.Name, got)
				continue
			}
			if s.CodeOnly != "" {
				if code, _ := gotV.(map[string]any)["code"].(string); code != s.CodeOnly {
					t.Errorf("%s/%s: code = %q, want %q (%s)", g.Name, s.Name, code, s.CodeOnly, got)
				}
				continue
			}
			var wantV any
			if err := json.Unmarshal(s.Response, &wantV); err != nil {
				t.Fatalf("%s/%s: vector response: %v", g.Name, s.Name, err)
			}
			if !reflect.DeepEqual(gotV, wantV) {
				t.Errorf("%s/%s: body = %s, want %s", g.Name, s.Name, got, s.Response)
			}
		}
	}
}
