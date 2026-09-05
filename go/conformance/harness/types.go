package harness

import (
	xrrexec "hop.top/xrr/adapters/exec"
	xrrgrpc "hop.top/xrr/adapters/grpc"
	xrrhttp "hop.top/xrr/adapters/http"
	xrrredis "hop.top/xrr/adapters/redis"
	xrrsql "hop.top/xrr/adapters/sql"
)

// Aliases for the xrr request types used by the harness internals.
// Keeping aliases here means the rest of the package imports
// `harness.xrrHTTPReq` rather than threading a long xrrhttp path
// through every call site.
type (
	xrrHTTPReq  = xrrhttp.Request
	xrrSQLReq   = xrrsql.Request
	xrrGRPCReq  = xrrgrpc.Request
	xrrRedisReq = xrrredis.Request
	xrrExecReq  = xrrexec.Request
)
