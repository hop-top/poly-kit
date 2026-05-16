package svc

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ScenarioRef identifies a scenario by namespace + ID + optional
// version. Resolution semantics (latest, etc.) live in the ScenarioStore
// implementation; see design §6.
type ScenarioRef struct {
	Namespace string
	ID        string
	Version   string // "" => "latest"
}

// String renders the canonical "<ns>/<id>[@<version>]" form.
func (r ScenarioRef) String() string {
	out := r.Namespace + "/" + r.ID
	if r.Version != "" {
		out += "@" + r.Version
	}
	return out
}

// ScenarioMeta is the lightweight view of a scenario returned by
// metadata endpoints. Loading it must not require parsing the full
// scenario body — drivers can satisfy this from a sidecar index.
type ScenarioMeta struct {
	Ref            ScenarioRef
	SchemaVersion  string
	FactorCoverage []int
	Tier           int
	CreatedAt      time.Time
	Deprecated     bool
}

// Validation regexes per design §6.
var (
	nsRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	idRe      = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)
	versionRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// ValidNamespace reports whether s matches the namespace regex.
func ValidNamespace(s string) bool { return nsRe.MatchString(s) }

// ValidID reports whether s matches the scenario-ID regex.
func ValidID(s string) bool { return idRe.MatchString(s) }

// ValidVersion reports whether s matches the version regex.
func ValidVersion(s string) bool { return versionRe.MatchString(s) }

// ParseScenarioRef parses "ns/id" or "ns/id@version" per design §6.
// Empty version is allowed (caller treats it as "latest").
func ParseScenarioRef(raw string) (ScenarioRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ScenarioRef{}, fmt.Errorf("empty scenario ref")
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ScenarioRef{}, fmt.Errorf("malformed ref %q (want ns/id[@version])", raw)
	}
	ns := parts[0]
	idVer := strings.SplitN(parts[1], "@", 2)
	id := idVer[0]
	var ver string
	if len(idVer) == 2 {
		ver = idVer[1]
	}
	if !ValidNamespace(ns) {
		return ScenarioRef{}, fmt.Errorf("invalid namespace %q", ns)
	}
	if !ValidID(id) {
		return ScenarioRef{}, fmt.Errorf("invalid id %q", id)
	}
	if ver != "" && !ValidVersion(ver) {
		return ScenarioRef{}, fmt.Errorf("invalid version %q", ver)
	}
	return ScenarioRef{Namespace: ns, ID: id, Version: ver}, nil
}
