package classifier

import (
	"strings"

	xrrgrpc "hop.top/xrr/adapters/grpc"
)

// gRPC method-name prefix tables. The lookup is case-sensitive
// against the Title-cased prefix because the canonical proto3 style
// is `service.MethodName` with CamelCase verbs (Get/List/Create…).
var (
	grpcReadPrefixes = []string{
		"Get", "List", "Watch", "Stream", "Find", "Search",
		"Lookup", "Read", "Describe", "Check", "Has", "Exists",
		"Count", "Query", "Fetch", "Show",
	}
	grpcWritePrefixes = []string{
		"Create", "Update", "Patch", "Set", "Add", "Put",
		"Insert", "Modify", "Mutate", "Apply", "Save", "Append",
		"Write", "Publish", "Send", "Push", "Register",
	}
	grpcDestructivePrefixes = []string{
		"Delete", "Remove", "Drop", "Destroy", "Erase",
		"Purge", "Reset", "Wipe", "Clear", "Cancel", "Revoke",
	}
)

// ClassifyGRPC returns the Class for a gRPC call by inspecting the
// method-name prefix per the conventional verb-noun pattern. The
// classifier is a default heuristic — adopters with proto schemas
// that don't follow the convention override per-call via
// harness.WithGRPCClassifier.
func ClassifyGRPC(req *xrrgrpc.Request) Class {
	if req == nil {
		return ClassUnknown
	}
	return ClassifyGRPCMethod(req.Service, req.Method)
}

// ClassifyGRPCMethod is the underlying classifier the adopter
// override (harness.WithGRPCClassifier) plugs into. Service is
// accepted for symmetry but not consulted today.
func ClassifyGRPCMethod(_, method string) Class {
	if method == "" {
		return ClassUnknown
	}
	for _, p := range grpcDestructivePrefixes {
		if strings.HasPrefix(method, p) {
			return ClassDestructive
		}
	}
	for _, p := range grpcWritePrefixes {
		if strings.HasPrefix(method, p) {
			return ClassWrite
		}
	}
	for _, p := range grpcReadPrefixes {
		if strings.HasPrefix(method, p) {
			return ClassRead
		}
	}
	return ClassUnknown
}
