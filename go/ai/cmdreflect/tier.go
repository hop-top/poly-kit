package cmdreflect

import "hop.top/kit/go/ai/toolspec"

// Tier is a position on the canonical six-tier side-effect ladder.
// Adopters declare a tier through the kit/side-effect annotation, in
// either the legacy four-value vocabulary or the expanded six-value
// one; this package resolves both into a Tier.
type Tier string

const (
	// TierRead makes no observable state change.
	TierRead Tier = "read"
	// TierWriteLocal mutates working-scope state, reversibly.
	TierWriteLocal Tier = "write-local"
	// TierWriteShared mutates shared or upstream state, reversibly.
	TierWriteShared Tier = "write-shared"
	// TierDestructiveLocal mutates working-scope state
	// irreversibly.
	TierDestructiveLocal Tier = "destructive-local"
	// TierDestructiveShared mutates shared or upstream state
	// irreversibly.
	TierDestructiveShared Tier = "destructive-shared"
	// TierInteractive is session-bound: a shell, a TUI, a
	// supervisor that blocks until signaled.
	TierInteractive Tier = "interactive"
	// TierUnknown is the zero value, meaning no tier resolved. It
	// never appears on a Descriptor — the walker substitutes a
	// default — but resolveTier returns it so the caller can tell
	// "unrecognized" from "read".
	TierUnknown Tier = ""
)

// destructiveNames is the heuristic kit applies when a command
// carries no kit/side-effect annotation. It is a default, not a
// contract: an adopter who annotates gets exactly what they
// declared.
var destructiveNames = map[string]bool{
	"delete":  true,
	"remove":  true,
	"rm":      true,
	"destroy": true,
	"purge":   true,
	"drop":    true,
}

// resolveTier maps a kit/side-effect annotation value onto the
// ladder. Legacy values resolve conservatively: bare "write" lands
// at write-shared and bare "destructive" at destructive-shared,
// because the unscoped legacy vocabulary cannot say whether the
// effect stays local and assuming it does would understate risk.
//
// An unrecognized value returns TierUnknown, which the walker
// treats as a declaration defect rather than silently substituting
// a default — an adopter who wrote "destrutive" should learn about
// the typo, not ship a command reflected as read-only.
func resolveTier(raw string) Tier {
	switch raw {
	case "read":
		return TierRead
	case "write":
		return TierWriteShared
	case "write-local":
		return TierWriteLocal
	case "write-shared":
		return TierWriteShared
	case "destructive":
		return TierDestructiveShared
	case "destructive-local":
		return TierDestructiveLocal
	case "destructive-shared":
		return TierDestructiveShared
	case "interactive":
		return TierInteractive
	}
	return TierUnknown
}

// fsPermission returns the kit:fs:* token for a tier. Interactive
// maps to read because an interactive session by itself mutates
// nothing; the commands typed inside it carry their own tiers.
func fsPermission(t Tier) toolspec.Permission {
	switch t {
	case TierWriteLocal:
		return toolspec.PermFSWriteLocal
	case TierWriteShared:
		return toolspec.PermFSWriteShared
	case TierDestructiveLocal:
		return toolspec.PermFSDestructiveLocal
	case TierDestructiveShared:
		return toolspec.PermFSDestructiveShared
	}
	return toolspec.PermFSRead
}

// safetyLevel projects a tier onto the legacy three-value enum so
// existing toolspec consumers keep working.
func safetyLevel(t Tier) toolspec.SafetyLevel {
	switch t {
	case TierWriteLocal, TierWriteShared, TierInteractive:
		return toolspec.SafetyLevelCaution
	case TierDestructiveLocal, TierDestructiveShared:
		return toolspec.SafetyLevelDangerous
	}
	return toolspec.SafetyLevelSafe
}

// resolveNetwork maps a kit/network annotation value onto its
// permission token. Absent, empty, and unrecognized values all
// resolve to kit:network:none.
func resolveNetwork(raw string) toolspec.Permission {
	switch raw {
	case "egress:public":
		return toolspec.PermNetworkEgressPublic
	case "egress:private":
		return toolspec.PermNetworkEgressPrivate
	case "ingress":
		return toolspec.PermNetworkIngress
	}
	return toolspec.PermNetworkNone
}
