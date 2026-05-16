package ext

import (
	"context"
	"fmt"
	"sync"

	"charm.land/log/v2"
)

// Manager orchestrates multi-capability extension lifecycle. An extension
// declaring multiple capabilities (e.g. CapRegistry|CapHook) gets routed
// to each mechanism from a single Add call.
type Manager struct {
	mu     sync.RWMutex
	exts   []Extension
	logger *log.Logger

	// Callbacks — set by mechanism packages via SetXxx before Add calls.
	onRegistry func(Extension)
	onHook     func(Extension)
	onDiscover func(Extension)
	onConfig   func(Extension)
}

// NewManager returns a Manager. Pass nil logger to disable logging.
func NewManager(logger *log.Logger) *Manager {
	return &Manager{logger: logger}
}

// SetOnRegistry sets the callback invoked for extensions with CapRegistry.
// Must be called before Add; not safe for concurrent use with Add.
func (m *Manager) SetOnRegistry(fn func(Extension)) { m.onRegistry = fn }

// SetOnHook sets the callback invoked for extensions with CapHook.
// Must be called before Add; not safe for concurrent use with Add.
func (m *Manager) SetOnHook(fn func(Extension)) { m.onHook = fn }

// SetOnDiscover sets the callback invoked for extensions with CapDiscover.
// Must be called before Add; not safe for concurrent use with Add.
func (m *Manager) SetOnDiscover(fn func(Extension)) { m.onDiscover = fn }

// SetOnConfig sets the callback invoked for extensions with CapConfig.
// Must be called before Add; not safe for concurrent use with Add.
func (m *Manager) SetOnConfig(fn func(Extension)) { m.onConfig = fn }

// Add routes an extension to all mechanisms matching its capabilities.
func (m *Manager) Add(e Extension) {
	m.mu.Lock()
	m.exts = append(m.exts, e)
	m.mu.Unlock()

	caps := e.Capabilities()
	name := e.Meta().Name

	if caps.Has(CapRegistry) && m.onRegistry != nil {
		m.log("registering %q in registry", name)
		m.onRegistry(e)
	}
	if caps.Has(CapHook) && m.onHook != nil {
		m.log("registering %q in hook bus", name)
		m.onHook(e)
	}
	if caps.Has(CapDiscover) && m.onDiscover != nil {
		m.log("registering %q in discover", name)
		m.onDiscover(e)
	}
	if caps.Has(CapConfig) && m.onConfig != nil {
		m.log("registering %q in config", name)
		m.onConfig(e)
	}
}

// InitAll calls Init on every added extension. Stops on first error.
func (m *Manager) InitAll(ctx context.Context) error {
	m.mu.RLock()
	exts := make([]Extension, len(m.exts))
	copy(exts, m.exts)
	m.mu.RUnlock()

	for _, e := range exts {
		m.log("initializing %q", e.Meta().Name)
		if err := e.Init(ctx); err != nil {
			return fmt.Errorf("ext: init %q: %w", e.Meta().Name, err)
		}
	}
	return nil
}

// CloseAll calls Close on every added extension in reverse order.
// Collects all errors.
func (m *Manager) CloseAll() []error {
	m.mu.RLock()
	exts := make([]Extension, len(m.exts))
	copy(exts, m.exts)
	m.mu.RUnlock()

	var errs []error
	for i := len(exts) - 1; i >= 0; i-- {
		e := exts[i]
		m.log("closing %q", e.Meta().Name)
		if err := e.Close(); err != nil {
			errs = append(errs, fmt.Errorf("ext: close %q: %w", e.Meta().Name, err))
		}
	}
	return errs
}

// Extensions returns all added extensions.
func (m *Manager) Extensions() []Extension {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Extension, len(m.exts))
	copy(out, m.exts)
	return out
}

func (m *Manager) log(format string, args ...any) {
	if m.logger != nil {
		m.logger.Debugf(format, args...)
	}
}
