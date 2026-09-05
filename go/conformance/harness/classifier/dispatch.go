package classifier

import (
	xrrexec "hop.top/xrr/adapters/exec"
	xrrgrpc "hop.top/xrr/adapters/grpc"
	xrrhttp "hop.top/xrr/adapters/http"
	xrrredis "hop.top/xrr/adapters/redis"
	xrrsql "hop.top/xrr/adapters/sql"
)

// Overrides bundles the adopter-supplied classifier callbacks the
// harness threads from harness.Option values into the dispatcher.
// Zero value uses each adapter's default classifier.
type Overrides struct {
	Exec ExecClassifier
	GRPC func(service, method string) Class
}

// Classify dispatches to the per-adapter classifier for adapterID.
// Unknown adapters return ClassUnknown — adopters extending xrr
// with bespoke adapters wire their own classifier at the call
// site rather than route through this dispatcher.
//
// req is the typed xrr Request value for the named adapter; passing
// a value of a different shape returns ClassUnknown.
func Classify(adapterID string, req any, ov Overrides) Class {
	switch adapterID {
	case "http":
		if r, ok := req.(*xrrhttp.Request); ok {
			return ClassifyHTTP(r)
		}
	case "grpc":
		if r, ok := req.(*xrrgrpc.Request); ok {
			if ov.GRPC != nil {
				return ov.GRPC(r.Service, r.Method)
			}
			return ClassifyGRPC(r)
		}
	case "sql":
		if r, ok := req.(*xrrsql.Request); ok {
			return ClassifySQL(r)
		}
	case "redis":
		if r, ok := req.(*xrrredis.Request); ok {
			return ClassifyRedis(r)
		}
	case "fs":
		if r, ok := req.(*FSRequest); ok {
			return ClassifyFS(r)
		}
		// Payload-style fallback: cassette envelopes deserialize into
		// map[string]any; pull the "op" field if present.
		if m, ok := req.(map[string]any); ok {
			if op, _ := m["op"].(string); op != "" {
				return ClassifyFSOp(op)
			}
		}
	case "exec":
		if r, ok := req.(*xrrexec.Request); ok {
			return ClassifyExec(r, ov.Exec)
		}
	}
	return ClassUnknown
}
