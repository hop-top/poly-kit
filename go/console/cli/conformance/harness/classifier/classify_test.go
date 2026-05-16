package classifier

import (
	"testing"

	xrrexec "hop.top/xrr/adapters/exec"
	xrrgrpc "hop.top/xrr/adapters/grpc"
	xrrhttp "hop.top/xrr/adapters/http"
	xrrsql "hop.top/xrr/adapters/sql"
)

func TestHTTP(t *testing.T) {
	cases := []struct {
		method string
		want   Class
	}{
		{"GET", ClassRead},
		{"head", ClassRead},
		{"OPTIONS", ClassRead},
		{"POST", ClassWrite},
		{"PUT", ClassWrite},
		{"PATCH", ClassWrite},
		{"DELETE", ClassDestructive},
		{"PROPFIND", ClassUnknown},
		{"", ClassUnknown},
	}
	for _, c := range cases {
		got := ClassifyHTTP(&xrrhttp.Request{Method: c.method})
		if got != c.want {
			t.Errorf("HTTP %q: got %s, want %s", c.method, got, c.want)
		}
	}
	if got := ClassifyHTTP(nil); got != ClassUnknown {
		t.Errorf("HTTP nil: got %s, want unknown", got)
	}
}

func TestGRPC(t *testing.T) {
	cases := []struct {
		method string
		want   Class
	}{
		{"GetUser", ClassRead},
		{"ListUsers", ClassRead},
		{"WatchEvents", ClassRead},
		{"CreateMission", ClassWrite},
		{"UpdatePayload", ClassWrite},
		{"PatchConfig", ClassWrite},
		{"DeleteMission", ClassDestructive},
		{"PurgeQueue", ClassDestructive},
		{"DoTheThing", ClassUnknown},
		{"", ClassUnknown},
	}
	for _, c := range cases {
		got := ClassifyGRPC(&xrrgrpc.Request{Service: "s.Svc", Method: c.method})
		if got != c.want {
			t.Errorf("gRPC %q: got %s, want %s", c.method, got, c.want)
		}
	}
}

func TestSQL(t *testing.T) {
	cases := []struct {
		query string
		want  Class
	}{
		{"SELECT * FROM t", ClassRead},
		{"  select 1  ", ClassRead},
		{"SHOW TABLES", ClassRead},
		{"EXPLAIN SELECT 1", ClassRead},
		{"INSERT INTO t VALUES (1)", ClassWrite},
		{"UPDATE t SET x = 1", ClassWrite},
		{"CREATE TABLE t (id int)", ClassWrite},
		{"DELETE FROM t WHERE id = 1", ClassDestructive},
		{"DROP TABLE t", ClassDestructive},
		{"TRUNCATE TABLE t", ClassDestructive},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", ClassRead},
		{"WITH cte AS (DELETE FROM t RETURNING *) SELECT * FROM cte", ClassDestructive},
		{"YOLO * FROM t", ClassUnknown},
		{"", ClassUnknown},
	}
	for _, c := range cases {
		got := ClassifySQLQuery(c.query)
		if got != c.want {
			t.Errorf("SQL %q: got %s, want %s", c.query, got, c.want)
		}
	}
}

