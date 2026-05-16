// Package version adds append-only version tracking to domain repositories.
//
// # DAG
//
// [DAG] is a directed acyclic graph of [Version] nodes. Each node stores
// a content-addressed SHA-256 hash, parent IDs, and a timestamp. The DAG
// supports:
//
//   - Append: add a version with validated parent references
//   - Heads: list leaf nodes (versions with no children)
//   - IsAncestor: ancestry queries between two versions
//   - CommonAncestor: find the nearest shared parent of two heads
//   - Branch detection: multiple heads indicate diverged history
//
// # VersionedRepository
//
// [VersionedRepository] wraps any [domain.Repository] and records each
// Create/Update/Delete as a new DAG node:
//
//	inner := memory.NewRepository[Widget]()
//	vr := version.NewVersionedRepository[Widget](inner)
//	vr.Create(ctx, &w)   // appends root version
//	vr.Update(ctx, &w)   // appends child of current head
//
// Additional methods:
//   - ListVersions: ordered history for an entity
//   - GetVersion: retrieve a specific snapshot
//   - Revert: reset entity to a prior version
//   - Heads: detect diverged branches
//
// # Sync Integration
//
// For cross-node convergence, combine with [sync.Replicator]. Each node
// independently appends versions; when entity state matches across peers,
// head hashes converge. Branch detection surfaces conflicts that need
// explicit merge resolution.
package version
