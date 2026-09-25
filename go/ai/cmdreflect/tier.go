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
	// TierUnannotated marks a command that declared no
	// kit/side-effect at all. It is NOT a position on the ladder:
	// it is the honest statement that kit reflected the command
	// and found no declaration, and it is what an unannotated
	// command resolves to unless the destructive-name heuristic
	// fires.
	//
	// It exists because the previous default — TierRead — made a
	// command that never declared a class indistinguishable from
	// one that declared read, and published the silent one to
	// agents and safety gates as safe. Every kit projection of
	// this tier is conservative: the legacy Level is Caution, the
	// filesystem permission is write-local (the weakest claim that
	// is not "reads nothing"), and the HTTP method is POST.
	//
	// It stays INVOCABLE. Kit's audited adopters leave a fifth to
	// a third of their commands unannotated; withholding them
	// would break working CLIs to punish missing metadata. The
	// coverage report is the pressure, not the surface.
	TierUnannotated Tier = "unannotated"
	// TierUnknown is the zero value, meaning no tier resolved. It
	// never appears on a Descriptor — the walker substitutes a
	// default — but resolveTier returns it so the caller can tell
	// "unrecognized" from "read" and from "undeclared".
	TierUnknown Tier = ""
)

// Declared reports whether t came from an adopter's annotation. It
// is false for TierUnannotated and TierUnknown — the two tiers that
// mean "nobody said" and "what was said does not parse".
func (t Tier) Declared() bool {
	return t != TierUnannotated && t != TierUnknown
}

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
//
// Unannotated maps to write-local rather than read: kit:fs:read is
// a positive claim that the command touches nothing, and kit has no
// grounds to make that claim for a command that said nothing. It
// stops short of the destructive tokens so an undeclared command is
// not mistaken for one an adopter marked dangerous.
func fsPermission(t Tier) toolspec.Permission {
	switch t {
	case TierWriteLocal, TierUnannotated:
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
// existing toolspec consumers keep working. Unannotated lands at
// caution, not safe: the enum has no "don't know" value and safe is
// the one answer kit cannot justify.
func safetyLevel(t Tier) toolspec.SafetyLevel {
	switch t {
	case TierWriteLocal, TierWriteShared, TierInteractive, TierUnannotated:
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
