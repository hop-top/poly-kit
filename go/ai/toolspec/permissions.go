package toolspec

// Permission is a typed permission token emitted into Safety.Permissions
// by the toolspec walker. The closed kit-defined set lives under the
// "kit:" namespace; consumers MAY define their own under their own
// namespace prefix (e.g. "myorg:db:write") without colliding.
//
// See ADR-0021 (kit/ai/toolspec safety tier ladder + permissions
// vocabulary) for the canonical mapping from cobra annotations to
// permissions and for the harness default-policy table.
type Permission string

// Filesystem permissions. Exactly one is emitted per command, derived
// from the resolved kit/side-effect tier. Order matches the tier
// ladder from least to most risky.
const (
	// PermFSRead — no observable filesystem state change.
	// Emitted when the resolved tier is "read".
	PermFSRead Permission = "kit:fs:read"

	// PermFSWriteLocal — mutates CWD-scoped state, reversible.
	// Emitted when the resolved tier is "write-local".
	PermFSWriteLocal Permission = "kit:fs:write:local"

	// PermFSWriteShared — mutates shared infra/upstream, reversible.
	// Emitted when the resolved tier is "write-shared" (or the
	// legacy "write" value, which maps conservatively to shared).
	PermFSWriteShared Permission = "kit:fs:write:shared"

	// PermFSDestructiveLocal — irreversible local mutation.
	// Emitted when the resolved tier is "destructive-local".
	PermFSDestructiveLocal Permission = "kit:fs:destructive:local"

	// PermFSDestructiveShared — irreversible shared mutation.
	// Emitted when the resolved tier is "destructive-shared" (or
	// the legacy "destructive" value, which maps conservatively to
	// shared).
	PermFSDestructiveShared Permission = "kit:fs:destructive:shared"
)

// Network permissions. Exactly one is emitted per command, derived
// from the kit/network annotation (default "none" when absent).
const (
	// PermNetworkNone — no network use (default).
	PermNetworkNone Permission = "kit:network:none"

	// PermNetworkEgressPublic — outbound to the public internet
	// (HTTPS APIs, package registries, public services).
	PermNetworkEgressPublic Permission = "kit:network:egress:public"

	// PermNetworkEgressPrivate — outbound to private/internal networks
	// (VPN-only services, internal control planes, on-prem databases).
	PermNetworkEgressPrivate Permission = "kit:network:egress:private"

	// PermNetworkIngress — listens for incoming connections (a server,
	// a webhook receiver, an interactive serve subcommand).
	PermNetworkIngress Permission = "kit:network:ingress"
)

// Capability permissions. Forward-looking; emitted only when the
// corresponding kit/* annotation is set. Additive — a command MAY
// carry both, neither, or one.
const (
	// PermExecSubprocess — the command spawns a subprocess (e.g. git
	// invoking ssh, kit shelling out to a build tool). Emitted when
	// the kit/exec annotation is set.
	PermExecSubprocess Permission = "kit:exec:subprocess"

	// PermBusPublish — the command publishes events to the kit event
	// bus. Emitted when the kit/bus-publish annotation is set.
	PermBusPublish Permission = "kit:bus:publish"
)

// String returns the permission as a plain string. Convenience for
// callers populating Safety.Permissions []string without explicit
// casts at every site.
func (p Permission) String() string { return string(p) }
