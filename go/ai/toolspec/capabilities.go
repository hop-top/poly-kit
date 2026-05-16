package toolspec

import "encoding/json"

// Capability describes a single discoverable capability of a service.
type Capability struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // "resource", "endpoint", "command"
	Path        string   `json:"path"`
	Methods     []string `json:"methods,omitempty"`
	Description string   `json:"description,omitempty"`
}

// CapabilitySet is the full set of capabilities a service exposes.
type CapabilitySet struct {
	ServiceName  string       `json:"service"`
	Version      string       `json:"version,omitempty"`
	Capabilities []Capability `json:"capabilities"`
}

// NewCapabilitySet returns a CapabilitySet with an empty (non-nil)
// Capabilities slice, ensuring JSON serializes to [] not null.
func NewCapabilitySet(svc, version string) CapabilitySet {
	return CapabilitySet{
		ServiceName:  svc,
		Version:      version,
		Capabilities: []Capability{},
	}
}

// Add appends a capability to the set.
func (cs *CapabilitySet) Add(c Capability) {
	cs.Capabilities = append(cs.Capabilities, c)
}

// JSON serializes the capability set.
func (cs *CapabilitySet) JSON() ([]byte, error) {
	return json.Marshal(cs)
}

// Merge adds capabilities from other, deduplicating by Name+Path.
func (cs *CapabilitySet) Merge(other CapabilitySet) {
	seen := make(map[string]bool, len(cs.Capabilities))
	for _, c := range cs.Capabilities {
		seen[c.Name+"\x00"+c.Path] = true
	}
	for _, c := range other.Capabilities {
		key := c.Name + "\x00" + c.Path
		if !seen[key] {
			cs.Capabilities = append(cs.Capabilities, c)
			seen[key] = true
		}
	}
}
