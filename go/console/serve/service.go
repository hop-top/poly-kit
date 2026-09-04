package serve

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

// Service is one long-running thing a tool can serve: an HTTP API, a
// local socket, an MCP channel, an RPC listener, a bus consumer. The
// four methods are the minimum a registration must provide
// (contract §"Service registration").
//
// Implementations are transport-specific and adopter- or kit-owned;
// this package never constructs one.
type Service interface {
	// Name is the stable service identifier. It is a CLI word, a
	// config key segment, and a bus topic payload value at once, so
	// it must satisfy [ValidateName] and must not change across
	// releases (contract §"Naming rules").
	Name() string

	// Start begins serving and blocks until ctx is canceled or the
	// service fails. Returning nil after cancellation is a clean
	// stop; returning a non-nil error is a failure, and under the
	// default failure policy brings the whole supervisor down
	// (contract §"One service fails while others run").
	//
	// Start must report readiness through ready exactly once, after
	// every acquisition that can fail deterministically has
	// succeeded — the listener bound, the socket file created, the
	// subscription attached (contract §"Readiness").
	Start(ctx context.Context, ready func()) error

	// Ready reports whether the service is currently accepting work.
	// It is readiness, not liveness: a ready service may be idle,
	// and may later fail.
	Ready() bool

	// Stop drains in-flight work and releases resources. The caller
	// bounds it with the service's stop timeout and abandons a Stop
	// that exceeds it, so an implementation must respect ctx rather
	// than assume it will be allowed to finish
	// (contract §"Ordered stop").
	Stop(ctx context.Context) error
}

// Validator is the optional configuration gate a [Service] may
// implement. Resolution calls it as the second of the three
// validation gates, before the policy gate and after registration
// (contract §"The override rule").
//
// A nil return means the resolved configuration is complete and
// usable. Any error is a configuration failure and exits 2.
type Validator interface {
	Validate() error
}

// Dependent is the optional ordering declaration a [Service] may
// implement. Start order is topological over DependsOn with ties
// broken by registration order; stop order is the exact reverse of
// the order services actually started (contract §"Ordering").
type Dependent interface {
	DependsOn() []string
}

// Classified is the optional policy declaration a [Service] may
// implement. The returned side-effect and network classes are the
// input to the third validation gate, resolved against the table in
// hop.top/kit/go/ai/toolspec/policy. A service that does not
// implement it is treated as unclassified and passes the gate.
type Classified interface {
	// Class returns the kit/side-effect and kit/network values for
	// this service, in that order.
	Class() (sideEffect, network string)
}

// Default lifecycle budgets. These mirror the existing HTTP defaults
// in hop.top/kit/go/transport/api so a service built on
// api.ListenAndServe inherits the same numbers it already had
// (contract §"Configuration surface").
const (
	// DefaultReadyTimeout is the budget from Start to readiness.
	// Config key: services.<name>.ready_timeout.
	DefaultReadyTimeout = 30 * time.Second

	// DefaultStopTimeout is the budget for one Stop.
	// Config key: services.<name>.stop_timeout.
	DefaultStopTimeout = 30 * time.Second

	// DefaultShutdownTimeout is the supervisor's total shutdown
	// budget across every service.
	// Config key: services.shutdown_timeout.
	DefaultShutdownTimeout = 60 * time.Second
)

// Config is the resolved services.<name> block for one service
// (contract §"Configuration surface"). Service-specific keys live in
// the same block and are read by the service itself; only the
// lifecycle keys are modeled here.
type Config struct {
	// Enabled is services.<name>.enabled. It decides whether the
	// supervisor form starts this service, and defaults to false: a
	// service that starts listening because a dependency upgrade
	// added it to the registry is an unrequested open port.
	Enabled bool

	// ReadyTimeout is services.<name>.ready_timeout. Zero means
	// DefaultReadyTimeout.
	ReadyTimeout time.Duration

	// StopTimeout is services.<name>.stop_timeout. Zero means
	// DefaultStopTimeout.
	StopTimeout time.Duration
}

// nameRE is the identifier grammar from contract §"Naming rules":
// lowercase ASCII, digits, and internal hyphens.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// reservedNames may not be registered. They are reserved for
// selector vocabulary and would be ambiguous with a service of the
// same name (contract §"Naming rules").
var reservedNames = map[string]struct{}{
	"all":  {},
	"none": {},
	"list": {},
}

// ValidateName reports whether name is a usable service identifier.
// It returns an error for the empty string, for anything outside the
// `^[a-z][a-z0-9-]*$` grammar, and for a reserved word.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("serve: service name is empty")
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf(
			"serve: service name %q must be lowercase letters, digits, or hyphens, starting with a letter",
			name,
		)
	}
	if _, ok := reservedNames[name]; ok {
		return fmt.Errorf("serve: service name %q is reserved", name)
	}
	return nil
}

// IsReservedName reports whether name is one of the reserved selector
// words (all, none, list).
func IsReservedName(name string) bool {
	_, ok := reservedNames[name]
	return ok
}