func TestRedis(t *testing.T) {
	cases := []struct {
		cmd  string
		args []string
		want Class
	}{
		{"GET", []string{"k"}, ClassRead},
		{"get", []string{"k"}, ClassRead},
		{"PING", nil, ClassRead},
		{"SET", []string{"k", "v"}, ClassWrite},
		{"HSET", []string{"h", "f", "v"}, ClassWrite},
		{"DEL", []string{"k"}, ClassDestructive},
		{"UNLINK", []string{"k"}, ClassDestructive},
		{"FLUSHALL", nil, ClassDestructive},
		{"SHUTDOWN", nil, ClassDestructive},
		{"CLUSTER", []string{"INFO"}, ClassRead},
		{"CLUSTER", []string{"RESET"}, ClassDestructive},
		{"MEMORY", []string{"PURGE"}, ClassDestructive},
		{"CLIENT", []string{"KILL", "id", "1"}, ClassDestructive},
		{"CLIENT", []string{"LIST"}, ClassRead},
		{"SCRIPT", []string{"FLUSH"}, ClassDestructive},
		{"DEBUG", []string{"RELOAD"}, ClassDestructive},
		{"BOGUS", nil, ClassUnknown},
		{"", nil, ClassUnknown},
	}
	for _, c := range cases {
		got := ClassifyRedisCmd(c.cmd, c.args)
		if got != c.want {
			t.Errorf("Redis %q %v: got %s, want %s", c.cmd, c.args, got, c.want)
		}
	}
}

func TestFS(t *testing.T) {
	cases := []struct {
		op   string
		want Class
	}{
		{FSOpWrite, ClassWrite},
		{FSOpMkdir, ClassWrite},
		{FSOpChmod, ClassWrite},
		{FSOpRemove, ClassDestructive},
		{FSOpRename, ClassDestructive},
		{FSOpTruncate, ClassDestructive},
		{"bespoke", ClassWrite},
		{"", ClassUnknown},
	}
	for _, c := range cases {
		got := ClassifyFSOp(c.op)
		if got != c.want {
			t.Errorf("FS %q: got %s, want %s", c.op, got, c.want)
		}
	}
}

func TestExec(t *testing.T) {
	// Default classifier is conservative: every exec is Write.
	got := ClassifyExec(&xrrexec.Request{Argv: []string{"ls"}}, nil)
	if got != ClassWrite {
		t.Errorf("default exec: got %s, want %s", got, ClassWrite)
	}
	// Custom classifier wins.
	custom := func(argv []string) Class {
		if len(argv) > 0 && argv[0] == "ls" {
			return ClassRead
		}
		return ClassWrite
	}
	got = ClassifyExec(&xrrexec.Request{Argv: []string{"ls", "-la"}}, custom)
	if got != ClassRead {
		t.Errorf("custom ls: got %s, want %s", got, ClassRead)
	}
	got = ClassifyExec(&xrrexec.Request{Argv: []string{"rm", "-rf"}}, custom)
	if got != ClassWrite {
		t.Errorf("custom rm: got %s, want %s", got, ClassWrite)
	}
}

func TestDispatch(t *testing.T) {
	// Each adapter routes through its typed classifier.
	if got := Classify("http", &xrrhttp.Request{Method: "GET"}, Overrides{}); got != ClassRead {
		t.Errorf("http GET: got %s", got)
	}
	if got := Classify("sql", &xrrsql.Request{Query: "DROP TABLE t"}, Overrides{}); got != ClassDestructive {
		t.Errorf("sql DROP: got %s", got)
	}
	// gRPC override beats the default heuristic.
	ov := Overrides{GRPC: func(svc, m string) Class { return ClassRead }}
	if got := Classify("grpc", &xrrgrpc.Request{Method: "DeleteMission"}, ov); got != ClassRead {
		t.Errorf("gRPC override: got %s", got)
	}
	// Unknown adapter.
	if got := Classify("nope", nil, Overrides{}); got != ClassUnknown {
		t.Errorf("unknown adapter: got %s", got)
	}
	// Wrong typed request returns unknown.
	if got := Classify("http", &xrrsql.Request{}, Overrides{}); got != ClassUnknown {
		t.Errorf("type mismatch: got %s", got)
	}
}

func TestIsMutating(t *testing.T) {
	cases := []struct {
		c    Class
		want bool
	}{
		{ClassRead, false},
		{ClassWrite, true},
		{ClassDestructive, true},
		{ClassUnknown, true},
	}
	for _, c := range cases {
		if got := c.c.IsMutating(); got != c.want {
			t.Errorf("%s.IsMutating(): got %v, want %v", c.c, got, c.want)
		}
	}
}
