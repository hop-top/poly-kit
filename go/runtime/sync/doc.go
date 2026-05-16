// Package sync provides local-first entity replication with multi-remote support.
//
// The replication model is remote-centric: each Replicator instance owns one
// domain.Repository and maintains connections to N named remotes. Each remote
// has a SyncMode (Bidirectional, PushOnly, PullOnly) and an optional filter.
//
// Data flow:
//
//	Local changes → enqueued as Diff → pushed to eligible remotes
//	Remote changes → pulled via Transport → applied to local repo
//
// Key components:
//
//   - Replicator: orchestrates push/pull loops per remote; subscribes to
//     bus events for automatic change capture.
//   - Transport: push/pull/ping interface; implementations include
//     MemoryTransport (testing) and HTTPTransport (production).
//   - RemoteSet: concurrent-safe named remote registry.
//   - Clock: hybrid logical clock for causal ordering across nodes.
//   - WallClock: plain wall-time interface for deterministic time in
//     tests; distinct from Clock (HLC) which returns Timestamps. Use
//     System for production, FixedClock or MockWallClock for tests.
//   - Diff: entity-level change record with before/after JSON snapshots.
//   - MergeFunc: pluggable conflict resolution.
//
// Combine with domain/version.VersionedRepository for full version-tracked
// replication where each mutation appends a DAG node.
package sync
